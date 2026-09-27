package discover

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/p2p/enode"
)

const (
	dnsBootstrapTimeout = 5 * time.Second
	dnsBootstrapRetry   = 30 * time.Second
	dnsBootstrapWorkers = 4
)

// dnsBootnode retains operator configuration across temporary resolver failures.
// Only resolved is shared with the table refresh goroutine (under tab.mutex).
type dnsBootnode struct {
	source   *enode.Node
	resolved *node
	failed   bool
}

func (tab *Table) dnsBootstrapLoop(ctx context.Context) {
	if len(tab.dnsBootnodes) == 0 {
		return
	}
	for {
		tab.resolveDNSBootnodes(ctx)
		timer := time.NewTimer(dnsBootstrapRetry)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}

// resolveDNSBootnodes limits parallel DNS queries and applies each answer as soon
// as it arrives. Other seeds can resolve concurrently; startup never waits.
func (tab *Table) resolveDNSBootnodes(ctx context.Context) {
	jobs := make(chan *dnsBootnode)
	var wg sync.WaitGroup
	for i := 0; i < min(len(tab.dnsBootnodes), dnsBootstrapWorkers); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seed := range jobs {
				tab.resolveDNSBootnode(ctx, seed)
			}
		}()
	}
enqueue:
	for _, seed := range tab.dnsBootnodes {
		select {
		case jobs <- seed:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(jobs)
	wg.Wait()
}

func (tab *Table) resolveDNSBootnode(ctx context.Context, seed *dnsBootnode) {
	lookupCtx, cancel := context.WithTimeout(ctx, dnsBootstrapTimeout)
	addresses, err := tab.lookupIP(lookupCtx, seed.source.Hostname())
	cancel()
	if ctx.Err() != nil {
		return
	}
	var resolved *node
	if err == nil {
		for _, address := range addresses {
			ip := address.IP
			if ip.To16() == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || address.Zone != "" {
				continue
			}
			if tab.netrestrict != nil && !tab.netrestrict.Contains(ip) {
				continue
			}
			n := enode.NewV4(seed.source.Pubkey(), ip, seed.source.TCP(), seed.source.UDP())
			resolved = wrapNode(n)
			break
		}
		if resolved == nil {
			err = errors.New("no allowed IP addresses")
		}
	}
	tab.mutex.Lock()
	seed.resolved = resolved
	tab.mutex.Unlock()
	if err != nil {
		// Warn once per outage, avoiding repeated warnings while DNS stays down.
		if !seed.failed {
			tab.log.Warn("Bootstrap DNS lookup failed; will retry", "id", seed.source.ID(), "host", seed.source.Hostname(), "retry", dnsBootstrapRetry, "err", err)
		} else {
			tab.log.Debug("Bootstrap DNS lookup still failing", "host", seed.source.Hostname(), "err", err)
		}
		seed.failed = true
		return
	}
	if seed.failed {
		tab.log.Info("Bootstrap DNS lookup recovered", "id", seed.source.ID(), "host", seed.source.Hostname(), "ip", resolved.IP())
	}
	seed.failed = false
	// The seed is still unverified; regular discovery Ping/Pong and ENR checks
	// authenticate it before it becomes a verified table entry.
	tab.addSeenNode(resolved)
}
