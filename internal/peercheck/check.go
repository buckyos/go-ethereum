// Package peercheck diagnoses one operator-supplied endpoint without joining the
// local peer set, opening a chain database, or persisting a node identity.
package peercheck

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// Schema identifies reports consumed by the release node CLI.
const Schema = "usdb-peer-check:v1"

// Request binds the probe to the selected release, never to the target's claims.
type Request struct {
	Enode       string        `json:"enode"`
	NetworkID   uint64        `json:"network_id"`
	ChainID     uint64        `json:"chain_id"`
	GenesisHash common.Hash   `json:"genesis_hash"`
	Genesis     *core.Genesis `json:"genesis"`
	TimeoutSecs int           `json:"timeout_secs"`
}

// Check describes one independent layer. SKIPPED is never a successful check.
type Check struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Endpoint records the observed path to a single resolved IP address.
type Endpoint struct {
	IP        string `json:"ip"`
	Family    string `json:"family"`
	TCPPort   int    `json:"tcp_port"`
	UDPPort   int    `json:"udp_port"`
	TCP       Check  `json:"tcp"`
	Discovery Check  `json:"discovery"`
	Identity  Check  `json:"identity"`
	Hello     Check  `json:"hello"`
	ETH       Check  `json:"eth"`
	Usable    bool   `json:"usable"`
}

// Report is a point-in-time observation, not proof of synchronization or trust.
type Report struct {
	Schema      string      `json:"schema_version"`
	Enode       string      `json:"enode"`
	CheckedAt   string      `json:"checked_at"`
	State       string      `json:"state"`
	Usable      bool        `json:"usable"`
	Syntax      Check       `json:"syntax"`
	DNS         Check       `json:"dns"`
	Endpoints   []Endpoint  `json:"endpoints"`
	Warnings    []string    `json:"warnings"`
	NetworkID   uint64      `json:"network_id"`
	GenesisHash common.Hash `json:"genesis_hash"`
}

func pass() Check    { return Check{State: "PASS"} }
func skipped() Check { return Check{State: "SKIPPED", Reason: "PREREQUISITE_FAILED"} }
func failed(reason string, err error) Check {
	return Check{State: "FAIL", Reason: reason, Detail: err.Error()}
}

// Run checks DNS, signed discovery replies, TCP, RLPx identity and eth status.
// At most 16 addresses and 4 parallel probes share the request's total deadline.
func Run(parent context.Context, req Request) (Report, error) {
	report := Report{Schema: Schema, Enode: req.Enode, CheckedAt: time.Now().UTC().Format(time.RFC3339),
		State: "FAIL", Syntax: skipped(), DNS: skipped(), Endpoints: []Endpoint{}, Warnings: []string{},
		NetworkID: req.NetworkID, GenesisHash: req.GenesisHash}
	if req.TimeoutSecs < 1 || req.TimeoutSecs > 120 {
		return report, errors.New("timeout_secs must be between 1 and 120")
	}
	if req.Genesis == nil || req.Genesis.Config == nil || req.Genesis.Config.ChainID == nil ||
		!req.Genesis.Config.ChainID.IsUint64() || req.Genesis.Config.ChainID.Uint64() != req.ChainID ||
		req.Genesis.Number != 0 || req.Genesis.Difficulty == nil {
		return report, errors.New("release genesis or chain ID is invalid")
	}
	hash, err := genesisHash(req.Genesis)
	if err != nil {
		return report, err
	}
	if hash != req.GenesisHash {
		return report, errors.New("release genesis hash does not match the expected network")
	}
	n, err := enode.ParseForConfig(enode.ValidSchemes, req.Enode)
	if err != nil {
		report.Syntax = failed("INVALID_ENODE", err)
		return report, nil
	}
	if n.Pubkey() == nil || n.TCP() == 0 || n.UDP() == 0 || (n.IP() == nil && n.Hostname() == "") {
		report.Syntax = failed("INVALID_ENODE", errors.New("complete enode with TCP and UDP ports required"))
		return report, nil
	}
	report.Syntax = pass()
	ctx, cancel := context.WithTimeout(parent, time.Duration(req.TimeoutSecs)*time.Second)
	defer cancel()
	ips, truncated, err := resolve(ctx, n, net.DefaultResolver.LookupIPAddr)
	if err != nil {
		report.DNS = failed("DNS_LOOKUP_FAILED", err)
		return report, nil
	}
	report.DNS = pass()
	if n.Hostname() == "" {
		report.DNS.Detail = "literal IP; DNS not required"
	}
	if truncated {
		report.Warnings = append(report.Warnings, "Only the first 16 allowed DNS addresses were checked.")
	}
	report.Endpoints = make([]Endpoint, len(ips))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < 4 && worker < len(ips); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				report.Endpoints[i] = probe(ctx, enode.NewV4(n.Pubkey(), ips[i], n.TCP(), n.UDP()), req)
			}
		}()
	}
	for i := range ips {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for _, endpoint := range report.Endpoints {
		if endpoint.Usable {
			report.Usable, report.State = true, "PASS"
		} else if !report.Usable && (endpoint.TCP.State == "PASS" || endpoint.Discovery.State == "PASS") {
			report.State = "PARTIAL"
		}
	}
	return report, nil
}

func genesisHash(genesis *core.Genesis) (hash common.Hash, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("invalid release genesis: %v", recovered)
		}
	}()
	return genesis.ToBlock().Hash(), nil
}

func resolve(ctx context.Context, n *enode.Node, lookup func(context.Context, string) ([]net.IPAddr, error)) ([]net.IP, bool, error) {
	addresses := []net.IPAddr{{IP: n.IP()}}
	if n.Hostname() != "" {
		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var err error
		addresses, err = lookup(lookupCtx, n.Hostname())
		if err != nil {
			return nil, false, err
		}
	}
	var ips []net.IP
	seen := make(map[string]bool)
	for _, address := range addresses {
		ip := address.IP
		if ip.To16() == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || address.Zone != "" || seen[ip.String()] {
			continue
		}
		seen[ip.String()] = true
		if len(ips) == 16 {
			return ips, true, nil
		}
		ips = append(ips, ip)
	}
	if len(ips) == 0 {
		return nil, false, errors.New("no allowed IP addresses")
	}
	return ips, false, nil
}

func probe(parent context.Context, n *enode.Node, req Request) Endpoint {
	e := Endpoint{IP: n.IP().String(), Family: "ipv6", TCPPort: n.TCP(), UDPPort: n.UDP(),
		TCP: skipped(), Discovery: skipped(), Identity: skipped(), Hello: skipped(), ETH: skipped()}
	if n.IP().To4() != nil {
		e.Family = "ipv4"
	}
	if parent.Err() != nil {
		e.TCP = failed("PROBE_TIMEOUT", parent.Err())
		e.Discovery = e.TCP
		return e
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	key, err := crypto.GenerateKey()
	if err != nil {
		e.Identity = failed("PROBE_IDENTITY_FAILED", err)
		return e
	}
	// UDP and TCP are independent observations. A blocked UDP port must not
	// prevent us from reporting a usable TCP connection (or vice versa).
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := ping(ctx, n, key); err != nil {
			e.Discovery = failed("DISCOVERY_FAILED", err)
		} else {
			e.Discovery = pass()
		}
	}()
	dialer := net.Dialer{}
	fd, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(n.IP().String(), strconv.Itoa(n.TCP())))
	if err != nil {
		e.TCP = failed("TCP_CONNECT_FAILED", err)
	} else {
		e.TCP = pass()
		handshake(ctx, fd, n, key, req, &e)
		fd.Close()
	}
	<-done
	e.Usable = e.Discovery.State == "PASS" && e.ETH.State == "PASS"
	return e
}

func ping(ctx context.Context, n *enode.Node, key *ecdsa.PrivateKey) error {
	family := "udp6"
	if n.IP().To4() != nil {
		family = "udp4"
	}
	conn, err := net.ListenUDP(family, &net.UDPAddr{})
	if err != nil {
		return err
	}
	db, err := enode.OpenDB("")
	if err != nil {
		conn.Close()
		return err
	}
	defer db.Close()
	local := enode.NewLocalNode(db, key)
	local.SetFallbackUDP(conn.LocalAddr().(*net.UDPAddr).Port)
	udp, err := discover.ListenV4(conn, local, discover.Config{PrivateKey: key})
	if err != nil {
		conn.Close()
		return err
	}
	defer udp.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			udp.Close()
		case <-done:
		}
	}()
	if err := udp.Ping(n); err != nil {
		return fmt.Errorf("signed discovery Ping/Pong: %w", err)
	}
	return nil
}
