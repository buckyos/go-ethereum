package eth

import (
	"context"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

func TestUSDBEconomicsRPCGenesisErrorsAndReorg(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	genesis := &core.Genesis{Config: params.AllEthashProtocolChanges, GasLimit: 30000000}
	g := genesis.MustCommit(db)
	engine := ethash.NewFaker()
	defer engine.Close()
	a, _ := core.GenerateChain(genesis.Config, g, engine, db, 2, func(i int, b *core.BlockGen) { b.SetExtra([]byte("branch-a")) })
	b, _ := core.GenerateChain(genesis.Config, g, engine, db, 3, func(i int, b *core.BlockGen) { b.SetExtra([]byte("branch-b")) })
	chain, err := core.NewBlockChain(db, nil, genesis.Config, engine, vm.Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()
	if _, err := chain.InsertChain(a); err != nil {
		t.Fatal(err)
	}
	api := NewEthereumAPI(&Ethereum{blockchain: chain, engine: engine})
	server := rpc.NewServer()
	defer server.Stop()
	if err := server.RegisterName("eth", api); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	defer client.Close()
	var genesisReport core.USDBBlockEconomics
	if err := client.Call(&genesisReport, "eth_getUSDBBlockEconomics", g.Hash()); err != nil {
		t.Fatal(err)
	}
	if genesisReport.Status != "genesis_not_applicable" || genesisReport.Amounts != nil {
		t.Fatal(genesisReport)
	}
	assertError := func(hash common.Hash, code int) {
		t.Helper()
		var report core.USDBBlockEconomics
		err := client.Call(&report, "eth_getUSDBBlockEconomics", hash)
		rpcErr, ok := err.(rpc.Error)
		if !ok || rpcErr.ErrorCode() != code {
			t.Fatalf("have %v want RPC %d", err, code)
		}
	}
	assertError(common.Hash{1}, -32060)
	assertError(a[0].Hash(), -32063) // Legacy rewards must not be labeled USDB v1.
	api.economics.active = 2
	assertError(a[0].Hash(), -32067)
	api.economics.active = 0
	// Seed a previously verified cache entry to exercise invalidation without
	// involving any Miner Pass fixture in this RPC/canonical-chain test.
	api.economics.values = map[common.Hash]*core.USDBBlockEconomics{a[0].Hash(): core.USDBEconomicsIdentity(a[0])}
	if _, err := chain.InsertChain(b); err != nil {
		t.Fatal(err)
	}
	assertError(a[0].Hash(), -32061)
	if _, err := api.GetUSDBBlockEconomics(context.Background(), b[0].Hash()); err == nil {
		t.Fatal("legacy branch unexpectedly qualified")
	}
	// Bound the interactive replay independently of whether there are transactions.
	h := b[0].Header()
	h.GasUsed = 60000001
	oversized := types.NewBlockWithHeader(h)
	rawdb.WriteBlock(db, oversized)
	rawdb.WriteCanonicalHash(db, oversized.Hash(), oversized.NumberU64())
	assertError(oversized.Hash(), -32066)
}
