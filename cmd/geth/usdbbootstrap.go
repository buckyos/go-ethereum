package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

const (
	defaultUSDBBootstrapAdminBalanceWei = "10000000000000000000"
	defaultSourceDAODaoArtifact         = "contracts/Dao.sol/SourceDao.json"
	defaultSourceDAODividendArtifact    = "contracts/Dividend.sol/DividendContract.json"
)

type usdbChainBootstrapConfig struct {
	ChainID                  uint64  `json:"chainId"`
	ArtifactsDir             string  `json:"artifactsDir"`
	DaoAddress               string  `json:"daoAddress"`
	DaoArtifact              string  `json:"daoArtifact"`
	DividendAddress          string  `json:"dividendAddress"`
	DividendArtifact         string  `json:"dividendArtifact"`
	BootstrapAdminPrivateKey string  `json:"bootstrapAdminPrivateKey"`
	BootstrapAdminBalanceWei string  `json:"bootstrapAdminBalanceWei"`
	GenesisDifficulty        string  `json:"genesisDifficulty"`
	MinimumDifficulty        string  `json:"minimumDifficulty"`
	DividendFeeSplitBlock    *uint64 `json:"dividendFeeSplitBlock"`
}

type hardhatRuntimeArtifact struct {
	DeployedBytecode string `json:"deployedBytecode"`
}

func loadUSDBBootstrapGenesisFromSourceDAOConfig(configPath string) (*core.Genesis, error) {
	configPath = filepath.Clean(configPath)

	blob, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read bootstrap config %s: %w", configPath, err)
	}
	var config usdbChainBootstrapConfig
	if err := json.Unmarshal(blob, &config); err != nil {
		return nil, fmt.Errorf("decode bootstrap config %s: %w", configPath, err)
	}
	if config.ChainID != 0 && config.ChainID != params.USDBNetworkID {
		return nil, fmt.Errorf("bootstrap config chainId %d does not match USDB network id %d", config.ChainID, params.USDBNetworkID)
	}

	daoAddress, err := parseRequiredAddress("daoAddress", config.DaoAddress)
	if err != nil {
		return nil, err
	}
	dividendAddress, err := parseRequiredAddress("dividendAddress", config.DividendAddress)
	if err != nil {
		return nil, err
	}
	bootstrapAdmin, err := deriveBootstrapAdminAddress(config.BootstrapAdminPrivateKey)
	if err != nil {
		return nil, err
	}
	bootstrapAdminBalance, err := parseBootstrapAdminBalance(config.BootstrapAdminBalanceWei)
	if err != nil {
		return nil, err
	}
	genesisDifficulty, err := parseOptionalPositiveBigInt("genesisDifficulty", config.GenesisDifficulty)
	if err != nil {
		return nil, err
	}
	minimumDifficulty, err := parseOptionalPositiveBigInt("minimumDifficulty", config.MinimumDifficulty)
	if err != nil {
		return nil, err
	}

	artifactsDir := resolveSourceDAOArtifactsDir(configPath, config.ArtifactsDir)
	daoCode, err := loadHardhatRuntimeBytecode(resolveSourceDAOArtifactPath(artifactsDir, config.DaoArtifact, defaultSourceDAODaoArtifact))
	if err != nil {
		return nil, fmt.Errorf("load dao runtime code: %w", err)
	}
	dividendCode, err := loadHardhatRuntimeBytecode(resolveSourceDAOArtifactPath(artifactsDir, config.DividendArtifact, defaultSourceDAODividendArtifact))
	if err != nil {
		return nil, fmt.Errorf("load dividend runtime code: %w", err)
	}

	opts := core.USDBBootstrapGenesisConfig{
		DaoAddress:            daoAddress,
		DaoCode:               daoCode,
		DividendAddress:       dividendAddress,
		DividendCode:          dividendCode,
		BootstrapAdmin:        bootstrapAdmin,
		BootstrapAdminBalance: bootstrapAdminBalance,
		GenesisDifficulty:     genesisDifficulty,
		MinimumDifficulty:     minimumDifficulty,
	}
	if config.DividendFeeSplitBlock != nil {
		opts.DividendFeeSplitBlock = new(big.Int).SetUint64(*config.DividendFeeSplitBlock)
	}
	return core.DefaultUSDBGenesisBlockWithBootstrap(opts), nil
}

func parseRequiredAddress(field string, value string) (common.Address, error) {
	if !common.IsHexAddress(value) {
		return common.Address{}, fmt.Errorf("invalid %s %q", field, value)
	}
	return common.HexToAddress(value), nil
}

func deriveBootstrapAdminAddress(keyHex string) (common.Address, error) {
	key, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		return common.Address{}, fmt.Errorf("invalid bootstrapAdminPrivateKey: %w", err)
	}
	return crypto.PubkeyToAddress(key.PublicKey), nil
}

func parseBootstrapAdminBalance(value string) (*big.Int, error) {
	if value == "" {
		value = defaultUSDBBootstrapAdminBalanceWei
	}
	balance, ok := new(big.Int).SetString(value, 0)
	if !ok || balance.Sign() < 0 {
		return nil, fmt.Errorf("invalid bootstrapAdminBalanceWei %q", value)
	}
	return balance, nil
}

func parseOptionalPositiveBigInt(field string, value string) (*big.Int, error) {
	if value == "" {
		return nil, nil
	}
	parsed, ok := new(big.Int).SetString(value, 0)
	if !ok || parsed.Sign() <= 0 {
		return nil, fmt.Errorf("invalid %s %q", field, value)
	}
	return parsed, nil
}

func resolveSourceDAOArtifactsDir(configPath string, artifactsDir string) string {
	if artifactsDir == "" {
		return filepath.Clean(filepath.Join(filepath.Dir(configPath), "..", "..", "artifacts-usdb"))
	}
	if filepath.IsAbs(artifactsDir) {
		return filepath.Clean(artifactsDir)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(configPath), "..", "..", artifactsDir))
}

func resolveSourceDAOArtifactPath(artifactsDir string, override string, fallback string) string {
	relative := fallback
	if override != "" {
		relative = override
	}
	if filepath.IsAbs(relative) {
		return filepath.Clean(relative)
	}
	return filepath.Clean(filepath.Join(artifactsDir, relative))
}

func loadHardhatRuntimeBytecode(path string) ([]byte, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read artifact %s: %w", path, err)
	}
	var artifact hardhatRuntimeArtifact
	if err := json.Unmarshal(blob, &artifact); err != nil {
		return nil, fmt.Errorf("decode artifact %s: %w", path, err)
	}
	code := common.FromHex(artifact.DeployedBytecode)
	if len(code) == 0 {
		return nil, fmt.Errorf("artifact %s has empty deployedBytecode", path)
	}
	return code, nil
}
