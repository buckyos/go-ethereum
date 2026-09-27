package tests

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/nat"
)

func startP2PBootstrapServer(t *testing.T, listen, advertise string, seeds []*enode.Node) *p2p.Server {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	srv := &p2p.Server{Config: p2p.Config{
		PrivateKey: key, MaxPeers: 10, ListenAddr: listen,
		BootstrapNodes: seeds, NAT: nat.ExtIP(net.ParseIP(advertise)),
	}}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)
	return srv
}

// TestP2PTransportBootstrap uses real discovery UDP and RLPx TCP, with neither
// static peers nor admin_addPeer. It does not start a blockchain or a miner.
func TestP2PTransportBootstrap(t *testing.T) {
	for _, tc := range []struct{ name, listen, contact, seedAdvertise string }{
		{"ipv4", "127.0.0.1:0", "127.0.0.1", "127.0.0.1"},
		{"ipv6", "[::1]:0", "::1", "::1"},
		{"dual-v4", ":0", "127.0.0.1", "127.0.0.1"},
		{"dual-v6", ":0", "::1", "::1"},
		{"dual-v6-advertises-v4", ":0", "::1", "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := func(seeds []*enode.Node) *p2p.Server {
				advertise := tc.contact
				if seeds == nil {
					advertise = tc.seedAdvertise
				}
				return startP2PBootstrapServer(t, tc.listen, advertise, seeds)
			}
			seed := start(nil)
			seedNode := enode.NewV4(&seed.PrivateKey.PublicKey, net.ParseIP(tc.contact), seed.Self().TCP(), seed.Self().UDP())
			joiner := start([]*enode.Node{seedNode})
			deadline := time.Now().Add(25 * time.Second)
			for time.Now().Before(deadline) {
				for _, peer := range joiner.Peers() {
					if peer.ID() != seedNode.ID() {
						continue
					}
					address := peer.RemoteAddr().(*net.TCPAddr)
					if !address.IP.Equal(net.ParseIP(tc.contact)) {
						t.Fatalf("connected through %s, expected %s", address.IP, tc.contact)
					}
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
			t.Fatalf("%s bootstrap did not discover and connect to seed", tc.name)
		})
	}
}

// TestP2PDNSBootstrap checks actual server startup and authenticated connections
// with a dead domain in the seed list. The discovery-only case uses TCP port zero
// in the DNS source, requiring UDP discovery to learn the real TCP endpoint.
func TestP2PDNSBootstrap(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	badIP := enode.NewV4(&key.PublicKey, net.IPv4(127, 0, 0, 1), 31303, 31303)
	bad, err := enode.ParseForConfig(enode.ValidSchemes, strings.Replace(badIP.String(), "127.0.0.1", "offline.invalid", 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"all-offline", "literal", "dns-discovery"} {
		t.Run(mode, func(t *testing.T) {
			seeds := []*enode.Node{bad}
			var want *enode.Node
			if mode != "all-offline" {
				seed := startP2PBootstrapServer(t, ":0", "127.0.0.1", nil)
				want = enode.NewV4(&seed.PrivateKey.PublicKey, net.IPv4(127, 0, 0, 1), seed.Self().TCP(), seed.Self().UDP())
				contact := want
				if mode == "dns-discovery" {
					udpOnly := enode.NewV4(want.Pubkey(), want.IP(), 0, want.UDP())
					contact, err = enode.ParseForConfig(enode.ValidSchemes, strings.Replace(udpOnly.String(), "127.0.0.1", "localhost", 1))
					if err != nil {
						t.Fatal(err)
					}
				}
				seeds = append(seeds, contact)
			}
			joiner := startP2PBootstrapServer(t, ":0", "127.0.0.1", seeds)
			if mode == "all-offline" {
				if joiner.PeerCount() != 0 || joiner.BootstrapNodes[0].String() != bad.String() {
					t.Fatal("offline seed lost or incorrectly reported as connected")
				}
				return
			}
			deadline := time.Now().Add(25 * time.Second)
			for time.Now().Before(deadline) {
				for _, peer := range joiner.Peers() {
					if peer.ID() == want.ID() && peer.RemoteAddr().(*net.TCPAddr).IP.IsLoopback() {
						return
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			t.Fatal("dead DNS seed prevented healthy seed discovery and connection")
		})
	}
}
