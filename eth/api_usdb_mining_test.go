package eth

import (
	"testing"

	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/miner"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

func TestUSDBMiningWorkStatusRPCWithoutIndexer(t *testing.T) {
	stack, err := node.New(&node.Config{DataDir: t.TempDir(), P2P: p2p.Config{NoDial: true, NoDiscovery: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	config := ethconfig.Defaults
	config.Genesis = &core.Genesis{Config: params.AllEthashProtocolChanges, GasLimit: 30000000}
	config.Ethash.PowMode = ethash.ModeFake
	config.Ethash.DatasetDir = t.TempDir()
	gap := uint32(0)
	config.Miner.USDB.AnchorGap = &gap
	backend, err := New(stack, &config)
	if err != nil {
		t.Fatal(err)
	}
	if err := stack.Start(); err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer()
	defer server.Stop()
	if err := server.RegisterName("miner", NewMinerAPI(backend)); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	defer client.Close()
	var status miner.USDBWorkStatus
	if err := client.Call(&status, "miner_getUSDBWorkStatus"); err != nil {
		t.Fatal(err)
	}
	if status.SchemaVersion != "usdb-miner-work:v1" || status.ConfiguredGap != 0 || status.Mining || status.WorkCurrent || status.Work != nil || status.Attempt != nil {
		t.Fatalf("unexpected cold miner diagnostics: %+v", status)
	}
}
