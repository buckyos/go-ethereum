package downloader

import (
	"errors"
	"fmt"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/consensus"
)

func TestExternalStateRetryRecoveryAndCancellation(t *testing.T) {
	for _, mode := range []string{"recover", "invalid", "blocked", "cancel", "shutdown", "budget"} {
		t.Run(mode, func(t *testing.T) {
			d := &Downloader{quitCh: make(chan struct{}), cancelCh: make(chan struct{})}
			attempts := 0
			bad := errors.New("invalid state root")
			_, err := d.retryExternalStateFor("test batch 1..3", func() (int, error) {
				attempts++
				switch mode {
				case "recover":
					if attempts == 3 {
						return 0, nil
					}
				case "invalid":
					if attempts == 2 {
						return 1, bad
					}
				case "blocked":
					return 1, consensus.ErrExternalStateBlocked
				case "cancel":
					close(d.cancelCh)
				case "shutdown":
					close(d.quitCh)
				}
				return 1, consensus.ErrExternalStateUnavailable
			}, time.Millisecond, 20*time.Millisecond)
			switch mode {
			case "recover":
				if err != nil || attempts != 3 {
					t.Fatalf("attempts=%d err=%v", attempts, err)
				}
			case "invalid":
				if !errors.Is(err, bad) || attempts != 2 {
					t.Fatalf("attempts=%d err=%v", attempts, err)
				}
			case "blocked":
				if !errors.Is(err, consensus.ErrExternalStateBlocked) || attempts != 1 {
					t.Fatalf("attempts=%d err=%v", attempts, err)
				}
			case "cancel", "shutdown":
				if !errors.Is(err, errCanceled) || attempts != 1 {
					t.Fatalf("attempts=%d err=%v", attempts, err)
				}
			case "budget":
				if !errors.Is(err, consensus.ErrExternalStateUnavailable) || attempts < 2 {
					t.Fatalf("attempts=%d err=%v", attempts, err)
				}
			}
		})
	}
}

// externalStateTestChain exercises the production downloader entry points while
// preserving a real chain database, validation and partial-prefix imports.
type externalStateTestChain struct {
	BlockChain
	insertBlocks  func(types.Blocks) (int, error)
	insertHeaders func([]*types.Header, int) (int, error)
}

func (c *externalStateTestChain) InsertChain(bs types.Blocks) (int, error) { return c.insertBlocks(bs) }
func (c *externalStateTestChain) InsertHeaderChain(hs []*types.Header, freq int) (int, error) {
	return c.insertHeaders(hs, freq)
}

func TestExternalStateDownloaderPaths(t *testing.T) {
	for _, mode := range []SyncMode{FullSync, LightSync, SnapSync} {
		t.Run(mode.String(), func(t *testing.T) {
			tester := newTester(t)
			defer tester.terminate()
			chain := testChainBase.shorten(800 / 8)
			peer := tester.newPeer("honest", eth.ETH67, chain.blocks[1:])
			var attempts int32
			wrapped := &externalStateTestChain{BlockChain: tester.chain}
			wrapped.insertBlocks = func(bs types.Blocks) (int, error) {
				if mode == FullSync && atomic.AddInt32(&attempts, 1) == 1 {
					// One committed prefix survives the wait, and reimport is idempotent.
					if _, err := tester.chain.InsertChain(bs[:1]); err != nil {
						return 0, err
					}
					return 1, consensus.ErrExternalStateUnavailable
				}
				return tester.chain.InsertChain(bs)
			}
			wrapped.insertHeaders = func(hs []*types.Header, freq int) (int, error) {
				if mode != FullSync && atomic.AddInt32(&attempts, 1) == 1 {
					return 0, consensus.ErrExternalStateUnavailable
				}
				return tester.chain.InsertHeaderChain(hs, freq)
			}
			tester.downloader.blockchain = wrapped
			tester.downloader.lightchain = wrapped
			head := peer.chain.CurrentBlock()
			err := tester.downloader.LegacySync("honest", head.Hash(), peer.chain.GetTd(head.Hash(), head.NumberU64()), nil, mode)
			if err != nil {
				t.Fatalf("automatic retry failed: %v", err)
			}
			if atomic.LoadInt32(&attempts) < 2 {
				t.Fatal("missing retry")
			}
			if tester.downloader.peers.Peer("honest") == nil {
				t.Fatal("honest peer dropped")
			}
			if tester.chain.CurrentHeader().Hash() != head.Hash() {
				t.Fatal("target head not reached")
			}
		})
	}
}

func TestExternalStateDownloaderDoesNotBlamePeer(t *testing.T) {
	for _, local := range []bool{true, false} {
		t.Run(fmt.Sprint(local), func(t *testing.T) {
			tester := newTester(t)
			defer tester.terminate()
			chain := testChainBase.shorten(800 / 8)
			peer := tester.newPeer("source", eth.ETH67, chain.blocks[1:])
			wrapped := &externalStateTestChain{BlockChain: tester.chain}
			wrapped.insertBlocks = func(types.Blocks) (int, error) {
				if local {
					return 0, consensus.ErrExternalStateBlocked
				}
				return 0, errors.New("invalid merkle root")
			}
			tester.downloader.blockchain = wrapped
			head := peer.chain.CurrentBlock()
			err := tester.downloader.LegacySync("source", head.Hash(), peer.chain.GetTd(head.Hash(), head.NumberU64()), nil, FullSync)
			if local {
				if !errors.Is(err, consensus.ErrExternalStateBlocked) || tester.downloader.peers.Peer("source") == nil {
					t.Fatalf("local failure blamed peer: %v", err)
				}
			} else if !errors.Is(err, errInvalidChain) || tester.downloader.peers.Peer("source") != nil {
				t.Fatalf("invalid block did not reject peer: %v", err)
			}
		})
	}
}
