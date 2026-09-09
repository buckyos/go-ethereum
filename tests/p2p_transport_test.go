package tests

import (
	"net"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/nat"
)

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
				key, err := crypto.GenerateKey()
				if err != nil {
					t.Fatal(err)
				}
				advertise := tc.contact
				if seeds == nil {
					advertise = tc.seedAdvertise
				}
				srv := &p2p.Server{Config: p2p.Config{
					PrivateKey: key, MaxPeers: 10, ListenAddr: tc.listen,
					BootstrapNodes: seeds, NAT: nat.ExtIP(net.ParseIP(advertise)),
				}}
				if err := srv.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(srv.Stop)
				return srv
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
