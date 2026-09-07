// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
// Licensed under the GNU Lesser General Public License, version 3 or later.

package usdbacceptance

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type evidenceRPC struct {
	genesis, checkpoint *types.Header
	observed            []*big.Int
	fault               string
	headers             int
}

func (r *evidenceRPC) HeaderByNumber(ctx context.Context, n *big.Int) (*types.Header, error) {
	if n.Sign() == 0 {
		return r.genesis, nil
	}
	r.headers++
	if r.fault == "reorg" && r.headers > 1 {
		replacement := *r.checkpoint
		replacement.Extra = []byte{1}
		return &replacement, nil
	}
	return r.checkpoint, nil
}
func (r *evidenceRPC) CodeAt(ctx context.Context, a common.Address, n *big.Int) ([]byte, error) {
	r.observed = append(r.observed, n)
	if r.fault == "code" {
		return []byte{0x60}, nil
	}
	if r.fault == "pruned" {
		return nil, errors.New("missing trie node")
	}
	return []byte{0}, nil
}
func (r *evidenceRPC) StorageAt(ctx context.Context, a common.Address, s common.Hash, n *big.Int) ([]byte, error) {
	r.observed = append(r.observed, n)
	if r.fault == "storage" {
		return []byte{1}, nil
	}
	return make([]byte, 32), nil
}
func (r *evidenceRPC) CallContract(ctx context.Context, m ethereum.CallMsg, n *big.Int) ([]byte, error) {
	r.observed = append(r.observed, n)
	if r.fault == "call" {
		return []byte{2}, nil
	}
	return []byte{1}, nil
}
func TestObserveValidationEvidencePinsReadsAndRejectsTampering(t *testing.T) {
	for _, fault := range []string{"", "code", "storage", "call", "pruned", "reorg"} {
		t.Run(fault, func(t *testing.T) {
			rpc := &evidenceRPC{genesis: &types.Header{Number: big.NewInt(0)}, checkpoint: &types.Header{Number: big.NewInt(15), Root: common.HexToHash("0x1234")}, fault: fault}
			a := common.HexToAddress(testDAO)
			e := ValidationEvidence{SchemaVersion: "sourcedao-bootstrap-validation:v2", Checkpoint: BlockIdentity{Number: 15, Hash: rpc.checkpoint.Hash(), StateRoot: rpc.checkpoint.Root}, GenesisHash: rpc.genesis.Hash(), ConfigSHA256: strings.Repeat("a", 64), GoldenSHA256: strings.Repeat("b", 64),
				Code: []CodeObservation{{Address: a, Keccak256: crypto.Keccak256Hash([]byte{0})}}, Storage: []StorageObservation{{Address: a}}, Calls: []CallObservation{{To: a, Data: []byte{1, 2, 3, 4}, Result: []byte{1}}}}
			digest, err := ObserveValidationEvidence(context.Background(), rpc, e)
			if fault != "" {
				if err == nil {
					t.Fatalf("accepted %s", fault)
				}
				return
			}
			if err != nil || digest != evidenceDigest(e) {
				t.Fatalf("replay failed: %s %v", digest, err)
			}
			if len(rpc.observed) != 3 {
				t.Fatal("did not replay every read")
			}
			for _, n := range rpc.observed {
				if n.Uint64() != 15 {
					t.Fatal("read unpinned state")
				}
			}
		})
	}
}
