package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/beacon"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
)

func TestUSDBExternalStateImportRecovery(t *testing.T) {
	for _, failure := range []struct {
		name string
		code int
		kind error
	}{
		{"height", -32040, consensus.ErrExternalStateUnavailable},
		{"snapshot-backfill", -32041, consensus.ErrExternalStateUnavailable},
		{"malformed-profile", 0, consensus.ErrExternalStateBlocked},
	} {
		for _, beaconWrapper := range []bool{false, true} {
			for _, failureAt := range []int32{1, 2} {
				t.Run(fmt.Sprintf("%s/beacon=%v/failing-query=%d", failure.name, beaconWrapper, failureAt), func(t *testing.T) {
					recipient := common.HexToAddress("0x1111111111111111111111111111111111111111")
					genesis := newUSDBImportTestGenesis(t)
					profile := newUSDBImportTestProfile(t, recipient, "100000000")
					producerServer := newUSDBImportTestProfileServer(t, profile)
					defer producerServer.Close()
					producerDB := rawdb.NewMemoryDatabase()
					producer := newUSDBImportTestEngine(genesis.Config, producerServer.URL)
					defer producer.Close()
					blocks := generateUSDBImportTestBlocks(t, genesis.Config, genesis.MustCommit(producerDB), producer, producerDB, recipient, 1)
					var calls int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if atomic.AddInt32(&calls, 1) == failureAt {
							var request struct {
								ID json.RawMessage `json:"id"`
							}
							if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
								t.Error(err)
								return
							}
							response := map[string]interface{}{"jsonrpc": "2.0", "id": request.ID}
							if failure.code == 0 {
								// The envelope is valid; decoding the local result must not blame the block.
								response["result"] = map[string]interface{}{"external_state": map[string]interface{}{"btc_height": "wrong-type"}}
							} else {
								response["error"] = map[string]interface{}{"code": failure.code, "message": "test local indexer " + failure.name}
							}
							json.NewEncoder(w).Encode(response)
							return
						}
						producerServer.Config.Handler.ServeHTTP(w, r)
					}))
					defer server.Close()
					db := rawdb.NewMemoryDatabase()
					genesisBlock := genesis.MustCommit(db)
					engine := newUSDBImportTestEngine(genesis.Config, server.URL)
					defer engine.Close()
					var validator consensus.Engine = engine
					if beaconWrapper {
						validator = beacon.New(engine)
					}
					chain, err := NewBlockChain(db, nil, genesis.Config, validator, vm.Config{}, nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer chain.Stop()
					// Reject locally provable structural invalidity without consulting an
					// unavailable indexer, and preserve normal BAD BLOCK handling for it.
					badHeader := blocks[0].Header()
					badHeader.GasUsed = badHeader.GasLimit + 1
					badBlock := types.NewBlockWithHeader(badHeader)
					if _, err := chain.InsertChain(types.Blocks{badBlock}); err == nil || !strings.Contains(err.Error(), "invalid gasUsed") {
						t.Fatalf("invalid header hidden by external state: %v", err)
					}
					if atomic.LoadInt32(&calls) != 0 {
						t.Fatal("invalid header queried indexer")
					}
					if _, err = chain.InsertChain(blocks); !errors.Is(err, failure.kind) {
						t.Fatalf("missing local error, calls=%d: %v", calls, err)
					}
					if failure.code == 0 {
						var decodeError *json.UnmarshalTypeError
						if !errors.As(err, &decodeError) {
							t.Fatalf("decoder cause lost through consensus/import: %v", err)
						}
					}
					if chain.CurrentBlock().Hash() != genesisBlock.Hash() {
						t.Fatal("unverifiable block committed")
					}
					if bad := rawdb.ReadAllBadBlocks(db); len(bad) != 1 || bad[0].Hash() != badBlock.Hash() {
						t.Fatalf("local error recorded as bad block: %v", bad)
					}
					state, err := chain.State()
					if err != nil {
						t.Fatal(err)
					}
					if state.GetBalance(recipient).Sign() != 0 {
						t.Fatal("failed reward leaked into committed state")
					}
					// The normal import entry point must fully validate after dependency recovery.
					if _, err = chain.InsertChain(blocks); err != nil {
						t.Fatalf("reimport failed: %v", err)
					}
					if chain.CurrentBlock().Hash() != blocks[0].Hash() {
						t.Fatal("retry did not advance chain")
					}
					state, err = chain.State()
					if err != nil {
						t.Fatal(err)
					}
					if state.GetBalance(recipient).Sign() <= 0 {
						t.Fatal("retry lost block rewards")
					}
				})
			}
		}
	}
}
