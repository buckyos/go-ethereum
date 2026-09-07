// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License, version 3 or later.

package usdbacceptance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// ValidationEvidence binds the strict verifier's complete RPC transcript to one
// historical state and the reviewed artifact/configuration identities.
type ValidationEvidence struct {
	SchemaVersion string               `json:"schema_version"`
	Checkpoint    BlockIdentity        `json:"checkpoint"`
	GenesisHash   common.Hash          `json:"genesis_hash"`
	ConfigSHA256  string               `json:"config_sha256"`
	GoldenSHA256  string               `json:"golden_sha256"`
	Code          []CodeObservation    `json:"code"`
	Storage       []StorageObservation `json:"storage"`
	Calls         []CallObservation    `json:"calls"`
}

// CodeObservation identifies runtime bytes read at the checkpoint.
type CodeObservation struct {
	Address   common.Address `json:"address"`
	Keccak256 common.Hash    `json:"keccak256"`
}

// StorageObservation records a full storage word, including ERC1967 pointers.
type StorageObservation struct {
	Address common.Address `json:"address"`
	Slot    common.Hash    `json:"slot"`
	Value   common.Hash    `json:"value"`
}

// CallObservation records a sender-independent historical eth_call.
type CallObservation struct {
	To     common.Address `json:"to"`
	Data   hexutil.Bytes  `json:"data"`
	Result hexutil.Bytes  `json:"result"`
}

// EvidenceClient is the read-only RPC surface needed for independent replay.
type EvidenceClient interface {
	CodeAt(context.Context, common.Address, *big.Int) ([]byte, error)
	StorageAt(context.Context, common.Address, common.Hash, *big.Int) ([]byte, error)
	CallContract(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error)
	HeaderByNumber(context.Context, *big.Int) (*types.Header, error)
}

// ReadValidationEvidence rejects legacy summaries that only report addresses and versions.
func ReadValidationEvidence(path string) (ValidationEvidence, error) {
	var summary validationSummary
	if err := readJSON(path, &summary); err != nil {
		return ValidationEvidence{}, err
	}
	if summary.Status != "ok" || summary.Mode != "strict" {
		return ValidationEvidence{}, errors.New("successful strict validation is required")
	}
	if err := validateEvidence(summary.Evidence); err != nil {
		return ValidationEvidence{}, err
	}
	return summary.Evidence, nil
}

func evidenceDigest(e ValidationEvidence) string {
	data, _ := json.Marshal(e)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func validateEvidence(e ValidationEvidence) error {
	if e.SchemaVersion != "sourcedao-bootstrap-validation:v2" || e.Checkpoint.Hash == (common.Hash{}) || e.Checkpoint.StateRoot == (common.Hash{}) ||
		e.GenesisHash == (common.Hash{}) || !isSHA256(e.ConfigSHA256) || !isSHA256(e.GoldenSHA256) {
		return errors.New("strict validation requires v2 checkpoint, configuration and reviewed-artifact evidence")
	}
	if len(e.Code) == 0 || len(e.Storage) == 0 || len(e.Calls) == 0 {
		return errors.New("strict validation RPC evidence is incomplete")
	}
	seen := make(map[string]bool)
	for _, code := range e.Code {
		key := "code:" + code.Address.Hex()
		if code.Address == (common.Address{}) || code.Keccak256 == (common.Hash{}) || code.Keccak256 == crypto.Keccak256Hash(nil) || seen[key] {
			return errors.New("invalid or duplicate runtime evidence")
		}
		seen[key] = true
	}
	for _, word := range e.Storage {
		key := "storage:" + word.Address.Hex() + word.Slot.Hex()
		if word.Address == (common.Address{}) || seen[key] {
			return errors.New("invalid or duplicate storage evidence")
		}
		seen[key] = true
	}
	for _, call := range e.Calls {
		key := "call:" + call.To.Hex() + hex.EncodeToString(call.Data)
		if call.To == (common.Address{}) || len(call.Data) < 4 || len(call.Result) == 0 || seen[key] {
			return errors.New("invalid or duplicate call evidence")
		}
		seen[key] = true
	}
	return nil
}

func validateEvidenceCoverage(v ValidationIdentity) error {
	if err := validateEvidence(v.Evidence); err != nil {
		return err
	}
	addresses := []common.Address{v.DAOAddress}
	for _, name := range requiredModules {
		addresses = append(addresses, v.Modules[name].Address)
	}
	for _, address := range addresses {
		code, storage, call := false, false, false
		for _, entry := range v.Evidence.Code {
			code = code || entry.Address == address
		}
		for _, entry := range v.Evidence.Storage {
			storage = storage || entry.Address == address
		}
		for _, entry := range v.Evidence.Calls {
			call = call || entry.To == address
		}
		if !code || !storage || !call {
			return fmt.Errorf("strict validation is missing code, storage or call evidence for %s", address)
		}
	}
	return nil
}

// ObserveValidationEvidence replays all reads and rechecks canonicality before
// returning a digest usable by Create/Verify. Unavailable historical state fails.
func ObserveValidationEvidence(ctx context.Context, client EvidenceClient, e ValidationEvidence) (string, error) {
	if err := validateEvidence(e); err != nil {
		return "", err
	}
	number := new(big.Int).SetUint64(e.Checkpoint.Number)
	check := func() error {
		h, err := client.HeaderByNumber(ctx, number)
		if err != nil {
			return err
		}
		if h == nil || h.Hash() != e.Checkpoint.Hash || h.Root != e.Checkpoint.StateRoot {
			return errors.New("validation checkpoint changed or does not match RPC")
		}
		return nil
	}
	genesis, err := client.HeaderByNumber(ctx, big.NewInt(0))
	if err != nil {
		return "", err
	}
	if genesis == nil || genesis.Hash() != e.GenesisHash {
		return "", errors.New("validation genesis differs from RPC")
	}
	if err := check(); err != nil {
		return "", err
	}
	for _, entry := range e.Code {
		actual, err := client.CodeAt(ctx, entry.Address, number)
		if err != nil {
			return "", fmt.Errorf("read historical code %s: %w", entry.Address, err)
		}
		if crypto.Keccak256Hash(actual) != entry.Keccak256 {
			return "", fmt.Errorf("runtime evidence mismatch at %s", entry.Address)
		}
	}
	for _, entry := range e.Storage {
		actual, err := client.StorageAt(ctx, entry.Address, entry.Slot, number)
		if err != nil {
			return "", fmt.Errorf("read historical storage %s: %w", entry.Address, err)
		}
		if common.BytesToHash(actual) != entry.Value {
			return "", fmt.Errorf("storage evidence mismatch at %s slot %s", entry.Address, entry.Slot)
		}
	}
	for _, entry := range e.Calls {
		actual, err := client.CallContract(ctx, ethereum.CallMsg{To: &entry.To, Data: entry.Data}, number)
		if err != nil {
			return "", fmt.Errorf("replay historical call %s: %w", entry.To, err)
		}
		if !bytes.Equal(actual, entry.Result) {
			return "", fmt.Errorf("call evidence mismatch at %s selector %x", entry.To, entry.Data[:4])
		}
	}
	if err := check(); err != nil {
		return "", err
	}
	return evidenceDigest(e), nil
}

func publicConfigDigest(filename string) (string, error) {
	return canonicalFileDigest(filename, []string{"rpcUrl", "artifactsDir", "outputPath"})
}

func canonicalFileDigest(filename string, omitted []string) (string, error) {
	var config map[string]interface{}
	if err := readJSON(filename, &config); err != nil {
		return "", err
	}
	for _, key := range omitted {
		delete(config, key)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(config); err != nil {
		return "", err
	}
	// Match JSON.stringify for JavaScript line separator characters as well.
	canonical := strings.TrimSuffix(buffer.String(), "\n")
	canonical = strings.ReplaceAll(strings.ReplaceAll(canonical, `\u2028`, "\u2028"), `\u2029`, "\u2029")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}
