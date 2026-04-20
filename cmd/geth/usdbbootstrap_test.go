package main

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

func TestLoadUSDBBootstrapGenesisFromSourceDAOConfig(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "tools", "config")
	artifactsDir := filepath.Join(root, "artifacts-usdb", "contracts")
	if err := os.MkdirAll(filepath.Join(artifactsDir, "Dao.sol"), 0o755); err != nil {
		t.Fatalf("failed to create dao artifact directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(artifactsDir, "Dividend.sol"), 0o755); err != nil {
		t.Fatalf("failed to create dividend artifact directory: %v", err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config directory: %v", err)
	}

	writeArtifact := func(path string, deployedBytecode string) {
		t.Helper()
		blob, err := json.Marshal(hardhatRuntimeArtifact{DeployedBytecode: deployedBytecode})
		if err != nil {
			t.Fatalf("failed to encode artifact %s: %v", path, err)
		}
		if err := os.WriteFile(path, blob, 0o600); err != nil {
			t.Fatalf("failed to write artifact %s: %v", path, err)
		}
	}
	writeArtifact(filepath.Join(artifactsDir, "Dao.sol", "SourceDao.json"), "0x600060005560206000f3")
	writeArtifact(filepath.Join(artifactsDir, "Dividend.sol", "DividendContract.json"), "0x600160005560206000f3")

	configPath := filepath.Join(configDir, "usdb-local.json")
	configBlob := []byte(`{
  "chainId": 20260323,
  "artifactsDir": "./artifacts-usdb",
  "daoAddress": "0x0000000000000000000000000000000000001001",
  "dividendAddress": "0x0000000000000000000000000000000000001002",
  "bootstrapAdminPrivateKey": "4f3edf983ac636a65a842ce7c78d9aa706d3b113bce036f4f5bcaeaf3f4e6f54",
  "bootstrapAdminBalanceWei": "0x16345785d8a0000",
  "genesisDifficulty": "0x40000",
  "minimumDifficulty": "0x20000",
  "dividendFeeSplitBlock": 16
}`)
	if err := os.WriteFile(configPath, configBlob, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	genesis, err := loadUSDBBootstrapGenesisFromSourceDAOConfig(configPath)
	if err != nil {
		t.Fatalf("failed to build bootstrap genesis: %v", err)
	}
	if genesis.Config == params.USDBChainConfig {
		t.Fatal("bootstrap genesis should clone the base USDB config")
	}

	daoAddress := common.HexToAddress("0x0000000000000000000000000000000000001001")
	dividendAddress := common.HexToAddress("0x0000000000000000000000000000000000001002")
	if len(genesis.Alloc[daoAddress].Code) == 0 {
		t.Fatal("dao code not injected into bootstrap genesis")
	}
	if len(genesis.Alloc[dividendAddress].Code) == 0 {
		t.Fatal("dividend code not injected into bootstrap genesis")
	}
	if got := genesis.Config.DividendAddress; got != dividendAddress {
		t.Fatalf("unexpected dividend address: %s", got)
	}
	if got := genesis.Config.DividendFeeSplitBlock; got == nil || got.Uint64() != 16 {
		t.Fatalf("unexpected dividend fee split block: %v", got)
	}
	if got := genesis.Difficulty; got == nil || got.Cmp(big.NewInt(0x40000)) != 0 {
		t.Fatalf("unexpected genesis difficulty: %v", got)
	}
	if got := genesis.Config.EthPoWMinimumDifficulty(); got.Cmp(big.NewInt(0x20000)) != 0 {
		t.Fatalf("unexpected minimum difficulty: %v", got)
	}

	key, err := crypto.HexToECDSA("4f3edf983ac636a65a842ce7c78d9aa706d3b113bce036f4f5bcaeaf3f4e6f54")
	if err != nil {
		t.Fatalf("failed to parse bootstrap key: %v", err)
	}
	adminAddress := crypto.PubkeyToAddress(key.PublicKey)
	balance := genesis.Alloc[adminAddress].Balance
	if balance == nil || balance.Cmp(new(big.Int).SetUint64(100000000000000000)) != 0 {
		t.Fatalf("unexpected bootstrap admin balance: %v", balance)
	}
}
