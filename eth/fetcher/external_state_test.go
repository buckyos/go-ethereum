package fetcher

import (
	"errors"
	"fmt"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestExternalStateRetryImportsDescendants(t *testing.T) {
	for _, phase := range []string{"header", "execution", "invalid-after-wait"} {
		t.Run(phase, func(t *testing.T) {
			hashes, blocks := makeChain(3, 0, genesis)
			tester := newTester(false)
			defer tester.fetcher.Stop()
			var ready int32
			attempted := make(chan struct{}, 8)
			imported := make(chan *types.Block, 4)
			dropped := make(chan string, 4)
			fail := func() error {
				if atomic.LoadInt32(&ready) == 0 {
					select {
					case attempted <- struct{}{}:
					default:
					}
					return consensus.ErrExternalStateUnavailable
				}
				if phase == "invalid-after-wait" {
					return errors.New("invalid difficulty")
				}
				return nil
			}
			tester.fetcher.verifyHeader = func(*types.Header) error {
				if phase != "execution" {
					return fail()
				}
				return nil
			}
			tester.fetcher.insertChain = func(bs types.Blocks) (int, error) {
				if phase == "execution" {
					if err := fail(); err != nil {
						return 0, err
					}
				}
				return tester.insertChain(bs)
			}
			tester.fetcher.importedHook = func(_ *types.Header, b *types.Block) { imported <- b }
			tester.fetcher.dropPeer = func(peer string) { dropped <- peer }
			// Children arrive before their parent; duplicate gossip must not duplicate work.
			for _, i := range []int{0, 1, 2, 2} {
				if err := tester.fetcher.Enqueue("honest", blocks[hashes[i]]); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-attempted:
			case <-time.After(time.Second):
				t.Fatal("missing initial attempt")
			}
			if tester.chainHeight() != 0 {
				t.Fatal("unverified block imported")
			}
			select {
			case p := <-dropped:
				t.Fatalf("dropped waiting peer %s", p)
			default:
			}
			atomic.StoreInt32(&ready, 1)
			if phase == "invalid-after-wait" {
				select {
				case <-dropped:
				case <-time.After(5 * time.Second):
					t.Fatal("invalid retry not rejected")
				}
				if tester.chainHeight() != 0 {
					t.Fatal("invalid retry imported")
				}
				return
			}
			for want := uint64(1); want <= 3; want++ {
				select {
				case b := <-imported:
					if b.NumberU64() != want {
						t.Fatalf("import order: %d want %d", b.NumberU64(), want)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("no automatic import of %d", want)
				}
			}
			select {
			case p := <-dropped:
				t.Fatalf("dropped recovered peer %s", p)
			default:
			}
		})
	}
}

func TestExternalStateBlockedDoesNotDropPeer(t *testing.T) {
	for _, light := range []bool{false, true} {
		for _, phase := range []string{"header", "import"} {
			t.Run(fmt.Sprintf("light=%v/%s", light, phase), func(t *testing.T) {
				hashes, blocks := makeChain(1, 0, genesis)
				block := blocks[hashes[0]]
				dropped := make(chan string, 1)
				f := NewBlockFetcher(light,
					func(common.Hash) *types.Header { return genesis.Header() },
					func(common.Hash) *types.Block { return genesis },
					func(*types.Header) error {
						if phase == "header" {
							return consensus.ErrExternalStateBlocked
						}
						return nil
					}, func(*types.Block, bool) {}, func() uint64 { return 0 },
					func([]*types.Header) (int, error) { return 0, consensus.ErrExternalStateBlocked },
					func(types.Blocks) (int, error) { return 0, consensus.ErrExternalStateBlocked },
					func(peer string) { dropped <- peer })
				defer f.Stop()
				f.importedHook = func(*types.Header, *types.Block) { t.Error("unverifiable block imported") }
				if light {
					f.importHeaders("honest", block.Header())
				} else {
					f.importBlocks("honest", block)
				}
				// Wait for the importer to finish before checking peer handling.
				select {
				case result := <-f.done:
					if !errors.Is(result.err, consensus.ErrExternalStateBlocked) {
						t.Fatalf("local failure lost: %v", result.err)
					}
				case <-time.After(time.Second):
					t.Fatal("import did not finish")
				}
				select {
				case peer := <-dropped:
					t.Fatalf("local failure dropped peer %s", peer)
				default:
				}
			})
		}
	}
}

func TestExternalStateQueueLimits(t *testing.T) {
	f := NewBlockFetcher(false, nil, func(common.Hash) *types.Block { return nil }, nil, nil, func() uint64 { return 0 }, nil, nil, nil)
	makeBlock := func(n int) *types.Block {
		return types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1), Extra: []byte{byte(n), byte(n >> 8)}})
	}
	for i := 0; i < maxQueuedBlocks+1; i++ {
		f.enqueue(string(rune('a'+i/blockLimit)), nil, makeBlock(i))
	}
	if len(f.queued) != maxQueuedBlocks {
		t.Fatalf("global count %d", len(f.queued))
	}
	before := f.queueBytes
	f.enqueue("duplicate", nil, makeBlock(0))
	if f.queueBytes != before || f.queues["duplicate"] != 0 {
		t.Fatal("duplicate charged twice")
	}
	for hash := range f.queued {
		f.forgetBlock(hash)
	}
	if f.queueBytes != 0 || len(f.peerBytes) != 0 || len(f.queues) != 0 {
		t.Fatal("quota leaked")
	}
	f.queueBytes = maxQueuedBytes
	f.enqueue("global-bytes", nil, makeBlock(0))
	f.queueBytes = 0
	f.peerBytes["peer-bytes"] = maxPeerQueuedBytes
	f.enqueue("peer-bytes", nil, makeBlock(1))
	f.enqueue("distant", nil, types.NewBlockWithHeader(&types.Header{Number: big.NewInt(maxQueueDist + 1)}))
	if len(f.queued) != 0 {
		t.Fatal("byte or distance limit not enforced")
	}
}

func TestExternalStateCompletionDoesNotBlockAfterStop(t *testing.T) {
	f := NewBlockFetcher(false, nil, nil, nil, nil, nil, nil, nil, nil)
	f.Stop()
	done := make(chan struct{})
	go func() { f.finishImport(common.Hash{}, consensus.ErrExternalStateUnavailable); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("import completion stuck after shutdown")
	}
}

func TestExternalStateQueueExpiry(t *testing.T) {
	block := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1)})
	f := NewBlockFetcher(false, nil, func(common.Hash) *types.Block { return nil }, func(*types.Header) error { t.Error("expired block reached validation"); return nil }, nil, func() uint64 { return 0 }, nil, nil, nil)
	f.enqueue("peer", nil, block)
	f.queued[block.Hash()].firstSeen = time.Now().Add(-externalStateQueueTTL - time.Second)
	popped := make(chan struct{}, 1)
	f.queueChangeHook = func(_ common.Hash, added bool) {
		if !added {
			popped <- struct{}{}
		}
	}
	stopped := make(chan struct{})
	go func() { f.loop(); close(stopped) }()
	select {
	case <-popped:
	case <-time.After(time.Second):
		t.Fatal("expiry not examined")
	}
	f.Stop()
	<-stopped
	if len(f.queued) != 0 || f.queueBytes != 0 || len(f.peerBytes) != 0 {
		t.Fatal("expired block retained memory or quota")
	}
}

func TestExternalStateConcurrentImportLimit(t *testing.T) {
	entered := make(chan struct{}, maxQueuedBlocks)
	release := make(chan struct{})
	f := NewBlockFetcher(false, nil, func(hash common.Hash) *types.Block {
		if hash == genesis.Hash() {
			return genesis
		}
		return nil
	}, func(*types.Header) error {
		entered <- struct{}{}
		<-release
		return consensus.ErrExternalStateUnavailable
	}, nil, func() uint64 { return 0 }, nil, nil, nil)
	for i := 0; i < 16; i++ {
		f.enqueue("peer", nil, types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1), ParentHash: genesis.Hash(), Extra: []byte{byte(i)}}))
	}
	stopped := make(chan struct{})
	go func() { f.loop(); close(stopped) }()
	defer func() { f.Stop(); close(release); <-stopped }()
	for i := 0; i < maxConcurrentImports; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("imports not scheduled")
		}
	}
	// A loop round trip establishes that no further imports can start while the
	// original four are blocked on the external query.
	f.FilterHeaders("peer", nil, time.Now())
	select {
	case <-entered:
		t.Fatal("concurrent import limit exceeded")
	default:
	}
}

func TestExternalStateLightHeaderRetry(t *testing.T) {
	for _, phase := range []string{"header", "import"} {
		t.Run(phase, func(t *testing.T) {
			hashes, blocks := makeChain(1, 0, genesis)
			header := blocks[hashes[0]].Header()
			var ready, imported int32
			attempted := make(chan struct{}, 2)
			completed := make(chan struct{}, 1)
			dropped := make(chan string, 1)
			query := func() error {
				if atomic.LoadInt32(&ready) == 0 {
					select {
					case attempted <- struct{}{}:
					default:
					}
					return consensus.ErrExternalStateUnavailable
				}
				return nil
			}
			f := NewBlockFetcher(true, func(hash common.Hash) *types.Header {
				if hash == genesis.Hash() {
					return genesis.Header()
				}
				if hash == header.Hash() && atomic.LoadInt32(&imported) != 0 {
					return header
				}
				return nil
			}, nil, func(*types.Header) error {
				if phase == "header" {
					return query()
				}
				return nil
			}, nil,
				func() uint64 { return uint64(atomic.LoadInt32(&imported)) },
				func([]*types.Header) (int, error) {
					if phase == "import" {
						if err := query(); err != nil {
							return 0, err
						}
					}
					atomic.StoreInt32(&imported, 1)
					return 0, nil
				}, nil,
				func(peer string) { dropped <- peer })
			f.importedHook = func(*types.Header, *types.Block) { completed <- struct{}{} }
			f.enqueue("honest", header, nil)
			f.Start()
			defer f.Stop()
			select {
			case <-attempted:
			case <-time.After(time.Second):
				t.Fatal("header query not attempted")
			}
			atomic.StoreInt32(&ready, 1)
			select {
			case <-completed:
			case <-time.After(5 * time.Second):
				t.Fatal("header not automatically retried")
			}
			select {
			case <-dropped:
				t.Fatal("local header failure dropped peer")
			default:
			}
		})
	}
}
