package tests

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/internal/peercheck"
	"github.com/ethereum/go-ethereum/p2p/enode"
	fixture "github.com/ethereum/go-ethereum/tests/common/peercheck"
)

func TestPeerCheckNetworkLayers(t *testing.T) {
	for _, mode := range []string{"ipv4", "ipv6", "dns", "udp-blocked", "tcp-closed", "wrong-key", "wrong-network", "wrong-genesis", "wrong-fork"} {
		t.Run(mode, func(t *testing.T) {
			req := fixture.Request()
			remote := req
			if mode == "wrong-network" {
				remote.NetworkID++
			}
			if mode == "wrong-genesis" {
				remote.GenesisHash[0] ^= 0xff
			}
			ip := "127.0.0.1"
			if mode == "ipv6" {
				ip = "::1"
			}
			n := fixture.Start(t, remote, ip, mode == "wrong-fork")
			if mode == "udp-blocked" {
				drop, err := net.ListenUDP("udp4", &net.UDPAddr{IP: n.IP()})
				if err != nil {
					t.Fatal(err)
				}
				defer drop.Close()
				n = enode.NewV4(n.Pubkey(), n.IP(), n.TCP(), drop.LocalAddr().(*net.UDPAddr).Port)
			}
			if mode == "tcp-closed" {
				closed, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				port := closed.Addr().(*net.TCPAddr).Port
				closed.Close()
				n = enode.NewV4(n.Pubkey(), n.IP(), port, n.UDP())
			}
			if mode == "wrong-key" {
				key, _ := crypto.GenerateKey()
				n = enode.NewV4(&key.PublicKey, n.IP(), n.TCP(), n.UDP())
			}
			req.Enode = n.URLv4()
			if mode == "dns" {
				req.Enode = strings.Replace(req.Enode, "127.0.0.1", "localhost", 1)
			}
			report, err := peercheck.Run(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "ipv4" || mode == "ipv6" || mode == "dns" {
				if !report.Usable || report.State != "PASS" {
					t.Fatalf("healthy peer rejected: %+v", report)
				}
				return
			}
			if report.Usable || report.State == "PASS" || len(report.Endpoints) != 1 {
				t.Fatalf("false success: %+v", report)
			}
			e := report.Endpoints[0]
			switch mode {
			case "udp-blocked":
				if e.Discovery.State != "FAIL" || e.ETH.State != "PASS" {
					t.Fatalf("UDP failure hid TCP/eth success: %+v", e)
				}
			case "tcp-closed":
				if e.TCP.State != "FAIL" || e.Discovery.State != "PASS" {
					t.Fatalf("TCP failure hid signed UDP pong: %+v", e)
				}
			case "wrong-key":
				if e.Identity.State != "FAIL" || e.TCP.State != "PASS" {
					t.Fatalf("wrong identity accepted: %+v", e)
				}
			default:
				if e.ETH.State != "FAIL" || e.Identity.State != "PASS" {
					t.Fatalf("network mismatch not identified: %+v", e)
				}
			}
		})
	}
}

func TestPeerCheckCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	key, _ := crypto.GenerateKey()
	req := fixture.Request()
	req.Enode = enode.NewV4(&key.PublicKey, net.IPv4(127, 0, 0, 1), listener.Addr().(*net.TCPAddr).Port, 1).String()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	report, err := peercheck.Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if report.Usable || time.Since(start) > 2*time.Second {
		t.Fatalf("cancellation did not bound the probe: %+v", report)
	}
}
