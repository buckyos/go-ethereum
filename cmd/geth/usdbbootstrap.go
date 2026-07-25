package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

const usdbGenesisBootstrapSchemaVersion uint64 = 1

type usdbGenesisBootstrapConfig struct {
	SchemaVersion         uint64                    `json:"schemaVersion"`
	ChainID               uint64                    `json:"chainId"`
	Predeploys            usdbGenesisPredeploys     `json:"predeploys"`
	BootstrapAdmin        usdbGenesisBootstrapAdmin `json:"bootstrapAdmin"`
	GenesisDifficulty     string                    `json:"genesisDifficulty"`
	MinimumDifficulty     string                    `json:"minimumDifficulty"`
	DividendFeeSplitBlock *uint64                   `json:"dividendFeeSplitBlock"`
}

type usdbGenesisPredeploys struct {
	Dao      usdbGenesisSystemContract `json:"dao"`
	Dividend usdbGenesisSystemContract `json:"dividend"`
}

type usdbGenesisSystemContract struct {
	Address         string `json:"address"`
	Artifact        string `json:"artifact"`
	RuntimeCodeHash string `json:"runtimeCodeHash"`
	ArtifactSHA256  string `json:"artifactSha256"`
}

type usdbGenesisBootstrapAdmin struct {
	Address    string `json:"address"`
	BalanceWei string `json:"balanceWei"`
}

type hardhatRuntimeArtifact struct {
	DeployedBytecode string `json:"deployedBytecode"`
}

func loadUSDBBootstrapGenesis(configPath string, artifactsRoot string) (*core.Genesis, error) {
	configPath = filepath.Clean(configPath)
	artifactsRoot, err := filepath.Abs(filepath.Clean(artifactsRoot))
	if err != nil {
		return nil, fmt.Errorf("resolve USDB bootstrap artifact root %s: %w", artifactsRoot, err)
	}
	artifactsRoot, err = filepath.EvalSymlinks(artifactsRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve USDB bootstrap artifact root %s: %w", artifactsRoot, err)
	}
	info, err := os.Stat(artifactsRoot)
	if err != nil {
		return nil, fmt.Errorf("stat USDB bootstrap artifact root %s: %w", artifactsRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("USDB bootstrap artifact root %s is not a directory", artifactsRoot)
	}

	blob, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read USDB genesis bootstrap config %s: %w", configPath, err)
	}
	var config usdbGenesisBootstrapConfig
	if err := decodeStrictJSON(blob, &config); err != nil {
		return nil, fmt.Errorf("decode USDB genesis bootstrap config %s: %w", configPath, err)
	}
	if config.SchemaVersion != usdbGenesisBootstrapSchemaVersion {
		return nil, fmt.Errorf(
			"unsupported USDB genesis bootstrap schemaVersion %d, expected %d",
			config.SchemaVersion,
			usdbGenesisBootstrapSchemaVersion,
		)
	}
	if config.ChainID != params.USDBNetworkID {
		return nil, fmt.Errorf(
			"USDB genesis bootstrap chainId %d does not match USDB network id %d",
			config.ChainID,
			params.USDBNetworkID,
		)
	}

	daoAddress, err := parseCanonicalNonZeroAddress("predeploys.dao.address", config.Predeploys.Dao.Address)
	if err != nil {
		return nil, err
	}
	dividendAddress, err := parseCanonicalNonZeroAddress("predeploys.dividend.address", config.Predeploys.Dividend.Address)
	if err != nil {
		return nil, err
	}
	bootstrapAdmin, err := parseCanonicalNonZeroAddress("bootstrapAdmin.address", config.BootstrapAdmin.Address)
	if err != nil {
		return nil, err
	}
	bootstrapAdminBalance, err := parseCanonicalPositiveBigInt("bootstrapAdmin.balanceWei", config.BootstrapAdmin.BalanceWei)
	if err != nil {
		return nil, err
	}
	genesisDifficulty, err := parseCanonicalPositiveBigInt("genesisDifficulty", config.GenesisDifficulty)
	if err != nil {
		return nil, err
	}
	minimumDifficulty, err := parseCanonicalPositiveBigInt("minimumDifficulty", config.MinimumDifficulty)
	if err != nil {
		return nil, err
	}
	if genesisDifficulty.Cmp(minimumDifficulty) < 0 {
		return nil, fmt.Errorf(
			"genesisDifficulty %s must not be below minimumDifficulty %s",
			config.GenesisDifficulty,
			config.MinimumDifficulty,
		)
	}
	if config.DividendFeeSplitBlock == nil || *config.DividendFeeSplitBlock == 0 {
		return nil, errors.New("dividendFeeSplitBlock must be a positive uint64")
	}

	daoCode, err := loadVerifiedHardhatRuntimeBytecode(artifactsRoot, "Dao", config.Predeploys.Dao)
	if err != nil {
		return nil, err
	}
	dividendCode, err := loadVerifiedHardhatRuntimeBytecode(artifactsRoot, "Dividend", config.Predeploys.Dividend)
	if err != nil {
		return nil, err
	}

	return core.DefaultUSDBGenesisBlockWithBootstrap(core.USDBBootstrapGenesisConfig{
		DaoAddress:            daoAddress,
		DaoCode:               daoCode,
		DividendAddress:       dividendAddress,
		DividendCode:          dividendCode,
		BootstrapAdmin:        bootstrapAdmin,
		BootstrapAdminBalance: bootstrapAdminBalance,
		DividendFeeSplitBlock: new(big.Int).SetUint64(*config.DividendFeeSplitBlock),
		GenesisDifficulty:     genesisDifficulty,
		MinimumDifficulty:     minimumDifficulty,
	})
}

func parseCanonicalNonZeroAddress(field string, value string) (common.Address, error) {
	if !strings.HasPrefix(value, "0x") || !common.IsHexAddress(value) {
		return common.Address{}, fmt.Errorf("%s must be a 0x-prefixed 20-byte EVM address, have %q", field, value)
	}
	address := common.HexToAddress(value)
	if address == (common.Address{}) {
		return common.Address{}, fmt.Errorf("%s must not be the zero address", field)
	}
	if value != address.Hex() {
		return common.Address{}, fmt.Errorf("%s must use canonical EIP-55 encoding %q, have %q", field, address.Hex(), value)
	}
	return address, nil
}

func parseCanonicalPositiveBigInt(field string, value string) (*big.Int, error) {
	if value == "" {
		return nil, fmt.Errorf("%s is required", field)
	}
	base := 10
	digits := value
	if strings.HasPrefix(value, "0x") {
		base = 16
		digits = value[2:]
		if digits == "" || strings.ToLower(digits) != digits {
			return nil, fmt.Errorf("%s must use canonical lowercase hexadecimal or decimal encoding, have %q", field, value)
		}
		if len(digits) > 1 && digits[0] == '0' {
			return nil, fmt.Errorf("%s hexadecimal encoding must not contain leading zeroes, have %q", field, value)
		}
	} else if len(value) > 1 && value[0] == '0' {
		return nil, fmt.Errorf("%s decimal encoding must not contain leading zeroes, have %q", field, value)
	}
	for _, digit := range digits {
		if (base == 10 && (digit < '0' || digit > '9')) ||
			(base == 16 && !strings.ContainsRune("0123456789abcdef", digit)) {
			return nil, fmt.Errorf("%s has invalid numeric encoding %q", field, value)
		}
	}
	parsed, ok := new(big.Int).SetString(digits, base)
	if !ok || parsed.Sign() <= 0 {
		return nil, fmt.Errorf("%s must be positive, have %q", field, value)
	}
	return parsed, nil
}

func loadVerifiedHardhatRuntimeBytecode(
	artifactsRoot string,
	contractName string,
	config usdbGenesisSystemContract,
) ([]byte, error) {
	artifactPath, err := resolveUSDBBootstrapArtifactPath(artifactsRoot, config.Artifact)
	if err != nil {
		return nil, fmt.Errorf("resolve %s artifact: %w", contractName, err)
	}
	blob, err := os.ReadFile(artifactPath)
	if err != nil {
		return nil, fmt.Errorf("read %s artifact %s: %w", contractName, artifactPath, err)
	}
	if err := verifyArtifactSHA256(contractName, config.ArtifactSHA256, blob); err != nil {
		return nil, err
	}
	if err := rejectDuplicateJSONFields(blob); err != nil {
		return nil, fmt.Errorf("decode %s artifact %s: %w", contractName, artifactPath, err)
	}
	var artifact hardhatRuntimeArtifact
	if err := json.Unmarshal(blob, &artifact); err != nil {
		return nil, fmt.Errorf("decode %s artifact %s: %w", contractName, artifactPath, err)
	}
	code, err := hexutil.Decode(artifact.DeployedBytecode)
	if err != nil {
		return nil, fmt.Errorf("%s artifact %s has invalid deployedBytecode: %w", contractName, artifactPath, err)
	}
	if len(code) == 0 {
		return nil, fmt.Errorf("%s artifact %s has empty deployedBytecode", contractName, artifactPath)
	}
	if len(code) > params.MaxCodeSize {
		return nil, fmt.Errorf(
			"%s artifact %s runtime code is %d bytes, exceeding MaxCodeSize %d",
			contractName,
			artifactPath,
			len(code),
			params.MaxCodeSize,
		)
	}

	expectedCodeHash, err := parseCanonicalHash(contractName+" runtimeCodeHash", config.RuntimeCodeHash)
	if err != nil {
		return nil, err
	}
	actualCodeHash := crypto.Keccak256Hash(code)
	if actualCodeHash != expectedCodeHash {
		return nil, fmt.Errorf(
			"%s runtime code hash mismatch: expected %s, got %s",
			contractName,
			expectedCodeHash,
			actualCodeHash,
		)
	}
	return code, nil
}

func resolveUSDBBootstrapArtifactPath(artifactsRoot string, artifact string) (string, error) {
	if artifact == "" {
		return "", errors.New("artifact path is required")
	}
	if filepath.IsAbs(artifact) {
		return "", fmt.Errorf("artifact path %q must be relative to the artifact root", artifact)
	}
	path := filepath.Clean(filepath.Join(artifactsRoot, artifact))
	relative, err := filepath.Rel(artifactsRoot, path)
	if err != nil {
		return "", fmt.Errorf("resolve artifact path %q: %w", artifact, err)
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact path %q escapes the artifact root", artifact)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve artifact path %q: %w", artifact, err)
	}
	relative, err = filepath.Rel(artifactsRoot, resolvedPath)
	if err != nil {
		return "", fmt.Errorf("resolve artifact path %q: %w", artifact, err)
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact path %q escapes the artifact root", artifact)
	}
	return resolvedPath, nil
}

func verifyArtifactSHA256(contractName string, expected string, blob []byte) error {
	if len(expected) != sha256.Size*2 || strings.ToLower(expected) != expected {
		return fmt.Errorf("%s artifactSha256 must be 64 lowercase hexadecimal characters, have %q", contractName, expected)
	}
	expectedBytes, err := hex.DecodeString(expected)
	if err != nil {
		return fmt.Errorf("%s artifactSha256 is invalid: %w", contractName, err)
	}
	actual := sha256.Sum256(blob)
	if !bytes.Equal(actual[:], expectedBytes) {
		return fmt.Errorf(
			"%s artifact SHA-256 mismatch: expected %s, got %x",
			contractName,
			expected,
			actual,
		)
	}
	return nil
}

func parseCanonicalHash(field string, value string) (common.Hash, error) {
	var hash common.Hash
	if err := hash.UnmarshalText([]byte(value)); err != nil {
		return common.Hash{}, fmt.Errorf("%s must be a 0x-prefixed 32-byte hash: %w", field, err)
	}
	if value != hash.Hex() {
		return common.Hash{}, fmt.Errorf("%s must use canonical lowercase encoding %q, have %q", field, hash.Hex(), value)
	}
	return hash, nil
}

func decodeStrictJSON(input []byte, destination interface{}) error {
	if err := rejectDuplicateJSONFields(input); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("invalid trailing JSON data: %w", err)
		}
		return errors.New("unexpected trailing JSON value")
	}
	return nil
}

func rejectDuplicateJSONFields(input []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	if err := consumeJSONValue(decoder, "$"); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("invalid trailing JSON data: %w", err)
		}
		return errors.New("unexpected trailing JSON value")
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON at %s: %w", path, err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("invalid JSON object key at %s: %w", path, err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key at %s is not a string", path)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON field %s.%s", path, key)
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder, path+"."+key); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("unterminated JSON object at %s: %w", path, err)
		}
		if end != json.Delim('}') {
			return fmt.Errorf("invalid JSON object terminator at %s", path)
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := consumeJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("unterminated JSON array at %s: %w", path, err)
		}
		if end != json.Delim(']') {
			return fmt.Errorf("invalid JSON array terminator at %s", path)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q at %s", delimiter, path)
	}
	return nil
}
