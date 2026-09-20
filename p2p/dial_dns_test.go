package p2p

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/netutil"
)

// DNS lookups after parsing are injected; localhost only supplies the initial,
// deliberately obsolete endpoint without depending on an external DNS service.
func dnsTestNode(t *testing.T) *enode.Node {
	t.Helper()
	n := enode.NewV4(&newkey().PublicKey, net.IPv4(127, 0, 0, 1), 31303, 31304)
	n, err := enode.ParseV4(strings.Replace(n.URLv4(), "127.0.0.1", "localhost", 1))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

type dnsTestDialer func(context.Context, *enode.Node) (net.Conn, error)

func (f dnsTestDialer) Dial(ctx context.Context, n *enode.Node) (net.Conn, error) {
	return f(ctx, n)
}

func TestDialDNSRefreshAndFallback(t *testing.T) {
	n := dnsTestNode(t)
	answers := []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}, {IP: net.ParseIP("2001:db8::1")}}
	var dialed, connected []string
	lookups := 0
	config := dialConfig{
		lookupIP: func(ctx context.Context, host string) ([]net.IPAddr, error) {
			lookups++
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > defaultDialTimeout {
				t.Fatal("DNS resolution must have a bounded deadline")
			}
			if host != n.Hostname() {
				t.Fatalf("lost DNS hostname: %q", host)
			}
			return answers, nil
		},
		dialer: dnsTestDialer(func(ctx context.Context, dest *enode.Node) (net.Conn, error) {
			dialed = append(dialed, dest.IP().String())
			if dest.ID() != n.ID() || dest.TCP() != 31303 || dest.UDP() != 31304 {
				t.Fatal("resolved endpoint changed identity or configured ports")
			}
			if dest.IP().To4() != nil {
				deadline, _ := ctx.Deadline()
				if time.Until(deadline) > defaultDialTimeout/2 {
					t.Fatal("first candidate can consume the entire fallback budget")
				}
				return nil, errors.New("IPv4 route unavailable")
			}
			fd, remote := net.Pipe()
			remote.Close()
			return fd, nil
		}),
	}
	d := &dialScheduler{dialConfig: config.withDefaults(), ctx: context.Background(),
		setupFunc: func(fd net.Conn, _ connFlag, dest *enode.Node) error {
			defer fd.Close()
			connected = append(connected, dest.IP().String())
			return nil
		}}
	task := newDialTask(n, staticDialedConn)
	task.run(d)
	// Reuse the same static task after the peer disconnects and DDNS changes.
	answers = []net.IPAddr{{IP: net.ParseIP("2001:db8::2")}}
	task.run(d)
	if strings.Join(dialed, ",") != "192.0.2.1,2001:db8::1,2001:db8::2" ||
		strings.Join(connected, ",") != "2001:db8::1,2001:db8::2" || lookups != 2 {
		t.Fatalf("DNS refresh/fallback failed: dialed=%v connected=%v lookups=%d", dialed, connected, lookups)
	}
	if task.dest != n || n.Hostname() != "localhost" {
		t.Fatal("reconnect overwrote the configured DNS destination")
	}
}

func TestDialDNSFailureRecoveryAndNetRestrict(t *testing.T) {
	n := dnsTestNode(t)
	restrict := new(netutil.Netlist)
	restrict.Add("2001:db8::/32")
	lookupErr := errors.New("temporary DNS failure")
	var answers []net.IPAddr
	dials := 0
	config := dialConfig{netRestrict: restrict,
		lookupIP: func(context.Context, string) ([]net.IPAddr, error) { return answers, lookupErr },
		dialer: dnsTestDialer(func(ctx context.Context, dest *enode.Node) (net.Conn, error) {
			if !restrict.Contains(dest.IP()) {
				t.Fatal("DNS resolution bypassed netrestrict")
			}
			dials++
			return nil, errors.New("connection refused")
		}),
	}
	d := &dialScheduler{dialConfig: config.withDefaults(), ctx: context.Background()}
	if err := d.checkDial(n); err != nil {
		t.Fatalf("cached IP blocked a fresh DNS lookup: %v", err)
	}
	task := newDialTask(n, staticDialedConn)
	task.run(d)
	lookupErr = nil
	task.run(d) // Empty response must not reuse the cached address.
	answers = []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}, {IP: net.ParseIP("::")},
		{IP: net.ParseIP("ff02::1")}, {IP: net.ParseIP("fe80::1")},
		{IP: net.ParseIP("2001:db8::1"), Zone: "eth0"}}
	task.run(d)
	if dials != 0 {
		t.Fatal("failed or disallowed DNS result fell back to a stale endpoint")
	}
	answers = []net.IPAddr{{IP: net.ParseIP("2001:db8::2")}, {IP: net.ParseIP("2001:db8::2")}}
	task.run(d)
	if dials != 1 {
		t.Fatalf("recovered endpoint not dialed exactly once: %d", dials)
	}
}

func TestDialDNSCancellation(t *testing.T) {
	n := dnsTestNode(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, done := make(chan struct{}), make(chan struct{})
	config := dialConfig{lookupIP: func(ctx context.Context, host string) ([]net.IPAddr, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	d := &dialScheduler{dialConfig: config.withDefaults(), ctx: ctx}
	go func() {
		newDialTask(n, staticDialedConn).run(d)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("lookup did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("DNS lookup prevented scheduler shutdown")
	}
}

func TestDialSchedDNSReconnect(t *testing.T) {
	n := dnsTestNode(t)
	lookupCount := 0
	config := dialConfig{maxActiveDials: 1, maxDialPeers: 1,
		lookupIP: func(context.Context, string) ([]net.IPAddr, error) {
			lookupCount++
			return []net.IPAddr{{IP: net.ParseIP(fmt.Sprintf("2001:db8::%d", lookupCount))}}, nil
		}}
	runDialTest(t, config, []dialTestRound{
		{update: func(d *dialScheduler) { d.addStatic(n) },
			wantNewDials: []*enode.Node{enode.NewV4(n.Pubkey(), net.ParseIP("2001:db8::1"), n.TCP(), n.UDP())}},
		{succeeded: []enode.ID{n.ID()}},
		{}, // Advance the simulated clock past the existing 35-second dial throttle.
		{peersRemoved: []enode.ID{n.ID()},
			wantNewDials: []*enode.Node{enode.NewV4(n.Pubkey(), net.ParseIP("2001:db8::2"), n.TCP(), n.UDP())}},
	})
}

func TestServerDNSBootstrapWithoutDiscoveryPeers(t *testing.T) {
	n := dnsTestNode(t)
	db, err := enode.OpenDB("")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dialer := newDialTestDialer()
	srv := &Server{Config: Config{MaxPeers: 10, BootstrapNodes: []*enode.Node{n}, Dialer: dialer},
		localnode: enode.NewLocalNode(db, newkey()), discmix: enode.NewFairMix(0)}
	srv.setupDialScheduler()
	defer srv.dialsched.stop()
	select {
	case request := <-dialer.init:
		if request.n.ID() != n.ID() || !request.n.IP().IsLoopback() {
			t.Fatalf("unexpected DNS bootstrap destination: %s", request.n)
		}
	case <-time.After(time.Second):
		t.Fatal("DNS bootstrap was not dialed without discovery peers")
	}
}
