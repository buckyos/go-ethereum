// Package peercheck provides isolated peers for endpoint diagnostic tests.
package peercheck

import (
	"math/big"
	"net"
	"testing"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/forkid"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	probe "github.com/ethereum/go-ethereum/internal/peercheck"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/nat"
	"github.com/ethereum/go-ethereum/params"
)

// Request returns a small in-memory release with no persistent data or accounts.
func Request() probe.Request {
	genesis := &core.Genesis{Config: params.AllEthashProtocolChanges, Difficulty: big.NewInt(1), GasLimit: 30000000, Alloc: core.GenesisAlloc{}}
	return probe.Request{NetworkID: 202608250, ChainID: genesis.Config.ChainID.Uint64(),
		Genesis: genesis, GenesisHash: genesis.ToBlock().Hash(), TimeoutSecs: 5}
}

// Start serves real discovery/RLPx and eth status without a blockchain or miner.
func Start(t testing.TB, req probe.Request, ip string, badFork bool) *enode.Node {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	protocol := p2p.Protocol{Name: "eth", Version: eth.ETH67, Length: 17,
		Run: func(p *p2p.Peer, rw p2p.MsgReadWriter) error {
			peer := eth.NewPeer(eth.ETH67, p, rw, nil)
			defer peer.Close()
			id := forkid.NewID(req.Genesis.Config, req.GenesisHash, 0)
			if badFork {
				id.Hash = [4]byte{0xff, 0xff, 0xff, 0xff}
			}
			if err := peer.Handshake(req.NetworkID, req.Genesis.Difficulty, req.GenesisHash, req.GenesisHash,
				id, func(forkid.ID) error { return nil }); err != nil {
				return err
			}
			_, err := rw.ReadMsg()
			return err
		}}
	srv := &p2p.Server{Config: p2p.Config{PrivateKey: key, MaxPeers: 16, NoDial: true,
		ListenAddr: net.JoinHostPort(ip, "0"), NAT: nat.ExtIP(net.ParseIP(ip)), Protocols: []p2p.Protocol{protocol}}}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)
	return enode.NewV4(&key.PublicKey, net.ParseIP(ip), srv.Self().TCP(), srv.Self().UDP())
}
