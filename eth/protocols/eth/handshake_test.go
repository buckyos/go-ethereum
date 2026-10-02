// Copyright 2020 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package eth

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/forkid"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
)

// Tests that handshake failures are detected and reported correctly.
func TestHandshake66(t *testing.T) { testHandshake(t, ETH66) }

func testHandshake(t *testing.T, protocol uint) {
	t.Parallel()

	// Create a test backend only to have some valid genesis chain
	backend := newTestBackend(3)
	defer backend.close()

	var (
		genesis = backend.chain.Genesis()
		head    = backend.chain.CurrentBlock()
		td      = backend.chain.GetTd(head.Hash(), head.NumberU64())
		forkID  = forkid.NewID(backend.chain.Config(), backend.chain.Genesis().Hash(), backend.chain.CurrentHeader().Number.Uint64())
	)
	tests := []struct {
		code uint64
		data interface{}
		want error
	}{
		{
			code: TransactionsMsg, data: []interface{}{},
			want: errNoStatusMsg,
		},
		{
			code: StatusMsg, data: StatusPacket{10, 1, td, head.Hash(), genesis.Hash(), forkID},
			want: errProtocolVersionMismatch,
		},
		{
			code: StatusMsg, data: StatusPacket{uint32(protocol), 999, td, head.Hash(), genesis.Hash(), forkID},
			want: errNetworkIDMismatch,
		},
		{
			code: StatusMsg, data: StatusPacket{uint32(protocol), 1, td, head.Hash(), common.Hash{3}, forkID},
			want: errGenesisMismatch,
		},
		{
			code: StatusMsg, data: StatusPacket{uint32(protocol), 1, td, head.Hash(), genesis.Hash(), forkid.ID{Hash: [4]byte{0x00, 0x01, 0x02, 0x03}}},
			want: errForkIDRejected,
		},
	}
	for i, test := range tests {
		// Create the two peers to shake with each other
		app, net := p2p.MsgPipe()
		defer app.Close()
		defer net.Close()

		peer := NewPeer(protocol, p2p.NewPeer(enode.ID{}, "peer", nil), net, nil)
		defer peer.Close()

		// Send the junk test with one peer, check the handshake failure
		go p2p.Send(app, test.code, test.data)

		err := peer.Handshake(1, td, head.Hash(), genesis.Hash(), forkID, forkid.NewFilter(backend.chain))
		if err == nil {
			t.Errorf("test %d: protocol returned nil error, want %q", i, test.want)
		} else if !errors.Is(err, test.want) {
			t.Errorf("test %d: wrong error: got %q, want %q", i, err, test.want)
		}
	}
}

// statusForkChain supplies immutable fork-filter inputs without generating a
// consensus chain: this test covers the wire status handshake, not block import.
type statusForkChain struct {
	config  *params.ChainConfig
	genesis *types.Block
	head    *types.Header
}

func (c *statusForkChain) Config() *params.ChainConfig  { return c.config }
func (c *statusForkChain) Genesis() *types.Block        { return c.genesis }
func (c *statusForkChain) CurrentHeader() *types.Header { return c.head }

func TestHandshakeUSDBActivation66(t *testing.T) {
	const height = uint64(100)
	const network = uint64(202608250)
	genesis := types.NewBlockWithHeader(&types.Header{Number: new(big.Int)})
	oldConfig := &params.ChainConfig{}
	updatedConfig := &params.ChainConfig{USDB: &params.USDBConsensusConfig{
		Activations: []params.USDBConsensusActivation{{Block: 0}, {Block: height}},
	}}
	laterConfig := &params.ChainConfig{USDB: &params.USDBConsensusConfig{
		Activations: []params.USDBConsensusActivation{{Block: 0}, {Block: height + 100}},
	}}
	tests := []struct {
		name                          string
		localConfig, remoteConfig     *params.ChainConfig
		localHeight, remoteHeight     uint64
		localRejected, remoteRejected bool
	}{
		{name: "old and updated before activation", localConfig: updatedConfig, remoteConfig: oldConfig, localHeight: height - 1, remoteHeight: height - 1},
		{name: "updated rejects old at activation", localConfig: updatedConfig, remoteConfig: oldConfig, localHeight: height, remoteHeight: height - 1, localRejected: true, remoteRejected: true},
		{name: "upgraded lagging remote can catch up", localConfig: updatedConfig, remoteConfig: updatedConfig, localHeight: height, remoteHeight: height - 1},
		{name: "upgraded lagging local can catch up", localConfig: updatedConfig, remoteConfig: updatedConfig, localHeight: height - 1, remoteHeight: height},
		{name: "old local passed unrecognized remote fork", localConfig: oldConfig, remoteConfig: updatedConfig, localHeight: height, remoteHeight: height - 1, localRejected: true},
		{name: "different future checkpoints remain compatible", localConfig: updatedConfig, remoteConfig: laterConfig, localHeight: height - 1, remoteHeight: height - 1},
		{name: "different checkpoints split at first activation", localConfig: updatedConfig, remoteConfig: laterConfig, localHeight: height, remoteHeight: height - 1, localRejected: true, remoteRejected: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			localChain := &statusForkChain{config: test.localConfig, genesis: genesis, head: &types.Header{Number: new(big.Int).SetUint64(test.localHeight)}}
			remoteChain := &statusForkChain{config: test.remoteConfig, genesis: genesis, head: &types.Header{Number: new(big.Int).SetUint64(test.remoteHeight)}}
			localWire, remoteWire := p2p.MsgPipe()
			defer localWire.Close()
			defer remoteWire.Close()
			localPeer := NewPeer(ETH66, p2p.NewPeer(enode.ID{1}, "local", nil), localWire, nil)
			remotePeer := NewPeer(ETH66, p2p.NewPeer(enode.ID{2}, "remote", nil), remoteWire, nil)
			defer localPeer.Close()
			defer remotePeer.Close()
			localResult, remoteResult := make(chan error, 1), make(chan error, 1)
			go func() {
				localResult <- localPeer.Handshake(network, big.NewInt(1), localChain.head.Hash(), genesis.Hash(), forkid.NewIDWithChain(localChain), forkid.NewFilter(localChain))
			}()
			go func() {
				remoteResult <- remotePeer.Handshake(network, big.NewInt(1), remoteChain.head.Hash(), genesis.Hash(), forkid.NewIDWithChain(remoteChain), forkid.NewFilter(remoteChain))
			}()
			for _, result := range []struct {
				name         string
				err          error
				wantRejected bool
			}{
				{name: "local", err: <-localResult, wantRejected: test.localRejected},
				{name: "remote", err: <-remoteResult, wantRejected: test.remoteRejected},
			} {
				if result.wantRejected {
					if !errors.Is(result.err, errForkIDRejected) {
						t.Fatalf("%s handshake error = %v, want fork ID rejection", result.name, result.err)
					}
				} else if result.err != nil {
					t.Fatalf("%s handshake unexpectedly rejected compatible peer: %v", result.name, result.err)
				}
			}
		})
	}
}
