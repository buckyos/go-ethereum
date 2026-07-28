// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package usdbacceptance defines the auditable checkpoint that promotes a
// controlled USDB bootstrap candidate chain into an accepted public chain.
package usdbacceptance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

const (
	// SchemaVersion identifies the first frozen bootstrap acceptance artifact.
	SchemaVersion = "uip-0010-bootstrap-acceptance:v1"

	bootstrapConfigSchemaVersion = 1
	bootstrapStateVersion        = "1"
)

var requiredModules = []string{
	"acquired",
	"committee",
	"devToken",
	"dividend",
	"lockup",
	"normalToken",
	"project",
}

// InputFiles names the release files committed by an acceptance artifact.
type InputFiles struct {
	GenesisJSON     string
	BootstrapConfig string
	BootstrapState  string
	Validation      string
}

// ChainIdentity captures the immutable chain data observed while creating or
// verifying an acceptance artifact.
type ChainIdentity struct {
	ChainID       uint64
	GenesisHash   common.Hash
	HeadNumber    uint64
	Checkpoint    BlockIdentity
	Confirmations uint64
	Transactions  []common.Hash
}

// BlockIdentity identifies one block and the state committed by its header.
type BlockIdentity struct {
	Number    uint64      `json:"number"`
	Hash      common.Hash `json:"hash"`
	StateRoot common.Hash `json:"state_root"`
}

// ModuleIdentity is the normalized result of strict SourceDAO module validation.
type ModuleIdentity struct {
	Address common.Address `json:"address"`
	Version string         `json:"version"`
}

// ValidationIdentity omits host paths, RPC URLs, and timestamps so independent
// joiners can reproduce the same strict validation digest.
type ValidationIdentity struct {
	ChainID         uint64                    `json:"chain_id"`
	DAOAddress      common.Address            `json:"dao_address"`
	DividendAddress common.Address            `json:"dividend_address"`
	BootstrapAdmin  common.Address            `json:"bootstrap_admin"`
	Modules         map[string]ModuleIdentity `json:"modules"`
}

// BootstrapCommitment binds the exact bootstrap inputs and normalized strict
// validation result accepted by the release operator.
type BootstrapCommitment struct {
	ConfigSHA256             string             `json:"config_sha256"`
	StateSHA256              string             `json:"state_sha256"`
	ValidationIdentitySHA256 string             `json:"validation_identity_sha256"`
	MaxOperationBlock        uint64             `json:"max_operation_block"`
	OperationTransactions    []common.Hash      `json:"operation_transactions"`
	Validation               ValidationIdentity `json:"validation"`
}

// GenesisCommitment binds both the canonical genesis file and resulting block.
type GenesisCommitment struct {
	JSONSHA256 string      `json:"json_sha256"`
	BlockHash  common.Hash `json:"block_hash"`
}

// Artifact is the signed-release input that promotes a bootstrap candidate
// chain into an accepted network.
type Artifact struct {
	SchemaVersion     string              `json:"schema_version"`
	ChainID           uint64              `json:"chain_id"`
	Genesis           GenesisCommitment   `json:"genesis"`
	Checkpoint        BlockIdentity       `json:"checkpoint"`
	ConfirmationDepth uint64              `json:"confirmation_depth"`
	Bootstrap         BootstrapCommitment `json:"bootstrap"`
}

type bootstrapConfig struct {
	SchemaVersion         uint64            `json:"schemaVersion"`
	ChainID               uint64            `json:"chainId"`
	DAOAddress            string            `json:"daoAddress"`
	DividendAddress       string            `json:"dividendAddress"`
	BootstrapAdminAddress string            `json:"bootstrapAdminAddress"`
	ExpectedModules       map[string]string `json:"expectedModules"`
}

type bootstrapOperation struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	TxHash      string `json:"tx_hash"`
	BlockNumber uint64 `json:"block_number"`
}

type bootstrapState struct {
	StateVersion    string               `json:"state_version"`
	Status          string               `json:"status"`
	Scope           string               `json:"scope"`
	ChainID         uint64               `json:"chain_id"`
	DAOAddress      string               `json:"dao_address"`
	DividendAddress string               `json:"dividend_address"`
	BootstrapAdmin  string               `json:"bootstrap_admin"`
	Operations      []bootstrapOperation `json:"operations"`
	FinalWiring     struct {
		Committee   string `json:"committee"`
		DevToken    string `json:"dev_token"`
		NormalToken string `json:"normal_token"`
		Lockup      string `json:"token_lockup"`
		Project     string `json:"project"`
		Dividend    string `json:"dividend"`
		Acquired    string `json:"acquired"`
	} `json:"final_wiring"`
}

type validationSummary struct {
	Status         string `json:"status"`
	ChainID        uint64 `json:"chainId"`
	Mode           string `json:"mode"`
	DAOAddress     string `json:"daoAddress"`
	BootstrapAdmin string `json:"bootstrapAdmin"`
	Modules        map[string]struct {
		Address         string  `json:"address"`
		Version         string  `json:"version"`
		ExpectedAddress *string `json:"expectedAddress"`
	} `json:"modules"`
}

type normalizedInputs struct {
	genesisSHA256     string
	configSHA256      string
	stateSHA256       string
	validation        ValidationIdentity
	validationHash    string
	maxOperationBlock uint64
	operationTxs      []common.Hash
}

// Create builds an acceptance artifact after checking exact agreement between
// the bootstrap config, completed state, strict validation, and chain identity.
func Create(files InputFiles, chain ChainIdentity) (*Artifact, error) {
	inputs, err := normalizeInputs(files)
	if err != nil {
		return nil, err
	}
	if err := validateChainIdentity(chain); err != nil {
		return nil, err
	}
	if chain.ChainID != inputs.validation.ChainID {
		return nil, fmt.Errorf("chain ID mismatch: RPC=%d bootstrap=%d", chain.ChainID, inputs.validation.ChainID)
	}
	if chain.Checkpoint.Number < inputs.maxOperationBlock {
		return nil, fmt.Errorf("checkpoint block %d precedes bootstrap operation block %d", chain.Checkpoint.Number, inputs.maxOperationBlock)
	}
	if !equalHashes(chain.Transactions, inputs.operationTxs) {
		return nil, errors.New("candidate chain contains transactions outside the completed bootstrap operation set")
	}
	if chain.HeadNumber < chain.Checkpoint.Number || chain.HeadNumber-chain.Checkpoint.Number < chain.Confirmations {
		return nil, fmt.Errorf("checkpoint block %d does not have %d confirmations at head %d", chain.Checkpoint.Number, chain.Confirmations, chain.HeadNumber)
	}
	return &Artifact{
		SchemaVersion: SchemaVersion,
		ChainID:       chain.ChainID,
		Genesis: GenesisCommitment{
			JSONSHA256: inputs.genesisSHA256,
			BlockHash:  chain.GenesisHash,
		},
		Checkpoint:        chain.Checkpoint,
		ConfirmationDepth: chain.Confirmations,
		Bootstrap: BootstrapCommitment{
			ConfigSHA256:             inputs.configSHA256,
			StateSHA256:              inputs.stateSHA256,
			ValidationIdentitySHA256: inputs.validationHash,
			MaxOperationBlock:        inputs.maxOperationBlock,
			OperationTransactions:    inputs.operationTxs,
			Validation:               inputs.validation,
		},
	}, nil
}

// Verify checks an artifact against local release inputs and an independently
// observed chain. A mismatch means the chain has not passed bootstrap acceptance.
func Verify(artifact *Artifact, files InputFiles, chain ChainIdentity) error {
	if artifact == nil {
		return errors.New("bootstrap acceptance artifact is nil")
	}
	if artifact.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported bootstrap acceptance schema %q", artifact.SchemaVersion)
	}
	if err := validateArtifact(artifact); err != nil {
		return err
	}
	inputs, err := normalizeInputs(files)
	if err != nil {
		return err
	}
	if err := validateChainIdentity(chain); err != nil {
		return err
	}
	if artifact.ChainID != chain.ChainID {
		return fmt.Errorf("accepted chain ID %d does not match RPC chain ID %d", artifact.ChainID, chain.ChainID)
	}
	if artifact.Genesis.BlockHash != chain.GenesisHash {
		return fmt.Errorf("genesis block hash mismatch: accepted=%s RPC=%s", artifact.Genesis.BlockHash, chain.GenesisHash)
	}
	if artifact.Genesis.JSONSHA256 != inputs.genesisSHA256 {
		return errors.New("canonical genesis JSON does not match bootstrap acceptance artifact")
	}
	if artifact.Checkpoint != chain.Checkpoint {
		return fmt.Errorf("bootstrap checkpoint mismatch at block %d", artifact.Checkpoint.Number)
	}
	if chain.HeadNumber < artifact.Checkpoint.Number ||
		chain.HeadNumber-artifact.Checkpoint.Number < artifact.ConfirmationDepth {
		return fmt.Errorf("accepted checkpoint block %d has insufficient confirmations at head %d", artifact.Checkpoint.Number, chain.HeadNumber)
	}
	if artifact.Bootstrap.ConfigSHA256 != inputs.configSHA256 {
		return errors.New("bootstrap config does not match acceptance artifact")
	}
	if artifact.Bootstrap.StateSHA256 != inputs.stateSHA256 {
		return errors.New("bootstrap state does not match acceptance artifact")
	}
	if artifact.Bootstrap.ValidationIdentitySHA256 != inputs.validationHash {
		return errors.New("strict validation identity does not match acceptance artifact")
	}
	if artifact.Bootstrap.MaxOperationBlock != inputs.maxOperationBlock {
		return errors.New("bootstrap operation boundary does not match acceptance artifact")
	}
	if !equalHashes(artifact.Bootstrap.OperationTransactions, inputs.operationTxs) {
		return errors.New("bootstrap operation transactions do not match acceptance artifact")
	}
	if !equalHashes(chain.Transactions, inputs.operationTxs) {
		return errors.New("accepted checkpoint history contains transactions outside the bootstrap operation set")
	}
	if !equalValidationIdentity(artifact.Bootstrap.Validation, inputs.validation) {
		return errors.New("normalized strict validation result does not match acceptance artifact")
	}
	return nil
}

// ReadArtifact decodes and validates the static structure of an acceptance file.
func ReadArtifact(path string) (*Artifact, error) {
	var artifact Artifact
	if err := readStrictJSON(path, &artifact); err != nil {
		return nil, fmt.Errorf("read bootstrap acceptance artifact: %w", err)
	}
	if artifact.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("unsupported bootstrap acceptance schema %q", artifact.SchemaVersion)
	}
	if err := validateArtifact(&artifact); err != nil {
		return nil, err
	}
	return &artifact, nil
}

// WriteArtifact writes a deterministic, reviewable JSON representation.
func WriteArtifact(path string, artifact *Artifact) error {
	if artifact == nil {
		return errors.New("bootstrap acceptance artifact is nil")
	}
	if err := validateArtifact(artifact); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encode bootstrap acceptance artifact: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("write bootstrap acceptance artifact: %w", err)
	}
	return nil
}

func normalizeInputs(files InputFiles) (*normalizedInputs, error) {
	if files.GenesisJSON == "" || files.BootstrapConfig == "" || files.BootstrapState == "" || files.Validation == "" {
		return nil, errors.New("genesis, bootstrap config, bootstrap state, and strict validation files are required")
	}
	genesisHash, err := fileSHA256(files.GenesisJSON)
	if err != nil {
		return nil, fmt.Errorf("hash canonical genesis JSON: %w", err)
	}
	configHash, err := fileSHA256(files.BootstrapConfig)
	if err != nil {
		return nil, fmt.Errorf("hash bootstrap config: %w", err)
	}
	stateHash, err := fileSHA256(files.BootstrapState)
	if err != nil {
		return nil, fmt.Errorf("hash bootstrap state: %w", err)
	}

	var config bootstrapConfig
	if err := readJSON(files.BootstrapConfig, &config); err != nil {
		return nil, fmt.Errorf("read bootstrap config: %w", err)
	}
	var state bootstrapState
	if err := readJSON(files.BootstrapState, &state); err != nil {
		return nil, fmt.Errorf("read bootstrap state: %w", err)
	}
	var validation validationSummary
	if err := readJSON(files.Validation, &validation); err != nil {
		return nil, fmt.Errorf("read strict validation summary: %w", err)
	}
	identity, maxBlock, operationTxs, err := normalizeBootstrapIdentity(config, state, validation)
	if err != nil {
		return nil, err
	}
	identityJSON, err := json.Marshal(identity)
	if err != nil {
		return nil, fmt.Errorf("encode strict validation identity: %w", err)
	}
	digest := sha256.Sum256(identityJSON)
	return &normalizedInputs{
		genesisSHA256:     genesisHash,
		configSHA256:      configHash,
		stateSHA256:       stateHash,
		validation:        identity,
		validationHash:    hex.EncodeToString(digest[:]),
		maxOperationBlock: maxBlock,
		operationTxs:      operationTxs,
	}, nil
}

func normalizeBootstrapIdentity(config bootstrapConfig, state bootstrapState, validation validationSummary) (ValidationIdentity, uint64, []common.Hash, error) {
	if config.SchemaVersion != bootstrapConfigSchemaVersion {
		return ValidationIdentity{}, 0, nil, fmt.Errorf("unsupported bootstrap config schema %d", config.SchemaVersion)
	}
	if state.StateVersion != bootstrapStateVersion || state.Status != "completed" || state.Scope != "full" {
		return ValidationIdentity{}, 0, nil, errors.New("bootstrap state must be a completed full-bootstrap state")
	}
	if validation.Status != "ok" || validation.Mode != "strict" {
		return ValidationIdentity{}, 0, nil, errors.New("SourceDAO validation must be a successful strict validation")
	}
	if config.ChainID == 0 || config.ChainID != state.ChainID || config.ChainID != validation.ChainID {
		return ValidationIdentity{}, 0, nil, errors.New("bootstrap config, state, and validation chain IDs do not match")
	}

	dao, err := matchingAddress("DAO", config.DAOAddress, state.DAOAddress, validation.DAOAddress)
	if err != nil {
		return ValidationIdentity{}, 0, nil, err
	}
	dividend, err := matchingAddress("Dividend", config.DividendAddress, state.DividendAddress, state.FinalWiring.Dividend)
	if err != nil {
		return ValidationIdentity{}, 0, nil, err
	}
	admin, err := matchingAddress("bootstrap admin", config.BootstrapAdminAddress, state.BootstrapAdmin, validation.BootstrapAdmin)
	if err != nil {
		return ValidationIdentity{}, 0, nil, err
	}

	stateModules := map[string]string{
		"acquired":    state.FinalWiring.Acquired,
		"committee":   state.FinalWiring.Committee,
		"devToken":    state.FinalWiring.DevToken,
		"dividend":    state.FinalWiring.Dividend,
		"lockup":      state.FinalWiring.Lockup,
		"normalToken": state.FinalWiring.NormalToken,
		"project":     state.FinalWiring.Project,
	}
	if len(validation.Modules) != len(requiredModules) {
		return ValidationIdentity{}, 0, nil, fmt.Errorf("strict validation must contain exactly %d SourceDAO modules", len(requiredModules))
	}
	modules := make(map[string]ModuleIdentity, len(requiredModules))
	for _, name := range requiredModules {
		validated, ok := validation.Modules[name]
		if !ok {
			return ValidationIdentity{}, 0, nil, fmt.Errorf("strict validation is missing module %q", name)
		}
		address, err := matchingAddress(name, stateModules[name], validated.Address)
		if err != nil {
			return ValidationIdentity{}, 0, nil, err
		}
		if expected := config.ExpectedModules[name]; expected != "" {
			if _, err := matchingAddress(name+" expected config", address.Hex(), expected); err != nil {
				return ValidationIdentity{}, 0, nil, err
			}
		}
		if validated.ExpectedAddress != nil && *validated.ExpectedAddress != "" {
			if _, err := matchingAddress(name+" validation expectation", address.Hex(), *validated.ExpectedAddress); err != nil {
				return ValidationIdentity{}, 0, nil, err
			}
		}
		if strings.TrimSpace(validated.Version) == "" {
			return ValidationIdentity{}, 0, nil, fmt.Errorf("strict validation module %q has no version", name)
		}
		modules[name] = ModuleIdentity{Address: address, Version: validated.Version}
	}
	if modules["dividend"].Address != dividend {
		return ValidationIdentity{}, 0, nil, errors.New("validated Dividend module does not match configured Dividend address")
	}

	var maxBlock uint64
	completedOperations := 0
	operationTxs := make([]common.Hash, 0, len(state.Operations))
	seenTxs := make(map[common.Hash]struct{})
	for _, operation := range state.Operations {
		switch operation.Status {
		case "completed":
			if operation.Name == "" || !isHash(operation.TxHash) || operation.BlockNumber == 0 {
				return ValidationIdentity{}, 0, nil, errors.New("completed bootstrap operation is missing transaction evidence")
			}
			txHash := common.HexToHash(operation.TxHash)
			if _, exists := seenTxs[txHash]; exists {
				return ValidationIdentity{}, 0, nil, fmt.Errorf("duplicate completed bootstrap transaction %s", txHash)
			}
			seenTxs[txHash] = struct{}{}
			operationTxs = append(operationTxs, txHash)
			completedOperations++
			if operation.BlockNumber > maxBlock {
				maxBlock = operation.BlockNumber
			}
		case "skipped":
		case "error":
			return ValidationIdentity{}, 0, nil, fmt.Errorf("bootstrap operation %q failed", operation.Name)
		default:
			return ValidationIdentity{}, 0, nil, fmt.Errorf("bootstrap operation %q has invalid status %q", operation.Name, operation.Status)
		}
	}
	if completedOperations == 0 {
		return ValidationIdentity{}, 0, nil, errors.New("bootstrap acceptance requires completed transaction evidence")
	}
	sortHashes(operationTxs)
	return ValidationIdentity{
		ChainID:         config.ChainID,
		DAOAddress:      dao,
		DividendAddress: dividend,
		BootstrapAdmin:  admin,
		Modules:         modules,
	}, maxBlock, operationTxs, nil
}

func validateArtifact(artifact *Artifact) error {
	if artifact.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported bootstrap acceptance schema %q", artifact.SchemaVersion)
	}
	if artifact.ChainID == 0 || artifact.Genesis.BlockHash == (common.Hash{}) {
		return errors.New("bootstrap acceptance artifact has incomplete chain identity")
	}
	if !isSHA256(artifact.Genesis.JSONSHA256) ||
		!isSHA256(artifact.Bootstrap.ConfigSHA256) ||
		!isSHA256(artifact.Bootstrap.StateSHA256) ||
		!isSHA256(artifact.Bootstrap.ValidationIdentitySHA256) {
		return errors.New("bootstrap acceptance artifact has incomplete file commitments")
	}
	if artifact.Checkpoint.Hash == (common.Hash{}) || artifact.Checkpoint.StateRoot == (common.Hash{}) {
		return errors.New("bootstrap acceptance artifact has incomplete checkpoint identity")
	}
	if artifact.Checkpoint.Number < artifact.Bootstrap.MaxOperationBlock {
		return errors.New("bootstrap acceptance checkpoint precedes bootstrap completion")
	}
	if len(artifact.Bootstrap.OperationTransactions) == 0 {
		return errors.New("bootstrap acceptance artifact has no operation transactions")
	}
	operationTxs := append([]common.Hash(nil), artifact.Bootstrap.OperationTransactions...)
	sortHashes(operationTxs)
	for index, txHash := range operationTxs {
		if txHash == (common.Hash{}) {
			return errors.New("bootstrap acceptance artifact contains an empty operation transaction")
		}
		if index > 0 && operationTxs[index-1] == txHash {
			return fmt.Errorf("bootstrap acceptance artifact contains duplicate operation transaction %s", txHash)
		}
	}
	if !equalHashes(operationTxs, artifact.Bootstrap.OperationTransactions) {
		return errors.New("bootstrap acceptance operation transactions are not canonically sorted")
	}
	validation := artifact.Bootstrap.Validation
	if validation.ChainID != artifact.ChainID ||
		validation.DAOAddress == (common.Address{}) ||
		validation.DividendAddress == (common.Address{}) ||
		validation.BootstrapAdmin == (common.Address{}) {
		return errors.New("bootstrap acceptance validation identity is incomplete")
	}
	if len(validation.Modules) != len(requiredModules) {
		return errors.New("bootstrap acceptance validation identity has invalid module count")
	}
	for _, name := range requiredModules {
		module, ok := validation.Modules[name]
		if !ok || module.Address == (common.Address{}) || strings.TrimSpace(module.Version) == "" {
			return fmt.Errorf("bootstrap acceptance validation identity has invalid module %q", name)
		}
	}
	if validation.Modules["dividend"].Address != validation.DividendAddress {
		return errors.New("bootstrap acceptance Dividend module identity is inconsistent")
	}
	identityJSON, err := json.Marshal(artifact.Bootstrap.Validation)
	if err != nil {
		return fmt.Errorf("encode accepted validation identity: %w", err)
	}
	digest := sha256.Sum256(identityJSON)
	if artifact.Bootstrap.ValidationIdentitySHA256 != hex.EncodeToString(digest[:]) {
		return errors.New("bootstrap acceptance validation digest is invalid")
	}
	return nil
}

func validateChainIdentity(chain ChainIdentity) error {
	if chain.ChainID == 0 || chain.GenesisHash == (common.Hash{}) {
		return errors.New("RPC chain identity is incomplete")
	}
	if chain.Checkpoint.Hash == (common.Hash{}) || chain.Checkpoint.StateRoot == (common.Hash{}) {
		return errors.New("RPC checkpoint identity is incomplete")
	}
	return nil
}

func matchingAddress(label string, values ...string) (common.Address, error) {
	var expected common.Address
	for _, value := range values {
		if !common.IsHexAddress(value) {
			return common.Address{}, fmt.Errorf("%s has invalid address %q", label, value)
		}
		address := common.HexToAddress(value)
		if address == (common.Address{}) {
			return common.Address{}, fmt.Errorf("%s must not be the zero address", label)
		}
		if expected == (common.Address{}) {
			expected = address
		} else if expected != address {
			return common.Address{}, fmt.Errorf("%s addresses do not match", label)
		}
	}
	return expected, nil
}

func equalValidationIdentity(left, right ValidationIdentity) bool {
	leftJSON, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightJSON, err := json.Marshal(right)
	return err == nil && string(leftJSON) == string(rightJSON)
}

func equalHashes(left, right []common.Hash) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sortHashes(hashes []common.Hash) {
	sort.Slice(hashes, func(left, right int) bool {
		return strings.Compare(hashes[left].Hex(), hashes[right].Hex()) < 0
	})
}

func fileSHA256(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func isHash(value string) bool {
	if len(value) != 2+common.HashLength*2 || !strings.HasPrefix(value, "0x") {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}

func readJSON(path string, output interface{}) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := rejectDuplicateJSONKeys(content); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	return nil
}

func readStrictJSON(path string, output interface{}) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := rejectDuplicateJSONKeys(content); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return fmt.Errorf("invalid trailing JSON data: %w", err)
		}
		return errors.New("unexpected trailing JSON value")
	}
	return nil
}

func rejectDuplicateJSONKeys(input []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()

	var walkValue func() error
	walkValue = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, isDelimiter := token.(json.Delim)
		if !isDelimiter {
			return nil
		}
		switch delimiter {
		case '{':
			keys := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("JSON object key is not a string")
				}
				if _, exists := keys[key]; exists {
					return fmt.Errorf("duplicate JSON key %q", key)
				}
				keys[key] = struct{}{}
				if err := walkValue(); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil {
				return err
			}
			if end != json.Delim('}') {
				return errors.New("invalid JSON object terminator")
			}
		case '[':
			for decoder.More() {
				if err := walkValue(); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil {
				return err
			}
			if end != json.Delim(']') {
				return errors.New("invalid JSON array terminator")
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
		}
		return nil
	}
	if err := walkValue(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("unexpected trailing JSON token")
	}
	return nil
}
