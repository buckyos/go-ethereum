// p2pprobe exercises the repository's actual discovery/RLPx implementation in
// isolated Docker networks. It starts no blockchain, miner or upstream services.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/nat"
)

func main() {
	mode := flag.String("mode", "serve", "serve, probe or http")
	keyFile := flag.String("key-file", "/state/nodekey", "isolated server identity")
	advertise := flag.String("advertise", "", "seed's advertised IP")
	target := flag.String("target", "", "seed enode or HTTP URL")
	family := flag.String("family", "", "optional HTTP address family: 4 or 6")
	flag.Parse()
	if err := run(*mode, *keyFile, *advertise, *target, *family); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(mode, keyFile, advertise, target, family string) error {
	if mode == "http" {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp"+family, address)
		}}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
		resp, err := client.Head(target)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("HTTP probe failed: %s", resp.Status)
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"status": resp.StatusCode, "family": family})
	}
	key, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	if mode == "serve" {
		key, err = crypto.LoadECDSA(keyFile)
		if os.IsNotExist(err) {
			key, err = crypto.GenerateKey()
			if err == nil {
				err = crypto.SaveECDSA(keyFile, key)
			}
		}
		if err != nil {
			return err
		}
		srv := &p2p.Server{Config: p2p.Config{PrivateKey: key, MaxPeers: 10, ListenAddr: ":31303", NAT: nat.ExtIP(net.ParseIP(advertise))}}
		if err := srv.Start(); err != nil {
			return err
		}
		defer srv.Stop()
		// Only the fixture's loopback-published HTTP port exposes this metadata.
		server := &http.Server{Addr: ":8545", ReadHeaderTimeout: 3 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"node": srv.NodeInfo(), "peers": srv.PeersInfo()})
		})}
		return server.ListenAndServe()
	}
	if mode != "probe" {
		return fmt.Errorf("unknown mode %q", mode)
	}
	seed, err := enode.ParseV4(target)
	if err != nil {
		return err
	}
	af := "6"
	if seed.IP().To4() != nil {
		af = "4"
	}
	conn, err := net.ListenUDP("udp"+af, &net.UDPAddr{})
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
	// Explicitly require the signed discovery pong; a TCP peer alone does not
	// prove UDP publication because the seed could have been dialed directly.
	err = udp.Ping(seed)
	udp.Close()
	if err != nil {
		return fmt.Errorf("discovery UDP ping failed: %w", err)
	}
	srv := &p2p.Server{Config: p2p.Config{PrivateKey: key, MaxPeers: 10, ListenAddr: ":0", BootstrapNodes: []*enode.Node{seed}}}
	if err := srv.Start(); err != nil {
		return err
	}
	defer srv.Stop()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, peer := range srv.Peers() {
			if peer.ID() != seed.ID() {
				continue
			}
			remote := peer.RemoteAddr().(*net.TCPAddr)
			if !remote.IP.Equal(seed.IP()) || remote.Port != seed.TCP() {
				return fmt.Errorf("dialed %s instead of requested %s:%d", remote, seed.IP(), seed.TCP())
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
				"udp_ping_verified": true, "remote": remote.String(), "node_id": peer.ID().String(), "family": af,
			})
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("discovery responded but RLPx connection did not complete: %s", target)
}
