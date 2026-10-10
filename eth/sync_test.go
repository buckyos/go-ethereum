// Copyright 2015 The go-ethereum Authors
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
	"fmt"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/forkid"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/downloader"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/eth/protocols/snap"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// The only new block has expired from the fetcher, while the connected peer's
// advertised parent is exactly our local head. No new gossip or reconnect may
// be needed for the downloader to retrieve and validate that fixed tip.
func TestExternalStateExpiredTipRecovery(t *testing.T) {
	for _, version := range []uint{eth.ETH66, eth.ETH67} {
		t.Run(fmt.Sprintf("eth%d", version), func(t *testing.T) {
			local := newTestHandlerWithBlocks(5)
			defer local.close()
			remote := newTestHandlerWithBlocks(5)
			defer remote.close()
			parent := remote.chain.CurrentBlock()
			if local.chain.CurrentBlock().Hash() != parent.Hash() {
				t.Fatal("fixture parents differ")
			}
			caps := []p2p.Cap{{Name: "eth", Version: version}}
			localPipe, remotePipe := p2p.MsgPipe()
			defer localPipe.Close()
			defer remotePipe.Close()
			peer := eth.NewPeer(version, p2p.NewPeer(enode.ID{1}, "", caps), localPipe, local.txpool)
			other := eth.NewPeer(version, p2p.NewPeer(enode.ID{2}, "", caps), remotePipe, remote.txpool)
			defer peer.Close()
			defer other.Close()
			ready := make(chan struct{}, 2)
			go local.handler.runEthPeer(peer, func(p *eth.Peer) error {
				ready <- struct{}{}
				return eth.Handle((*ethHandler)(local.handler), p)
			})
			go remote.handler.runEthPeer(other, func(p *eth.Peer) error {
				ready <- struct{}{}
				return eth.Handle((*ethHandler)(remote.handler), p)
			})
			for i := 0; i < 2; i++ {
				select {
				case <-ready:
				case <-time.After(time.Second):
					t.Fatal("peer handshake did not finish")
				}
			}
			blocks, _ := core.GenerateChain(remote.chain.Config(), parent, ethash.NewFaker(), remote.db, 1, nil)
			tip := blocks[0]
			if _, err := remote.chain.InsertChain(blocks); err != nil {
				t.Fatal(err)
			}
			// Direct insertion does not broadcast a mined-block event. The peer
			// still advertises the parent established during the handshake.
			if head, td := peer.Head(); head != parent.Hash() || td.Cmp(local.chain.GetTd(parent.Hash(), parent.NumberU64())) != 0 {
				t.Fatal("expected equal local and advertised parent TD")
			}
			probe := newChainSyncer(local.handler)
			probe.forced = true
			if probe.nextSyncOp() != nil {
				t.Fatal("fixture unexpectedly schedules sync without the expired tip hint")
			}
			heads := make(chan core.ChainHeadEvent, 8)
			sub := local.chain.SubscribeChainHeadEvent(heads)
			defer sub.Unsubscribe()
			local.handler.retryExpiredBlock(peer.ID(), tip.Header())
			deadline := time.NewTimer(15 * time.Second)
			defer deadline.Stop()
			for local.chain.CurrentBlock().Hash() != tip.Hash() {
				select {
				case <-heads:
				case <-deadline.C:
					t.Fatal("expired tip was not automatically downloaded")
				}
			}
			if connected := local.handler.peers.peer(peer.ID()); connected == nil || connected.Peer != peer {
				t.Fatal("recovery replaced or disconnected the peer")
			}
			// An older gossip callback cannot overwrite the recovery target.
			local.handler.retryExpiredBlock(peer.ID(), parent.Header())
			peer.UpdateHead(parent.Hash(), local.chain.GetTd(parent.Hash(), parent.NumberU64()))
			if head, _ := peer.Head(); head != tip.Hash() {
				t.Fatal("late parent hint rolled back the peer target")
			}
		})
	}
}

// Complete a real downloader sync to block 5 with block 6 already queued in the
// fetcher. The peer serves a fixed chain and never reannounces the queued tip.
func TestSyncCompletionImportsQueuedTip(t *testing.T) {
	for _, version := range []uint{eth.ETH66, eth.ETH67} {
		for _, mode := range []downloader.SyncMode{downloader.FullSync, downloader.SnapSync} {
			t.Run(fmt.Sprintf("eth%d/%s", version, mode), func(t *testing.T) {
				empty := newTestHandler()
				defer empty.close()
				if mode == downloader.FullSync {
					atomic.StoreUint32(&empty.handler.snapSync, 0)
				}
				full := newTestHandlerWithBlocks(5)
				defer full.close()
				parent := full.chain.CurrentBlock()
				blocks, _ := core.GenerateChain(full.chain.Config(), parent, ethash.NewFaker(), full.db, 1, nil)
				tip := blocks[0]

				caps := []p2p.Cap{{Name: "eth", Version: version}, {Name: "snap", Version: snap.SNAP1}}
				emptyPipe, fullPipe := p2p.MsgPipe()
				defer emptyPipe.Close()
				defer fullPipe.Close()
				emptyPeer := eth.NewPeer(version, p2p.NewPeer(enode.ID{1}, "", caps), emptyPipe, empty.txpool)
				fullPeer := eth.NewPeer(version, p2p.NewPeer(enode.ID{2}, "", caps), fullPipe, full.txpool)
				defer emptyPeer.Close()
				defer fullPeer.Close()
				ready := make(chan struct{}, 2)
				go empty.handler.runEthPeer(emptyPeer, func(peer *eth.Peer) error {
					ready <- struct{}{}
					return eth.Handle((*ethHandler)(empty.handler), peer)
				})
				go full.handler.runEthPeer(fullPeer, func(peer *eth.Peer) error {
					ready <- struct{}{}
					return eth.Handle((*ethHandler)(full.handler), peer)
				})
				emptySnapPipe, fullSnapPipe := p2p.MsgPipe()
				defer emptySnapPipe.Close()
				defer fullSnapPipe.Close()
				emptySnap := snap.NewPeer(snap.SNAP1, p2p.NewPeer(enode.ID{1}, "", caps), emptySnapPipe)
				fullSnap := snap.NewPeer(snap.SNAP1, p2p.NewPeer(enode.ID{2}, "", caps), fullSnapPipe)
				go empty.handler.runSnapExtension(emptySnap, func(peer *snap.Peer) error {
					return snap.Handle((*snapHandler)(empty.handler), peer)
				})
				go full.handler.runSnapExtension(fullSnap, func(peer *snap.Peer) error {
					return snap.Handle((*snapHandler)(full.handler), peer)
				})
				for i := 0; i < 2; i++ {
					select {
					case <-ready:
					case <-time.After(time.Second):
						t.Fatal("peer handshake did not finish")
					}
				}
				if mode == downloader.SnapSync {
					// Finish the genesis snapshot first: this tiny chain executes
					// entirely after the pivot, which otherwise races cancellation
					// of its initial state transfer against the block download.
					cancel := make(chan struct{})
					timer := time.AfterFunc(5*time.Second, func() { close(cancel) })
					err := empty.handler.downloader.SnapSyncer.Sync(empty.chain.Genesis().Root(), cancel)
					timer.Stop()
					if err != nil {
						t.Fatal("genesis snapshot transfer failed:", err)
					}
				}
				if err := empty.handler.blockFetcher.Enqueue(emptyPeer.ID(), tip); err != nil {
					t.Fatal(err)
				}
				// This round trip is a loop barrier before sync starts. Downloader
				// responses use their own dispatcher and do not wake the fetcher.
				empty.handler.blockFetcher.FilterHeaders(emptyPeer.ID(), nil, time.Now())
				if empty.chain.CurrentBlock().NumberU64() != 0 {
					t.Fatal("tip imported before its parents")
				}
				heads := make(chan core.ChainHeadEvent, 8)
				sub := empty.chain.SubscribeChainHeadEvent(heads)
				defer sub.Unsubscribe()
				op := peerToSyncOp(mode, emptyPeer)
				if op.head != parent.Hash() {
					t.Fatal("downloader target is not the queued tip's parent")
				}
				if err := empty.handler.doSync(op); err != nil {
					t.Fatal("sync failed:", err)
				}
				if atomic.LoadUint32(&empty.handler.snapSync) != 0 {
					t.Fatal("snap sync still enabled after completion")
				}
				deadline := time.NewTimer(2 * time.Second)
				defer deadline.Stop()
				for empty.chain.CurrentBlock().Hash() != tip.Hash() {
					select {
					case <-heads:
					case <-deadline.C:
						t.Fatalf("queued tip stalled after sync: have %d, want 6 without another announcement or reconnect",
							empty.chain.CurrentBlock().NumberU64())
					}
				}
				if peer := empty.handler.peers.peer(emptyPeer.ID()); peer == nil || peer.Peer != emptyPeer {
					t.Fatal("recovery replaced or disconnected the peer")
				}
			})
		}
	}
}

// Tests that snap sync is disabled after a successful sync cycle.
func TestSnapSyncDisabling66(t *testing.T) { testSnapSyncDisabling(t, eth.ETH66, snap.SNAP1) }
func TestSnapSyncDisabling67(t *testing.T) { testSnapSyncDisabling(t, eth.ETH67, snap.SNAP1) }

// Tests that snap sync gets disabled as soon as a real block is successfully
// imported into the blockchain.
func testSnapSyncDisabling(t *testing.T, ethVer uint, snapVer uint) {
	t.Parallel()

	// Create an empty handler and ensure it's in snap sync mode
	empty := newTestHandler()
	if atomic.LoadUint32(&empty.handler.snapSync) == 0 {
		t.Fatalf("snap sync disabled on pristine blockchain")
	}
	defer empty.close()

	// Create a full handler and ensure snap sync ends up disabled
	full := newTestHandlerWithBlocks(1024)
	if atomic.LoadUint32(&full.handler.snapSync) == 1 {
		t.Fatalf("snap sync not disabled on non-empty blockchain")
	}
	defer full.close()

	// Sync up the two handlers via both `eth` and `snap`
	caps := []p2p.Cap{{Name: "eth", Version: ethVer}, {Name: "snap", Version: snapVer}}

	emptyPipeEth, fullPipeEth := p2p.MsgPipe()
	defer emptyPipeEth.Close()
	defer fullPipeEth.Close()

	emptyPeerEth := eth.NewPeer(ethVer, p2p.NewPeer(enode.ID{1}, "", caps), emptyPipeEth, empty.txpool)
	fullPeerEth := eth.NewPeer(ethVer, p2p.NewPeer(enode.ID{2}, "", caps), fullPipeEth, full.txpool)
	defer emptyPeerEth.Close()
	defer fullPeerEth.Close()

	go empty.handler.runEthPeer(emptyPeerEth, func(peer *eth.Peer) error {
		return eth.Handle((*ethHandler)(empty.handler), peer)
	})
	go full.handler.runEthPeer(fullPeerEth, func(peer *eth.Peer) error {
		return eth.Handle((*ethHandler)(full.handler), peer)
	})

	emptyPipeSnap, fullPipeSnap := p2p.MsgPipe()
	defer emptyPipeSnap.Close()
	defer fullPipeSnap.Close()

	emptyPeerSnap := snap.NewPeer(snapVer, p2p.NewPeer(enode.ID{1}, "", caps), emptyPipeSnap)
	fullPeerSnap := snap.NewPeer(snapVer, p2p.NewPeer(enode.ID{2}, "", caps), fullPipeSnap)

	go empty.handler.runSnapExtension(emptyPeerSnap, func(peer *snap.Peer) error {
		return snap.Handle((*snapHandler)(empty.handler), peer)
	})
	go full.handler.runSnapExtension(fullPeerSnap, func(peer *snap.Peer) error {
		return snap.Handle((*snapHandler)(full.handler), peer)
	})
	// Wait a bit for the above handlers to start
	time.Sleep(250 * time.Millisecond)

	// Check that snap sync was disabled
	op := peerToSyncOp(downloader.SnapSync, empty.handler.peers.peerWithHighestTD())
	if err := empty.handler.doSync(op); err != nil {
		t.Fatal("sync failed:", err)
	}
	if atomic.LoadUint32(&empty.handler.snapSync) == 1 {
		t.Fatalf("snap sync not disabled after successful synchronisation")
	}
}

// A busy peer set must not bypass the local dependency cooldown or permanent
// operator block. A nil handler deliberately catches accidental scheduling.
func TestExternalStateSyncScheduling(t *testing.T) {
	temporary := &chainSyncer{}
	temporary.deferExternalState(consensus.ErrExternalStateUnavailable)
	if temporary.externalStateBlocked || !time.Now().Before(temporary.externalStateRetryAt) || temporary.nextSyncOp() != nil {
		t.Fatal("temporary failure did not defer sync")
	}
	permanent := &chainSyncer{}
	permanent.deferExternalState(consensus.ErrExternalStateBlocked)
	if !permanent.externalStateBlocked || permanent.nextSyncOp() != nil {
		t.Fatal("permanent local failure still schedules downloads")
	}
}

func TestExternalStateExpiredTipHints(t *testing.T) {
	local := newTestHandlerWithBlocks(1)
	defer local.close()
	// Keep scheduling idle so the hint boundary can be checked independently
	// from the actual network download covered above.
	h := &handler{chain: local.chain, peers: newPeerSet(), quitSync: make(chan struct{})}
	h.chainSync = newChainSyncer(h)
	pipe, other := p2p.MsgPipe()
	defer pipe.Close()
	defer other.Close()
	p := eth.NewPeer(eth.ETH67, p2p.NewPeer(enode.ID{1}, "", nil), pipe, local.txpool)
	defer p.Close()
	remote := eth.NewPeer(eth.ETH67, p2p.NewPeer(enode.ID{2}, "", nil), other, local.txpool)
	defer remote.Close()
	parent := local.chain.CurrentBlock()
	parentTD := local.chain.GetTd(parent.Hash(), parent.NumberU64())
	errs := make(chan error, 2)
	for _, peer := range []*eth.Peer{p, remote} {
		go func(peer *eth.Peer) {
			errs <- peer.Handshake(1, parentTD, parent.Hash(), local.chain.Genesis().Hash(),
				forkid.NewID(local.chain.Config(), local.chain.Genesis().Hash(), parent.NumberU64()), local.handler.forkFilter)
		}(peer)
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if err := h.peers.registerPeer(p, nil); err != nil {
		t.Fatal(err)
	}
	unknownParent := common.HexToHash("0x1234")
	header := &types.Header{Number: big.NewInt(3), ParentHash: unknownParent, Difficulty: big.NewInt(100)}
	h.retryExpiredBlock(p.ID(), header)
	if head, _ := p.Head(); head != parent.Hash() || len(h.chainSync.peerEventCh) != 0 {
		t.Fatal("guessed TD for a parent unknown to both the chain and the peer")
	}
	// A peer can advertise a backlog parent that has not arrived locally yet.
	p.SetHead(unknownParent, parentTD)
	h.retryExpiredBlock(p.ID(), header)
	wantTD := new(big.Int).Add(parentTD, header.Difficulty)
	if head, td := p.Head(); head != header.Hash() || td.Cmp(wantTD) != 0 {
		t.Fatal("lost the expired backlog tip")
	}
	if local.chain.CurrentBlock().Hash() != parent.Hash() {
		t.Fatal("hint bypassed block validation")
	}
	// Coalesced wakeups must remain bounded and cannot block the fetcher even
	// if the chain sync loop is busy or permanently blocked by local state.
	h.chainSync.externalStateBlocked = true
	for i := 0; i < 100; i++ {
		if !h.chainSync.handlePeerEvent(p) {
			t.Fatal("unexpected shutdown")
		}
	}
	if len(h.chainSync.peerEventCh) != 1 || h.chainSync.nextSyncOp() != nil {
		t.Fatal("wakeups bypassed permanent block or grew without bound")
	}
	if err := h.peers.unregisterPeer(p.ID()); err != nil {
		t.Fatal(err)
	}
	h.retryExpiredBlock(p.ID(), header) // Disconnected peers leave no retry task behind.
	close(h.quitSync)
	if h.chainSync.handlePeerEvent(p) {
		t.Fatal("accepted a wakeup after shutdown")
	}
}
