package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	internalusdb "github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/params"
)

const (
	testDaoArtifactPath      = "contracts/Dao.sol/SourceDao.json"
	testDividendArtifactPath = "contracts/Dividend.sol/DividendContract.json"
	testUSDBBootstrapChainID = uint64(42_424_242)
)

type usdbBootstrapTestFixture struct {
	configPath    string
	artifactsRoot string
	config        usdbGenesisBootstrapConfig
	daoCode       []byte
	dividendCode  []byte
}

func TestLoadUSDBBootstrapGenesis(t *testing.T) {
	fixture := newUSDBBootstrapTestFixture(t)

	genesis, err := loadUSDBBootstrapGenesis(fixture.configPath, fixture.artifactsRoot)
	if err != nil {
		t.Fatalf("failed to build bootstrap genesis: %v", err)
	}
	if genesis.Config == params.USDBChainConfig {
		t.Fatal("bootstrap genesis should clone the base USDB config")
	}
	if got := genesis.Config.ChainID; got == nil || got.Uint64() != testUSDBBootstrapChainID {
		t.Fatalf("unexpected configurable chain ID: %v", got)
	}
	if got := genesis.Config.ChainID_ALT; got == nil || got.Uint64() != testUSDBBootstrapChainID {
		t.Fatalf("unexpected alternate chain ID: %v", got)
	}
	if got := genesis.Config.USDB.BTCNetworkID; got != "btc-regtest" {
		t.Fatalf("unexpected BTC source network: %q", got)
	}
	if got := genesis.Config.USDB.BTCIndexOriginHeight; got != 1 {
		t.Fatalf("unexpected BTC index origin height: %d", got)
	}

	daoAddress := common.HexToAddress(fixture.config.Predeploys.Dao.Address)
	dividendAddress := common.HexToAddress(fixture.config.Predeploys.Dividend.Address)
	adminAddress := common.HexToAddress(fixture.config.BootstrapAdmin.Address)
	if got := genesis.Alloc[daoAddress].Code; !bytes.Equal(got, fixture.daoCode) {
		t.Fatalf("unexpected Dao runtime code: %x", got)
	}
	if got := genesis.Alloc[dividendAddress].Code; !bytes.Equal(got, fixture.dividendCode) {
		t.Fatalf("unexpected Dividend runtime code: %x", got)
	}
	if got := genesis.Config.DividendAddress; got != dividendAddress {
		t.Fatalf("unexpected dividend address: %s", got)
	}
	if got := genesis.Config.DividendFeeSplitBlock; got == nil || got.Uint64() != 16 {
		t.Fatalf("unexpected dividend fee-split block: %v", got)
	}
	if got, want := genesis.Config.DividendCodeHash, crypto.Keccak256Hash(fixture.dividendCode); got != want {
		t.Fatalf("unexpected Dividend code hash: have %s want %s", got, want)
	}
	if got := genesis.Difficulty; got == nil || got.Cmp(big.NewInt(0x180000)) != 0 {
		t.Fatalf("unexpected genesis difficulty: %v", got)
	}
	if got := genesis.Config.EthPoWMinimumDifficulty(); got.Cmp(big.NewInt(0x100000)) != 0 {
		t.Fatalf("unexpected minimum difficulty: %v", got)
	}
	if got := genesis.Alloc[adminAddress].Balance; got == nil || got.Cmp(new(big.Int).SetUint64(10_000_000_000_000_000_000)) != 0 {
		t.Fatalf("unexpected bootstrap admin balance: %v", got)
	}

	configBlob, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatalf("failed to read bootstrap config: %v", err)
	}
	if bytes.Contains(configBlob, []byte("PrivateKey")) || bytes.Contains(configBlob, []byte("privateKey")) {
		t.Fatal("public USDB genesis bootstrap config contains a private-key field")
	}
}

func TestUSDBBootstrapConfigSelectsBTCMainnet(t *testing.T) {
	fixture := newUSDBBootstrapTestFixture(t)
	fixture.config.BTCSource = usdbGenesisBTCSource{
		NetworkID:         "btc-mainnet",
		IndexOriginHeight: 963_800,
	}
	fixture.config.USDBConsensus.Activations[0].BTCActivationRegistryID = internalusdb.BTCMainnetActivationRegistryIDV1
	writeUSDBBootstrapTestConfig(t, fixture.configPath, fixture.config)

	genesis, err := loadUSDBBootstrapGenesis(fixture.configPath, fixture.artifactsRoot)
	if err != nil {
		t.Fatalf("failed to build BTC-mainnet bootstrap genesis: %v", err)
	}
	if got := genesis.Config.USDB.BTCNetworkID; got != "btc-mainnet" {
		t.Fatalf("unexpected BTC source network: %q", got)
	}
	if got := genesis.Config.USDB.BTCIndexOriginHeight; got != 963_800 {
		t.Fatalf("unexpected BTC mainnet index origin height: %d", got)
	}
}

func TestUSDBBootstrapGenesisIsPathIndependentAndDeterministic(t *testing.T) {
	first := newUSDBBootstrapTestFixture(t)
	second := newUSDBBootstrapTestFixture(t)

	firstGenesis, err := loadUSDBBootstrapGenesis(first.configPath, first.artifactsRoot)
	if err != nil {
		t.Fatalf("failed to build first bootstrap genesis: %v", err)
	}
	secondGenesis, err := loadUSDBBootstrapGenesis(second.configPath, second.artifactsRoot)
	if err != nil {
		t.Fatalf("failed to build second bootstrap genesis: %v", err)
	}
	firstJSON, err := json.Marshal(firstGenesis)
	if err != nil {
		t.Fatalf("failed to encode first bootstrap genesis: %v", err)
	}
	secondJSON, err := json.Marshal(secondGenesis)
	if err != nil {
		t.Fatalf("failed to encode second bootstrap genesis: %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("identical bootstrap specs and artifacts produced different genesis JSON")
	}
	if firstGenesis.ToBlock().Hash() != secondGenesis.ToBlock().Hash() {
		t.Fatal("identical bootstrap specs and artifacts produced different genesis hashes")
	}
}

func TestUSDBBootstrapSchemaV2Templates(t *testing.T) {
	tests := []struct {
		name              string
		file              string
		wantChainID       uint64
		wantBTCNetwork    string
		wantOriginHeight  uint32
		wantRegistryID    string
		wantFeeSplitBlock uint64
	}{
		{
			name:              "local regtest",
			file:              "usdb-local-chain.json",
			wantChainID:       params.USDBNetworkID,
			wantBTCNetwork:    "btc-regtest",
			wantOriginHeight:  1,
			wantRegistryID:    internalusdb.BTCRegtestActivationRegistryIDV1,
			wantFeeSplitBlock: 256,
		},
		{
			name:              "regtest example",
			file:              "usdb-chain-bootstrap.example.json",
			wantChainID:       params.USDBNetworkID,
			wantBTCNetwork:    "btc-regtest",
			wantOriginHeight:  1,
			wantRegistryID:    internalusdb.BTCRegtestActivationRegistryIDV1,
			wantFeeSplitBlock: 256,
		},
		{
			name:              "BTC mainnet example",
			file:              "usdb-chain-bootstrap-btc-mainnet.example.json",
			wantChainID:       0,
			wantBTCNetwork:    "btc-mainnet",
			wantOriginHeight:  963_800,
			wantRegistryID:    internalusdb.BTCMainnetActivationRegistryIDV1,
			wantFeeSplitBlock: 8192,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join("..", "..", "tools", "config", test.file)
			blob, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to read bootstrap template %s: %v", path, err)
			}
			var config usdbGenesisBootstrapConfig
			if err := decodeStrictJSON(blob, &config); err != nil {
				t.Fatalf("failed to decode bootstrap template %s: %v", path, err)
			}
			if config.SchemaVersion != usdbGenesisBootstrapSchemaVersion || config.ChainID != test.wantChainID ||
				config.BTCSource.NetworkID != test.wantBTCNetwork || config.BTCSource.IndexOriginHeight != test.wantOriginHeight ||
				config.DividendFeeSplitBlock == nil || *config.DividendFeeSplitBlock != test.wantFeeSplitBlock {
				t.Fatalf("unexpected bootstrap template identity: %+v", config)
			}
			if len(config.USDBConsensus.Activations) != 1 ||
				config.USDBConsensus.Activations[0].BTCActivationRegistryID != test.wantRegistryID ||
				config.USDBConsensus.Activations[0].Versions.FeeSplitPolicyVersion != internalusdb.FeeSplitPolicyVersionV1 {
				t.Fatalf("unexpected bootstrap template activation: %+v", config.USDBConsensus.Activations)
			}
		})
	}
}

func TestUSDBBootstrapConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*usdbGenesisBootstrapConfig)
	}{
		{
			name: "unsupported schema",
			mutate: func(config *usdbGenesisBootstrapConfig) {
				config.SchemaVersion = 3
			},
		},
		{
			name: "zero chain ID",
			mutate: func(config *usdbGenesisBootstrapConfig) {
				config.ChainID = 0
			},
		},
		{
			name: "empty activation schedule",
			mutate: func(config *usdbGenesisBootstrapConfig) {
				config.USDBConsensus.Activations = nil
			},
		},
		{
			name: "activation schedule starts after genesis",
			mutate: func(config *usdbGenesisBootstrapConfig) {
				config.USDBConsensus.Activations[0].Block = 1
			},
		},
		{
			name: "unknown BTC registry",
			mutate: func(config *usdbGenesisBootstrapConfig) {
				config.USDBConsensus.Activations[0].BTCActivationRegistryID = strings.Repeat("f", 64)
			},
		},
		{
			name: "BTC registry network mismatch",
			mutate: func(config *usdbGenesisBootstrapConfig) {
				config.BTCSource.NetworkID = "btc-mainnet"
			},
		},
		{
			name: "zero Dao address",
			mutate: func(config *usdbGenesisBootstrapConfig) {
				config.Predeploys.Dao.Address = common.Address{}.Hex()
			},
		},
		{
			name: "non-canonical admin address",
			mutate: func(config *usdbGenesisBootstrapConfig) {
				config.BootstrapAdmin.Address = strings.ToLower(config.BootstrapAdmin.Address)
			},
		},
		{
			name: "duplicate predeploy address",
			mutate: func(config *usdbGenesisBootstrapConfig) {
				config.Predeploys.Dividend.Address = config.Predeploys.Dao.Address
			},
		},
		{
			name: "empty admin balance",
			mutate: func(config *usdbGenesisBootstrapConfig) {
				config.BootstrapAdmin.BalanceWei = ""
			},
		},
		{
			name: "minimum above genesis",
			mutate: func(config *usdbGenesisBootstrapConfig) {
				config.GenesisDifficulty = "0x1000"
				config.MinimumDifficulty = "0x2000"
			},
		},
		{
			name: "zero fee-split block",
			mutate: func(config *usdbGenesisBootstrapConfig) {
				value := uint64(0)
				config.DividendFeeSplitBlock = &value
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newUSDBBootstrapTestFixture(t)
			test.mutate(&fixture.config)
			writeUSDBBootstrapTestConfig(t, fixture.configPath, fixture.config)

			if _, err := loadUSDBBootstrapGenesis(fixture.configPath, fixture.artifactsRoot); err == nil {
				t.Fatal("invalid USDB genesis bootstrap config was accepted")
			}
		})
	}
}

func TestUSDBBootstrapConfigRejectsAmbiguousJSON(t *testing.T) {
	fixture := newUSDBBootstrapTestFixture(t)
	encoded, err := json.Marshal(fixture.config)
	if err != nil {
		t.Fatalf("failed to encode bootstrap config: %v", err)
	}
	base := string(encoded)
	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "legacy private key field",
			json: strings.Replace(
				base,
				"{",
				`{"bootstrapAdminPrivateKey":"secret",`,
				1,
			),
			want: "unknown field",
		},
		{
			name: "duplicate field",
			json: strings.Replace(
				base,
				`"chainId":42424242`,
				`"chainId":42424242,"chainId":42424242`,
				1,
			),
			want: "duplicate JSON field",
		},
		{
			name: "trailing value",
			json: base + `{}`,
			want: "trailing JSON",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(fixture.configPath, []byte(test.json), 0o600); err != nil {
				t.Fatalf("failed to write bootstrap config: %v", err)
			}
			_, err := loadUSDBBootstrapGenesis(fixture.configPath, fixture.artifactsRoot)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("unexpected error: have %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestUSDBBootstrapArtifactVerification(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *usdbBootstrapTestFixture)
		want   string
	}{
		{
			name: "runtime code hash mismatch",
			mutate: func(t *testing.T, fixture *usdbBootstrapTestFixture) {
				fixture.config.Predeploys.Dao.RuntimeCodeHash = common.HexToHash("0x01").Hex()
			},
			want: "runtime code hash mismatch",
		},
		{
			name: "artifact SHA mismatch",
			mutate: func(t *testing.T, fixture *usdbBootstrapTestFixture) {
				fixture.config.Predeploys.Dao.ArtifactSHA256 = strings.Repeat("0", sha256.Size*2)
			},
			want: "artifact SHA-256 mismatch",
		},
		{
			name: "malformed runtime code",
			mutate: func(t *testing.T, fixture *usdbBootstrapTestFixture) {
				blob := []byte(`{"deployedBytecode":"0x6000zz"}`)
				path := filepath.Join(fixture.artifactsRoot, filepath.FromSlash(testDaoArtifactPath))
				if err := os.WriteFile(path, blob, 0o600); err != nil {
					t.Fatalf("failed to write malformed artifact: %v", err)
				}
				sum := sha256.Sum256(blob)
				fixture.config.Predeploys.Dao.ArtifactSHA256 = hex.EncodeToString(sum[:])
			},
			want: "invalid deployedBytecode",
		},
		{
			name: "duplicate deployed bytecode",
			mutate: func(t *testing.T, fixture *usdbBootstrapTestFixture) {
				blob := []byte(`{"deployedBytecode":"0x6000","deployedBytecode":"0x6001"}`)
				path := filepath.Join(fixture.artifactsRoot, filepath.FromSlash(testDaoArtifactPath))
				if err := os.WriteFile(path, blob, 0o600); err != nil {
					t.Fatalf("failed to write duplicate-field artifact: %v", err)
				}
				sum := sha256.Sum256(blob)
				fixture.config.Predeploys.Dao.ArtifactSHA256 = hex.EncodeToString(sum[:])
			},
			want: "duplicate JSON field",
		},
		{
			name: "artifact path escape",
			mutate: func(t *testing.T, fixture *usdbBootstrapTestFixture) {
				fixture.config.Predeploys.Dao.Artifact = "../SourceDao.json"
			},
			want: "escapes the artifact root",
		},
		{
			name: "artifact symlink escape",
			mutate: func(t *testing.T, fixture *usdbBootstrapTestFixture) {
				original := filepath.Join(fixture.artifactsRoot, filepath.FromSlash(testDaoArtifactPath))
				blob, err := os.ReadFile(original)
				if err != nil {
					t.Fatalf("failed to read Dao artifact: %v", err)
				}
				outside := filepath.Join(filepath.Dir(fixture.artifactsRoot), "outside.json")
				if err := os.WriteFile(outside, blob, 0o600); err != nil {
					t.Fatalf("failed to write outside artifact: %v", err)
				}
				link := filepath.Join(filepath.Dir(original), "SourceDaoLink.json")
				if err := os.Symlink(outside, link); err != nil {
					t.Skipf("artifact symlinks are unavailable: %v", err)
				}
				fixture.config.Predeploys.Dao.Artifact = "contracts/Dao.sol/SourceDaoLink.json"
			},
			want: "escapes the artifact root",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newUSDBBootstrapTestFixture(t)
			test.mutate(t, &fixture)
			writeUSDBBootstrapTestConfig(t, fixture.configPath, fixture.config)

			_, err := loadUSDBBootstrapGenesis(fixture.configPath, fixture.artifactsRoot)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("unexpected error: have %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestParseCanonicalPositiveBigInt(t *testing.T) {
	for _, value := range []string{"1", "10000000000000000000", "0x1", "0x180000"} {
		if _, err := parseCanonicalPositiveBigInt("value", value); err != nil {
			t.Fatalf("valid numeric value %q was rejected: %v", value, err)
		}
	}
	for _, value := range []string{"", "0", "-1", "+1", "01", "0X1", "0x", "0x01", "0xAB"} {
		if _, err := parseCanonicalPositiveBigInt("value", value); err == nil {
			t.Fatalf("invalid numeric value %q was accepted", value)
		}
	}
}

func newUSDBBootstrapTestFixture(t *testing.T) usdbBootstrapTestFixture {
	t.Helper()

	root := t.TempDir()
	artifactsRoot := filepath.Join(root, "artifacts")
	configPath := filepath.Join(root, "config", "usdb-genesis-bootstrap.json")
	daoConfig, daoCode := writeUSDBBootstrapTestArtifact(t, artifactsRoot, testDaoArtifactPath, "0x600060005560206000f3")
	dividendConfig, dividendCode := writeUSDBBootstrapTestArtifact(t, artifactsRoot, testDividendArtifactPath, "0x600160005560206000f3")
	daoConfig.Address = "0x0000000000000000000000000000000000001001"
	dividendConfig.Address = "0x0000000000000000000000000000000000001002"
	feeSplitBlock := uint64(16)
	config := usdbGenesisBootstrapConfig{
		SchemaVersion: usdbGenesisBootstrapSchemaVersion,
		ChainID:       testUSDBBootstrapChainID,
		BTCSource: usdbGenesisBTCSource{
			NetworkID:         "btc-regtest",
			IndexOriginHeight: 1,
		},
		USDBConsensus: usdbGenesisConsensus{
			Activations: []params.USDBConsensusActivation{{
				Block:                   0,
				BTCActivationRegistryID: internalusdb.BTCRegtestActivationRegistryIDV1,
				BTCAnchorMaxAgeBlocks:   params.USDBDevelopmentBTCAnchorMaxAgeBlocks,
				Versions: params.USDBConsensusVersions{
					PayloadVersion:                       internalusdb.ProfileSelectorPayloadVersionV1,
					BTCAnchorPolicyVersion:               internalusdb.BTCAnchorPolicyVersionV1,
					DifficultyPolicyVersion:              internalusdb.DifficultyPolicyVersionV1,
					RewardRuleVersion:                    internalusdb.RewardRuleVersionV1,
					CoinbaseEmissionPolicyVersion:        internalusdb.CoinbaseEmissionPolicyVersionV1,
					FeeSplitPolicyVersion:                internalusdb.FeeSplitPolicyVersionV1,
					CollaborationEfficiencyPolicyVersion: internalusdb.CollaborationEfficiencyPolicyVersionV1,
					PricePolicyVersion:                   internalusdb.PricePolicyVersionV1,
				},
			}},
		},
		Predeploys: usdbGenesisPredeploys{
			Dao:      daoConfig,
			Dividend: dividendConfig,
		},
		BootstrapAdmin: usdbGenesisBootstrapAdmin{
			Address:    "0xabCd35AfbB4561213fEAfF01B5F91e18F8Df7c37",
			BalanceWei: "10000000000000000000",
		},
		GenesisDifficulty:     "0x180000",
		MinimumDifficulty:     "0x100000",
		DividendFeeSplitBlock: &feeSplitBlock,
	}
	writeUSDBBootstrapTestConfig(t, configPath, config)
	return usdbBootstrapTestFixture{
		configPath:    configPath,
		artifactsRoot: artifactsRoot,
		config:        config,
		daoCode:       daoCode,
		dividendCode:  dividendCode,
	}
}

func writeUSDBBootstrapTestArtifact(
	t *testing.T,
	artifactsRoot string,
	relativePath string,
	deployedBytecode string,
) (usdbGenesisSystemContract, []byte) {
	t.Helper()

	path := filepath.Join(artifactsRoot, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create artifact directory: %v", err)
	}
	blob, err := json.Marshal(hardhatRuntimeArtifact{DeployedBytecode: deployedBytecode})
	if err != nil {
		t.Fatalf("failed to encode artifact %s: %v", path, err)
	}
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatalf("failed to write artifact %s: %v", path, err)
	}
	code, err := hexutil.Decode(deployedBytecode)
	if err != nil {
		t.Fatalf("failed to decode test runtime code: %v", err)
	}
	sum := sha256.Sum256(blob)
	return usdbGenesisSystemContract{
		Artifact:        relativePath,
		RuntimeCodeHash: crypto.Keccak256Hash(code).Hex(),
		ArtifactSHA256:  hex.EncodeToString(sum[:]),
	}, code
}

func writeUSDBBootstrapTestConfig(t *testing.T, path string, config usdbGenesisBootstrapConfig) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create config directory: %v", err)
	}
	blob, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("failed to encode bootstrap config: %v", err)
	}
	blob = append(blob, '\n')
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatalf("failed to write bootstrap config: %v", err)
	}
}
