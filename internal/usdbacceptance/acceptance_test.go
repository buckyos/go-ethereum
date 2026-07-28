// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package usdbacceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

const (
	testChainID  = uint64(20260323)
	testDAO      = "0x0000000000000000000000000000000000001001"
	testDividend = "0x0000000000000000000000000000000000001002"
	testAdmin    = "0x0000000000000000000000000000000000001003"
)

var testModuleAddresses = map[string]string{
	"committee":   "0x0000000000000000000000000000000000002001",
	"devToken":    "0x0000000000000000000000000000000000002002",
	"normalToken": "0x0000000000000000000000000000000000002003",
	"lockup":      "0x0000000000000000000000000000000000002004",
	"project":     "0x0000000000000000000000000000000000002005",
	"dividend":    testDividend,
	"acquired":    "0x0000000000000000000000000000000000002006",
}

func TestCreateAndVerifyAcceptance(t *testing.T) {
	files := writeAcceptanceFixture(t, testAdmin)
	chain := testChainIdentity()

	artifact, err := Create(files, chain)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if artifact.SchemaVersion != SchemaVersion {
		t.Fatalf("unexpected schema %q", artifact.SchemaVersion)
	}
	if artifact.Bootstrap.MaxOperationBlock != 12 {
		t.Fatalf("unexpected max operation block %d", artifact.Bootstrap.MaxOperationBlock)
	}
	if err := Verify(artifact, files, chain); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	artifactPath := filepath.Join(t.TempDir(), "acceptance.json")
	if err := WriteArtifact(artifactPath, artifact); err != nil {
		t.Fatalf("WriteArtifact failed: %v", err)
	}
	decoded, err := ReadArtifact(artifactPath)
	if err != nil {
		t.Fatalf("ReadArtifact failed: %v", err)
	}
	if err := Verify(decoded, files, chain); err != nil {
		t.Fatalf("Verify decoded artifact failed: %v", err)
	}

	// Host-local validation fields do not change the normalized identity.
	validation := readFixtureJSON(t, files.Validation)
	validation["generatedAt"] = "2030-01-01T00:00:00Z"
	validation["rpcUrl"] = "http://joiner:8545"
	validation["artifactsDir"] = "/release/artifacts"
	writeFixtureJSON(t, files.Validation, validation)
	if err := Verify(artifact, files, chain); err != nil {
		t.Fatalf("Verify rejected equivalent joiner validation: %v", err)
	}
}

func TestCreateRejectsBootstrapAdminMismatch(t *testing.T) {
	files := writeAcceptanceFixture(t, "0x0000000000000000000000000000000000009999")
	_, err := Create(files, testChainIdentity())
	if err == nil || !strings.Contains(err.Error(), "bootstrap admin addresses do not match") {
		t.Fatalf("expected bootstrap admin mismatch, got %v", err)
	}
}

func TestCreateRejectsCheckpointBeforeBootstrapCompletion(t *testing.T) {
	files := writeAcceptanceFixture(t, testAdmin)
	chain := testChainIdentity()
	chain.Checkpoint.Number = 11

	_, err := Create(files, chain)
	if err == nil || !strings.Contains(err.Error(), "precedes bootstrap operation block") {
		t.Fatalf("expected operation boundary rejection, got %v", err)
	}
}

func TestCreateRejectsInsufficientConfirmations(t *testing.T) {
	files := writeAcceptanceFixture(t, testAdmin)
	chain := testChainIdentity()
	chain.HeadNumber = chain.Checkpoint.Number + chain.Confirmations - 1

	_, err := Create(files, chain)
	if err == nil || !strings.Contains(err.Error(), "does not have 3 confirmations") {
		t.Fatalf("expected confirmation-depth rejection, got %v", err)
	}
}

func TestVerifyRejectsCheckpointReplacement(t *testing.T) {
	files := writeAcceptanceFixture(t, testAdmin)
	chain := testChainIdentity()
	artifact, err := Create(files, chain)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	chain.Checkpoint.Hash = common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err := Verify(artifact, files, chain); err == nil || !strings.Contains(err.Error(), "checkpoint mismatch") {
		t.Fatalf("expected checkpoint mismatch, got %v", err)
	}
}

func TestCreateRejectsUnexpectedCandidateTransaction(t *testing.T) {
	files := writeAcceptanceFixture(t, testAdmin)
	chain := testChainIdentity()
	chain.Transactions = append(chain.Transactions,
		common.HexToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
	)
	_, err := Create(files, chain)
	if err == nil || !strings.Contains(err.Error(), "outside the completed bootstrap operation set") {
		t.Fatalf("expected unexpected-transaction rejection, got %v", err)
	}
}

func TestReadArtifactRejectsDuplicateKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acceptance.json")
	content := `{"schema_version":"uip-0010-bootstrap-acceptance:v1","schema_version":"other"}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadArtifact(path); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("expected duplicate-key rejection, got %v", err)
	}
}

func TestCreateRejectsDuplicateBootstrapInputKey(t *testing.T) {
	files := writeAcceptanceFixture(t, testAdmin)
	duplicate := []byte(`{"schemaVersion":1,"schemaVersion":1}`)
	if err := os.WriteFile(files.BootstrapConfig, duplicate, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(files, testChainIdentity()); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("expected duplicate bootstrap input rejection, got %v", err)
	}
}

func testChainIdentity() ChainIdentity {
	return ChainIdentity{
		ChainID:       testChainID,
		GenesisHash:   common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
		HeadNumber:    20,
		Confirmations: 3,
		Checkpoint: BlockIdentity{
			Number:    15,
			Hash:      common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
			StateRoot: common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333"),
		},
		Transactions: []common.Hash{
			common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		},
	}
}

func writeAcceptanceFixture(t *testing.T, validationAdmin string) InputFiles {
	t.Helper()
	dir := t.TempDir()
	files := InputFiles{
		GenesisJSON:     filepath.Join(dir, "genesis.json"),
		BootstrapConfig: filepath.Join(dir, "bootstrap-config.json"),
		BootstrapState:  filepath.Join(dir, "bootstrap-state.json"),
		Validation:      filepath.Join(dir, "validation.json"),
	}
	writeFixtureJSON(t, files.GenesisJSON, map[string]interface{}{
		"config": map[string]interface{}{"chainId": testChainID},
	})
	writeFixtureJSON(t, files.BootstrapConfig, map[string]interface{}{
		"schemaVersion":         1,
		"chainId":               testChainID,
		"rpcUrl":                "http://candidate:8545",
		"artifactsDir":          "../../artifacts-usdb",
		"daoAddress":            testDAO,
		"dividendAddress":       testDividend,
		"bootstrapAdminAddress": testAdmin,
		"cycleMinLength":        60,
		"expectedModules":       testModuleAddresses,
	})
	writeFixtureJSON(t, files.BootstrapState, map[string]interface{}{
		"state_version":    "1",
		"generated_at":     "2026-07-27T00:00:00Z",
		"completed_at":     "2026-07-27T00:01:00Z",
		"status":           "completed",
		"scope":            "full",
		"message":          "completed",
		"rpc_url":          "http://candidate:8545",
		"chain_id":         testChainID,
		"dao_address":      testDAO,
		"dividend_address": testDividend,
		"bootstrap_admin":  testAdmin,
		"operations": []interface{}{
			map[string]interface{}{
				"name":         "Dao.initialize",
				"status":       "completed",
				"tx_hash":      "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"block_number": 10,
			},
			map[string]interface{}{
				"name":         "Dividend.finalizeBootstrap",
				"status":       "completed",
				"tx_hash":      "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				"block_number": 12,
			},
		},
		"final_wiring": map[string]interface{}{
			"committee":    testModuleAddresses["committee"],
			"dev_token":    testModuleAddresses["devToken"],
			"normal_token": testModuleAddresses["normalToken"],
			"token_lockup": testModuleAddresses["lockup"],
			"project":      testModuleAddresses["project"],
			"dividend":     testModuleAddresses["dividend"],
			"acquired":     testModuleAddresses["acquired"],
		},
	})
	modules := make(map[string]interface{}, len(testModuleAddresses))
	for name, address := range testModuleAddresses {
		modules[name] = map[string]interface{}{
			"address":         address,
			"version":         "1.0.0",
			"expectedAddress": address,
		}
	}
	writeFixtureJSON(t, files.Validation, map[string]interface{}{
		"status":         "ok",
		"generatedAt":    "2026-07-27T00:01:30Z",
		"chainId":        testChainID,
		"rpcUrl":         "http://candidate:8545",
		"artifactsDir":   "/candidate/artifacts",
		"mode":           "strict",
		"daoAddress":     testDAO,
		"bootstrapAdmin": validationAdmin,
		"modules":        modules,
	})
	return files
}

func writeFixtureJSON(t *testing.T, path string, value interface{}) {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	content = append(content, '\n')
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFixtureJSON(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]interface{}
	if err := json.Unmarshal(content, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
