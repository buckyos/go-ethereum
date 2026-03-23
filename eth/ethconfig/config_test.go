package ethconfig

import (
	"testing"

	"github.com/ethereum/go-ethereum/consensus/beacon"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/params"
)

func newTestNode(t *testing.T) *node.Node {
	t.Helper()

	stack, err := node.New(&node.Config{Name: "geth-test"})
	if err != nil {
		t.Fatalf("failed to create test node: %v", err)
	}
	t.Cleanup(func() {
		_ = stack.Close()
	})
	return stack
}

func TestCreateConsensusEngineSkipsBeaconWrapperWithoutMerge(t *testing.T) {
	stack := newTestNode(t)
	engine := CreateConsensusEngine(stack, params.USDBChainConfig, &Defaults.Ethash, nil, false, rawdb.NewMemoryDatabase())

	if _, ok := engine.(*ethash.Ethash); !ok {
		t.Fatalf("expected pure ethash engine for usdb, got %T", engine)
	}
}

func TestCreateConsensusEngineUsesBeaconWrapperWithMerge(t *testing.T) {
	stack := newTestNode(t)
	engine := CreateConsensusEngine(stack, params.SepoliaChainConfig, &Defaults.Ethash, nil, false, rawdb.NewMemoryDatabase())

	if _, ok := engine.(*beacon.Beacon); !ok {
		t.Fatalf("expected beacon wrapper for merge-aware chain, got %T", engine)
	}
}
