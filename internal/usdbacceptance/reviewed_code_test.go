// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
// Licensed under the GNU Lesser General Public License, version 3 or later.

package usdbacceptance

import (
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestReviewedRuntimeRejectsWrongCodeAndImmutable(t *testing.T) {
	template := append([]byte{0x60}, make([]byte, 32)...)
	contract := reviewedContract{Name: "Committee", RuntimeHash: crypto.Keccak256Hash(template)}
	if err := json.Unmarshal([]byte(`{"immutable_references":{"1":[{"start":1,"length":32}]}}`), &contract); err != nil {
		t.Fatal(err)
	}
	implementation := common.HexToAddress(testDAO)
	actual := append([]byte(nil), template...)
	copy(actual[1:], common.LeftPadBytes(implementation.Bytes(), 32))
	if err := matchReviewedRuntime(actual, contract, &implementation); err != nil {
		t.Fatal(err)
	}
	actual[0] = 0x61
	if err := matchReviewedRuntime(actual, contract, &implementation); err == nil {
		t.Fatal("accepted wrong implementation with same ABI/version")
	}
	actual[0] = 0x60
	actual[32] ^= 1
	if err := matchReviewedRuntime(actual, contract, &implementation); err == nil {
		t.Fatal("accepted wrong UUPS self immutable")
	}
	if err := matchReviewedRuntime(actual[:2], contract, &implementation); err == nil {
		t.Fatal("accepted truncated runtime")
	}
}
