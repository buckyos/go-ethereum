package discover

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/netutil"
)

func dnsBootstrapTestNode(t *testing.T, host string) *enode.Node {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ip := enode.NewV4(&key.PublicKey, net.IPv4(127, 0, 0, 1), 31303, 31304)
	n, err := enode.ParseForConfig(enode.ValidSchemes, strings.Replace(ip.String(), "127.0.0.1", host, 1))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDNSBootstrapRecovery(t *testing.T) {
	dns := dnsBootstrapTestNode(t, "offline.invalid")
	literal := dnsBootstrapTestNode(t, "127.0.0.2")
	db, err := enode.OpenDB("")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	restrict, _ := netutil.ParseNetlist("127.0.0.0/8")
	tab, err := newTable(newPingRecorder(), db, Config{Bootnodes: []*enode.Node{dns, literal}, NetRestrict: restrict})
	if err != nil {
		t.Fatal(err)
	}
	if tab.getNode(literal.ID()) == nil || tab.getNode(dns.ID()) != nil {
		t.Fatal("unresolved DNS seed interfered with literal IP bootstrap")
	}
	lookupErr := error(&net.DNSError{Name: dns.Hostname(), Err: "no such host", IsNotFound: true})
	var answers []net.IPAddr
	tab.lookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > dnsBootstrapTimeout {
			t.Error("discovery DNS lookup must have a bounded deadline")
		}
		return answers, lookupErr
	}
	tab.resolveDNSBootnodes(context.Background())
	lookupErr = nil
	answers = []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}, {IP: net.ParseIP("::")}}
	tab.resolveDNSBootnodes(context.Background())
	if tab.getNode(dns.ID()) != nil {
		t.Fatal("failed DNS or netrestrict installed an unusable seed")
	}
	answers = []net.IPAddr{{IP: net.ParseIP("127.0.0.3")}}
	tab.resolveDNSBootnodes(context.Background())
	resolved := tab.getNode(dns.ID())
	if resolved == nil || !resolved.IP().Equal(answers[0].IP) || resolved.TCP() != dns.TCP() || resolved.UDP() != dns.UDP() {
		t.Fatalf("recovered DNS seed not available for discovery: %v", resolved)
	}
	// After eviction the resolved seed must still be available as a fallback.
	tab.delete(wrapNode(resolved))
	tab.loadSeedNodes()
	if tab.getNode(dns.ID()) == nil || dns.IP() != nil || dns.Hostname() != "offline.invalid" {
		t.Fatal("DNS recovery lost the source or its discovery fallback")
	}
	answers = nil
	tab.resolveDNSBootnodes(context.Background())
	if tab.dnsBootnodes[0].resolved != nil {
		t.Fatal("empty DNS answer kept a stale fallback endpoint")
	}
}

func TestDNSBootstrapDoesNotBlockStartupOrShutdown(t *testing.T) {
	slow := dnsBootstrapTestNode(t, "slow.invalid")
	fast := dnsBootstrapTestNode(t, "fast.invalid")
	db, err := enode.OpenDB("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	tab, err := newTable(newPingRecorder(), db, Config{Bootnodes: []*enode.Node{slow, fast}})
	if err != nil {
		t.Fatal(err)
	}
	started, added := make(chan struct{}), make(chan struct{}, 1)
	tab.lookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if host == slow.Hostname() {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	tab.nodeAddedHook = func(n *node) {
		if n.ID() == fast.ID() {
			select {
			case added <- struct{}{}:
			default:
			}
		}
	}
	go tab.loop()
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() { tab.close(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("blocked DNS lookup prevented shutdown")
		}
	})
	for _, ready := range []<-chan struct{}{started, tab.initDone, added} {
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			t.Fatal("slow DNS prevented initialization or a healthy DNS seed")
		}
	}
}
