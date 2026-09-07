// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
// Licensed under the GNU Lesser General Public License, version 3 or later.

package usdbacceptance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type reviewedContract struct {
	Name        string      `json:"contract_name"`
	RuntimeHash common.Hash `json:"runtime_bytecode_keccak256"`
	Immutables  map[string][]struct {
		Start  int `json:"start"`
		Length int `json:"length"`
	} `json:"immutable_references"`
}

type reviewedGolden struct {
	SchemaVersion string             `json:"schema_version"`
	Contracts     []reviewedContract `json:"contracts"`
}

var implementationSlot = common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")
var reviewedModuleNames = map[string]string{
	"committee": "SourceDaoCommittee", "devToken": "DevToken", "normalToken": "NormalToken", "lockup": "SourceTokenLockup", "project": "ProjectManagement", "dividend": "DividendContract", "acquired": "Acquired",
}

func matchReviewedRuntime(code []byte, contract reviewedContract, implementation *common.Address) error {
	normalized := append([]byte(nil), code...)
	if implementation != nil {
		for _, references := range contract.Immutables {
			for _, ref := range references {
				if ref.Start < 0 || ref.Length != 32 || ref.Start > len(normalized)-ref.Length {
					return errors.New("invalid reviewed immutable reference")
				}
				word := normalized[ref.Start : ref.Start+ref.Length]
				if !bytes.Equal(word, common.LeftPadBytes(implementation.Bytes(), 32)) {
					return errors.New("UUPS immutable does not identify its implementation")
				}
				copy(word, make([]byte, 32))
			}
		}
	}
	if crypto.Keccak256Hash(normalized) != contract.RuntimeHash {
		return fmt.Errorf("runtime differs from reviewed %s artifact", contract.Name)
	}
	return nil
}

// ObserveReviewedCode independently compares checkpoint runtime to the locally
// reviewed golden, rather than trusting a hash asserted by the validation report.
// The golden must be distributed through the release review's trusted channel.
func ObserveReviewedCode(ctx context.Context, client EvidenceClient, files InputFiles) error {
	inputs, err := normalizeInputs(files)
	if err != nil {
		return err
	}
	var golden reviewedGolden
	if err := readJSON(files.ContractGolden, &golden); err != nil {
		return err
	}
	if golden.SchemaVersion != "sourcedao-usdb-contract-golden:v1" {
		return errors.New("unsupported reviewed contract golden")
	}
	records := make(map[string]reviewedContract)
	for _, contract := range golden.Contracts {
		if _, exists := records[contract.Name]; exists || contract.RuntimeHash == (common.Hash{}) {
			return errors.New("invalid or duplicate reviewed contract")
		}
		records[contract.Name] = contract
	}
	if len(records) != 9 {
		return errors.New("reviewed golden must contain nine production and proxy contracts")
	}
	number := new(big.Int).SetUint64(inputs.validation.Evidence.Checkpoint.Number)
	check := func(name string, address common.Address, implementation *common.Address) error {
		record, ok := records[name]
		if !ok {
			return fmt.Errorf("golden is missing %s", name)
		}
		code, err := client.CodeAt(ctx, address, number)
		if err != nil {
			return err
		}
		return matchReviewedRuntime(code, record, implementation)
	}
	if err := check("SourceDao", inputs.validation.DAOAddress, nil); err != nil {
		return err
	}
	daoSlot, err := client.StorageAt(ctx, inputs.validation.DAOAddress, implementationSlot, number)
	if err != nil {
		return err
	}
	if new(big.Int).SetBytes(daoSlot).Sign() != 0 {
		return errors.New("DAO predeploy has an unexpected implementation slot")
	}
	for _, key := range requiredModules {
		address := inputs.validation.Modules[key].Address
		slot, err := client.StorageAt(ctx, address, implementationSlot, number)
		if err != nil {
			return err
		}
		if key == "dividend" {
			if new(big.Int).SetBytes(slot).Sign() != 0 {
				return errors.New("Dividend predeploy has an unexpected implementation slot")
			}
			if err := check(reviewedModuleNames[key], address, nil); err != nil {
				return err
			}
			continue
		}
		if len(slot) != 32 || new(big.Int).SetBytes(slot).Sign() == 0 || new(big.Int).SetBytes(slot).BitLen() > 160 {
			return fmt.Errorf("invalid %s implementation slot", key)
		}
		implementation := common.BytesToAddress(slot)
		if err := check("ERC1967Proxy", address, nil); err != nil {
			return err
		}
		if err := check(reviewedModuleNames[key], implementation, &implementation); err != nil {
			return err
		}
	}
	header, err := client.HeaderByNumber(ctx, number)
	if err != nil {
		return err
	}
	if header == nil || header.Hash() != inputs.validation.Evidence.Checkpoint.Hash || header.Root != inputs.validation.Evidence.Checkpoint.StateRoot {
		return errors.New("checkpoint changed during reviewed-code inspection")
	}
	return nil
}
