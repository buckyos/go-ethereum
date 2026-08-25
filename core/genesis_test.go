// Copyright 2017 The go-ethereum Authors
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

package core

import (
	"encoding/json"
	"math/big"
	"reflect"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/usdbstate"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/params"
)

func TestInvalidCliqueConfig(t *testing.T) {
	block := DefaultGoerliGenesisBlock()
	block.ExtraData = []byte{}
	if _, err := block.Commit(nil); err == nil {
		t.Fatal("Expected error on invalid clique config")
	}
}

func TestSetupGenesis(t *testing.T) {
	var (
		customghash = common.HexToHash("0x89c99d90b79719238d2645c7642f2c9295246e80775b38cfd162b696817fbd50")
		customg     = Genesis{
			Config: &params.ChainConfig{HomesteadBlock: big.NewInt(3)},
			Alloc: GenesisAlloc{
				{1}: {Balance: big.NewInt(1), Storage: map[common.Hash]common.Hash{{1}: {1}}},
			},
		}
		oldcustomg = customg
	)
	oldcustomg.Config = &params.ChainConfig{HomesteadBlock: big.NewInt(2)}
	tests := []struct {
		name       string
		fn         func(ethdb.Database) (*params.ChainConfig, common.Hash, error)
		wantConfig *params.ChainConfig
		wantHash   common.Hash
		wantErr    error
	}{
		{
			name: "genesis without ChainConfig",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
				return SetupGenesisBlock(db, new(Genesis))
			},
			wantErr:    errGenesisNoConfig,
			wantConfig: params.AllEthashProtocolChanges,
		},
		{
			name: "no block in DB, genesis == nil",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
				return SetupGenesisBlock(db, nil)
			},
			wantHash:   params.MainnetGenesisHash,
			wantConfig: params.MainnetChainConfig,
		},
		{
			name: "mainnet block in DB, genesis == nil",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
				DefaultGenesisBlock().MustCommit(db)
				return SetupGenesisBlock(db, nil)
			},
			wantHash:   params.MainnetGenesisHash,
			wantConfig: params.MainnetChainConfig,
		},
		{
			name: "custom block in DB, genesis == nil",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
				customg.MustCommit(db)
				return SetupGenesisBlock(db, nil)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
		},
		{
			name: "custom block in DB, genesis == ropsten",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
				customg.MustCommit(db)
				return SetupGenesisBlock(db, DefaultRopstenGenesisBlock())
			},
			wantErr:    &GenesisMismatchError{Stored: customghash, New: params.RopstenGenesisHash},
			wantHash:   params.RopstenGenesisHash,
			wantConfig: params.RopstenChainConfig,
		},
		{
			name: "compatible config in DB",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
				oldcustomg.MustCommit(db)
				return SetupGenesisBlock(db, &customg)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
		},
		{
			name: "incompatible config in DB",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
				// Commit the 'old' genesis block with Homestead transition at #2.
				// Advance to block #4, past the homestead transition block of customg.
				genesis := oldcustomg.MustCommit(db)

				bc, _ := NewBlockChain(db, nil, oldcustomg.Config, ethash.NewFullFaker(), vm.Config{}, nil, nil)
				defer bc.Stop()

				blocks, _ := GenerateChain(oldcustomg.Config, genesis, ethash.NewFaker(), db, 4, nil)
				bc.InsertChain(blocks)
				bc.CurrentBlock()
				// This should return a compatibility error.
				return SetupGenesisBlock(db, &customg)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
			wantErr: &params.ConfigCompatError{
				What:         "Homestead fork block",
				StoredConfig: big.NewInt(2),
				NewConfig:    big.NewInt(3),
				RewindTo:     1,
			},
		},
	}

	for _, test := range tests {
		db := rawdb.NewMemoryDatabase()
		config, hash, err := test.fn(db)
		// Check the return values.
		if !reflect.DeepEqual(err, test.wantErr) {
			spew := spew.ConfigState{DisablePointerAddresses: true, DisableCapacities: true}
			t.Errorf("%s: returned error %#v, want %#v", test.name, spew.NewFormatter(err), spew.NewFormatter(test.wantErr))
		}
		if !reflect.DeepEqual(config, test.wantConfig) {
			t.Errorf("%s:\nreturned %v\nwant     %v", test.name, config, test.wantConfig)
		}
		if hash != test.wantHash {
			t.Errorf("%s: returned hash %s, want %s", test.name, hash.Hex(), test.wantHash.Hex())
		} else if err == nil {
			// Check database content.
			stored := rawdb.ReadBlock(db, test.wantHash, 0)
			if stored.Hash() != test.wantHash {
				t.Errorf("%s: block in DB has hash %s, want %s", test.name, stored.Hash(), test.wantHash)
			}
		}
	}
}

// TestGenesisHashes checks the congruity of default genesis data to
// corresponding hardcoded genesis hash values.
func TestGenesisHashes(t *testing.T) {
	for i, c := range []struct {
		genesis *Genesis
		want    common.Hash
	}{
		{DefaultGenesisBlock(), params.MainnetGenesisHash},
		{DefaultGoerliGenesisBlock(), params.GoerliGenesisHash},
		{DefaultRopstenGenesisBlock(), params.RopstenGenesisHash},
		{DefaultRinkebyGenesisBlock(), params.RinkebyGenesisHash},
		{DefaultSepoliaGenesisBlock(), params.SepoliaGenesisHash},
		{DefaultUSDBGenesisBlock(), params.USDBGenesisHash},
	} {
		// Test via MustCommit
		if have := c.genesis.MustCommit(rawdb.NewMemoryDatabase()).Hash(); have != c.want {
			t.Errorf("case: %d a), want: %s, got: %s", i, c.want.Hex(), have.Hex())
		}
		// Test via ToBlock
		if have := c.genesis.ToBlock().Hash(); have != c.want {
			t.Errorf("case: %d a), want: %s, got: %s", i, c.want.Hex(), have.Hex())
		}
	}
}

func TestDefaultUSDBGenesisJSONRoundTrip(t *testing.T) {
	genesis := DefaultUSDBGenesisBlock()
	encoded, err := json.Marshal(genesis)
	if err != nil {
		t.Fatalf("failed to encode USDB genesis: %v", err)
	}
	var decoded Genesis
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("failed to decode USDB genesis: %v", err)
	}
	if decoded.Config == nil {
		t.Fatal("round-tripped USDB genesis lost chain config")
	}
	if !reflect.DeepEqual(decoded.Config.USDB, genesis.Config.USDB) {
		t.Fatalf("round-tripped USDB activation registry changed: have %+v want %+v", decoded.Config.USDB, genesis.Config.USDB)
	}
	versions, err := decoded.Config.USDBConsensusAt(0)
	if err != nil {
		t.Fatalf("failed to resolve round-tripped genesis activation: %v", err)
	}
	if versions == nil || versions.PayloadVersion != 1 || versions.DifficultyPolicyVersion != 1 ||
		versions.RewardRuleVersion != 1 || versions.CoinbaseEmissionPolicyVersion != 1 ||
		versions.FeeSplitPolicyVersion != 0 ||
		versions.CollaborationEfficiencyPolicyVersion != 1 || versions.PricePolicyVersion != 1 ||
		versions.QuotePolicyVersion != 0 || versions.AuxPoolPolicyVersion != 0 {
		t.Fatalf("round-tripped genesis returned unexpected USDB versions: %+v", versions)
	}
	if got, want := decoded.ToBlock().Hash(), genesis.ToBlock().Hash(); got != want {
		t.Fatalf("round-tripped genesis hash changed: have %s want %s", got, want)
	}
}

func TestDefaultUSDBGenesisSystemState(t *testing.T) {
	genesis := DefaultUSDBGenesisBlock()
	account, ok := genesis.Alloc[usdbstate.SystemStateAddress]
	if !ok {
		t.Fatal("default USDB genesis is missing the reserved system-state account")
	}
	if account.Balance == nil || account.Balance.Sign() != 0 {
		t.Fatalf("unexpected system-state balance: %v", account.Balance)
	}
	if account.Nonce != usdbstate.SystemStateNonce {
		t.Fatalf("unexpected system-state nonce: have %d want %d", account.Nonce, usdbstate.SystemStateNonce)
	}
	if len(account.Code) != 0 {
		t.Fatalf("system-state account must not contain EVM code: %x", account.Code)
	}
	if got := account.Storage[usdbstate.SystemStateSchemaVersionSlot]; got != common.BigToHash(big.NewInt(1)) {
		t.Fatalf("unexpected system-state schema version: %s", got)
	}
	if got := account.Storage[usdbstate.IssuedUSDBAtomsSlot]; got != (common.Hash{}) {
		t.Fatalf("empty built-in alloc must have zero issued supply: %s", got)
	}
	price := common.BigToHash(usdb.FixedPriceAtomsPerBTCV1())
	if got := account.Storage[usdbstate.PriceAtomsPerBTCSlot]; got != price {
		t.Fatalf("unexpected genesis price: have %s want %s", got, price)
	}
	if got := account.Storage[usdbstate.RealPriceAtomsPerBTCSlot]; got != price {
		t.Fatalf("unexpected genesis real price: have %s want %s", got, price)
	}
	if got := account.Storage[usdbstate.PricePolicyVersionSlot]; got != common.BigToHash(big.NewInt(1)) {
		t.Fatalf("unexpected genesis price policy: %s", got)
	}
	if got := account.Storage[usdbstate.PriceSourceKindSlot]; got != common.BigToHash(big.NewInt(1)) {
		t.Fatalf("unexpected genesis price source: %s", got)
	}
	rangeID, err := usdb.FixedPriceRangeIDV1(params.USDBChainConfig.ChainID, 0)
	if err != nil {
		t.Fatalf("failed to derive genesis price range: %v", err)
	}
	if got := account.Storage[usdbstate.PricePolicyRangeIDSlot]; got != rangeID {
		t.Fatalf("unexpected genesis price range: have %s want %s", got, rangeID)
	}
}

func TestInitializeUSDBGenesisSystemStateRejectsInvalidInputs(t *testing.T) {
	fundedAddress := common.HexToAddress("0x0000000000000000000000000000000000002000")
	tests := []struct {
		name  string
		alloc GenesisAlloc
	}{
		{
			name: "nil allocation balance",
			alloc: GenesisAlloc{
				fundedAddress: {Balance: nil},
			},
		},
		{
			name: "negative allocation balance",
			alloc: GenesisAlloc{
				fundedAddress: {Balance: big.NewInt(-1)},
			},
		},
		{
			name: "issued supply overflow",
			alloc: GenesisAlloc{
				fundedAddress: {Balance: new(big.Int).Lsh(big.NewInt(1), 256)},
			},
		},
		{
			name: "reserved account conflict",
			alloc: GenesisAlloc{
				usdbstate.SystemStateAddress: {
					Balance: big.NewInt(1),
					Nonce:   usdbstate.SystemStateNonce,
				},
			},
		},
		{
			name: "incompatible system-state schema",
			alloc: GenesisAlloc{
				usdbstate.SystemStateAddress: {
					Balance: big.NewInt(0),
					Nonce:   usdbstate.SystemStateNonce,
					Storage: map[common.Hash]common.Hash{
						usdbstate.SystemStateSchemaVersionSlot: common.BigToHash(big.NewInt(2)),
					},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := initializeUSDBGenesisSystemState(test.alloc, params.USDBChainConfig); err == nil {
				t.Fatal("invalid USDB genesis system state was accepted")
			}
		})
	}
}

func TestGenesis_Commit(t *testing.T) {
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.TestChainConfig,
		// difficulty is nil
	}

	db := rawdb.NewMemoryDatabase()
	genesisBlock := genesis.MustCommit(db)
	if genesis.Difficulty != nil {
		t.Fatalf("assumption wrong")
	}

	// This value should have been set as default in the ToBlock method.
	if genesisBlock.Difficulty().Cmp(params.GenesisDifficulty) != 0 {
		t.Errorf("assumption wrong: want: %d, got: %v", params.GenesisDifficulty, genesisBlock.Difficulty())
	}

	// Expect the stored total difficulty to be the difficulty of the genesis block.
	stored := rawdb.ReadTd(db, genesisBlock.Hash(), genesisBlock.NumberU64())

	if stored.Cmp(genesisBlock.Difficulty()) != 0 {
		t.Errorf("inequal difficulty; stored: %v, genesisBlock: %v", stored, genesisBlock.Difficulty())
	}
}

func TestDefaultUSDBGenesisBlockWithBootstrap(t *testing.T) {
	daoAddr := common.HexToAddress("0x0000000000000000000000000000000000001001")
	dividendAddr := common.HexToAddress("0x0000000000000000000000000000000000001002")
	adminAddr := common.HexToAddress("0x0000000000000000000000000000000000001003")
	daoCode := []byte{0x60, 0x00, 0x60, 0x00}
	dividendCode := []byte{0x60, 0x01, 0x60, 0x00}
	adminBalance := big.NewInt(123456789)
	activationBlock := big.NewInt(16)
	genesisDifficulty := big.NewInt(0x40000)
	minimumDifficulty := big.NewInt(0x20000)

	opts := validUSDBBootstrapGenesisConfig()
	opts.DaoAddress = daoAddr
	opts.DaoCode = daoCode
	opts.DividendAddress = dividendAddr
	opts.DividendCode = dividendCode
	opts.BootstrapAdmin = adminAddr
	opts.BootstrapAdminBalance = adminBalance
	opts.DividendFeeSplitBlock = activationBlock
	opts.GenesisDifficulty = genesisDifficulty
	opts.MinimumDifficulty = minimumDifficulty
	genesis, err := DefaultUSDBGenesisBlockWithBootstrap(opts)
	if err != nil {
		t.Fatalf("failed to build USDB bootstrap genesis: %v", err)
	}

	if got := genesis.Config.DividendAddress; got != dividendAddr {
		t.Fatalf("unexpected dividend address: %s", got)
	}
	if got := genesis.Config.ChainID; got == nil || got.Cmp(opts.ChainID) != 0 {
		t.Fatalf("unexpected configurable chain ID: %v", got)
	}
	if got := genesis.Config.ChainID_ALT; got == nil || got.Cmp(opts.ChainID) != 0 {
		t.Fatalf("unexpected alternate chain ID: %v", got)
	}
	if got := genesis.Config.DividendFeeSplitBlock; got == nil || got.Cmp(activationBlock) != 0 {
		t.Fatalf("unexpected dividend activation block: %v", got)
	}
	if got, want := genesis.Config.DividendCodeHash, crypto.Keccak256Hash(dividendCode); got != want {
		t.Fatalf("unexpected dividend runtime code hash: have %s want %s", got, want)
	}
	if got := genesis.Config.USDB.Activations[0].Versions.FeeSplitPolicyVersion; got != 1 {
		t.Fatalf("bootstrap genesis did not activate fee policy v1: %d", got)
	}
	if got := genesis.Alloc[daoAddr].Code; !reflect.DeepEqual(got, daoCode) {
		t.Fatalf("unexpected dao code: %x", got)
	}
	if got := genesis.Alloc[dividendAddr].Code; !reflect.DeepEqual(got, dividendCode) {
		t.Fatalf("unexpected dividend code: %x", got)
	}
	if got := genesis.Alloc[adminAddr].Balance; got == nil || got.Cmp(adminBalance) != 0 {
		t.Fatalf("unexpected bootstrap admin balance: %v", got)
	}
	if got := genesis.Difficulty; got == nil || got.Cmp(genesisDifficulty) != 0 {
		t.Fatalf("unexpected genesis difficulty: %v", got)
	}
	if got := genesis.Config.EthPoWMinimumDifficulty(); got.Cmp(minimumDifficulty) != 0 {
		t.Fatalf("unexpected minimum difficulty override: %v", got)
	}
	systemAccount := genesis.Alloc[usdbstate.SystemStateAddress]
	issuedWord := systemAccount.Storage[usdbstate.IssuedUSDBAtomsSlot]
	if got := new(big.Int).SetBytes(issuedWord[:]); got.Cmp(adminBalance) != 0 {
		t.Fatalf("unexpected bootstrap issued supply: have %v want %v", got, adminBalance)
	}
	if DefaultUSDBGenesisBlock().ToBlock().Hash() == genesis.ToBlock().Hash() {
		t.Fatalf("bootstrap overlay must produce a distinct development genesis hash")
	}
}

func TestUSDBBootstrapGenesisPreservesAndClonesBaseState(t *testing.T) {
	base := DefaultUSDBGenesisBlock()
	configJSON, err := json.Marshal(base.Config)
	if err != nil {
		t.Fatalf("failed to encode base chain config: %v", err)
	}
	var baseConfig params.ChainConfig
	if err := json.Unmarshal(configJSON, &baseConfig); err != nil {
		t.Fatalf("failed to clone base chain config: %v", err)
	}
	base.Config = &baseConfig
	existingAddress := common.HexToAddress("0x0000000000000000000000000000000000002001")
	existingCode := []byte{0x60, 0x02}
	existingBalance := big.NewInt(99)
	existingStorageKey := common.HexToHash("0x01")
	existingStorageValue := common.HexToHash("0x02")
	base.Alloc[existingAddress] = GenesisAccount{
		Code:    existingCode,
		Balance: existingBalance,
		Storage: map[common.Hash]common.Hash{existingStorageKey: existingStorageValue},
	}
	systemAccount := base.Alloc[usdbstate.SystemStateAddress]
	systemAccount.Storage[usdbstate.PricePolicyVersionSlot] = common.BigToHash(big.NewInt(1))
	base.Alloc[usdbstate.SystemStateAddress] = systemAccount
	opts := validUSDBBootstrapGenesisConfig()

	genesis, err := buildUSDBGenesisBlockWithBootstrap(base, opts)
	if err != nil {
		t.Fatalf("failed to build USDB bootstrap genesis: %v", err)
	}
	existing := genesis.Alloc[existingAddress]
	if !reflect.DeepEqual(existing.Code, existingCode) {
		t.Fatalf("base alloc code was not preserved: %x", existing.Code)
	}
	if existing.Balance == nil || existing.Balance.Cmp(existingBalance) != 0 {
		t.Fatalf("base alloc balance was not preserved: %v", existing.Balance)
	}
	if existing.Storage[existingStorageKey] != existingStorageValue {
		t.Fatalf("base alloc storage was not preserved: %v", existing.Storage)
	}
	systemAccount = genesis.Alloc[usdbstate.SystemStateAddress]
	if got := systemAccount.Storage[usdbstate.PricePolicyVersionSlot]; got != common.BigToHash(big.NewInt(1)) {
		t.Fatalf("system policy storage was not preserved: %s", got)
	}
	issuedWord := systemAccount.Storage[usdbstate.IssuedUSDBAtomsSlot]
	wantIssued := new(big.Int).Add(big.NewInt(99), big.NewInt(123456789))
	if got := new(big.Int).SetBytes(issuedWord[:]); got.Cmp(wantIssued) != 0 {
		t.Fatalf("system issued supply was not recalculated: have %v want %v", got, wantIssued)
	}

	existingCode[0] = 0xff
	existingBalance.SetInt64(1)
	opts.DaoCode[0] = 0xfe
	opts.BootstrapAdminBalance.SetInt64(1)
	opts.USDBConsensus.Activations[0].Versions.PayloadVersion = 2
	base.Config.USDB.Activations[0].Versions.PayloadVersion = 2

	if got := genesis.Alloc[existingAddress].Code[0]; got != 0x60 {
		t.Fatalf("cloned base alloc code changed through input alias: %x", got)
	}
	if got := genesis.Alloc[existingAddress].Balance; got.Cmp(big.NewInt(99)) != 0 {
		t.Fatalf("cloned base alloc balance changed through input alias: %v", got)
	}
	if got := genesis.Alloc[common.HexToAddress("0x0000000000000000000000000000000000001001")].Code[0]; got != 0x60 {
		t.Fatalf("cloned Dao code changed through options alias: %x", got)
	}
	if got := genesis.Alloc[common.HexToAddress("0x0000000000000000000000000000000000001003")].Balance; got.Cmp(big.NewInt(123456789)) != 0 {
		t.Fatalf("cloned bootstrap balance changed through options alias: %v", got)
	}
	if got := genesis.Config.USDB.Activations[0].Versions.PayloadVersion; got != 1 {
		t.Fatalf("cloned chain config changed through base alias: %d", got)
	}
}

func TestUSDBBootstrapGenesisRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*USDBBootstrapGenesisConfig)
	}{
		{
			name: "zero chain ID",
			mutate: func(config *USDBBootstrapGenesisConfig) {
				config.ChainID = big.NewInt(0)
			},
		},
		{
			name: "empty consensus schedule",
			mutate: func(config *USDBBootstrapGenesisConfig) {
				config.USDBConsensus.Activations = nil
			},
		},
		{
			name: "BTC registry network mismatch",
			mutate: func(config *USDBBootstrapGenesisConfig) {
				config.USDBConsensus.BTCNetworkID = "btc-mainnet"
			},
		},
		{
			name: "fee policy disabled",
			mutate: func(config *USDBBootstrapGenesisConfig) {
				config.USDBConsensus.Activations[0].Versions.FeeSplitPolicyVersion = 0
			},
		},
		{
			name: "zero Dao address",
			mutate: func(config *USDBBootstrapGenesisConfig) {
				config.DaoAddress = common.Address{}
			},
		},
		{
			name: "empty Dividend code",
			mutate: func(config *USDBBootstrapGenesisConfig) {
				config.DividendCode = nil
			},
		},
		{
			name: "oversized Dao code",
			mutate: func(config *USDBBootstrapGenesisConfig) {
				config.DaoCode = make([]byte, params.MaxCodeSize+1)
			},
		},
		{
			name: "duplicate system addresses",
			mutate: func(config *USDBBootstrapGenesisConfig) {
				config.DividendAddress = config.DaoAddress
			},
		},
		{
			name: "non-positive admin balance",
			mutate: func(config *USDBBootstrapGenesisConfig) {
				config.BootstrapAdminBalance = big.NewInt(0)
			},
		},
		{
			name: "zero fee-split block",
			mutate: func(config *USDBBootstrapGenesisConfig) {
				config.DividendFeeSplitBlock = big.NewInt(0)
			},
		},
		{
			name: "partial difficulty override",
			mutate: func(config *USDBBootstrapGenesisConfig) {
				config.MinimumDifficulty = nil
			},
		},
		{
			name: "genesis difficulty below minimum",
			mutate: func(config *USDBBootstrapGenesisConfig) {
				config.GenesisDifficulty = big.NewInt(99)
				config.MinimumDifficulty = big.NewInt(100)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validUSDBBootstrapGenesisConfig()
			test.mutate(&config)
			if _, err := DefaultUSDBGenesisBlockWithBootstrap(config); err == nil {
				t.Fatal("invalid USDB bootstrap config was accepted")
			}
		})
	}
}

func TestUSDBBootstrapGenesisRejectsBaseAllocConflict(t *testing.T) {
	base := DefaultUSDBGenesisBlock()
	config := validUSDBBootstrapGenesisConfig()
	base.Alloc[config.DaoAddress] = GenesisAccount{Balance: big.NewInt(1)}

	if _, err := buildUSDBGenesisBlockWithBootstrap(base, config); err == nil {
		t.Fatal("USDB bootstrap address conflicting with base alloc was accepted")
	}
}

func validUSDBBootstrapGenesisConfig() USDBBootstrapGenesisConfig {
	consensus := cloneUSDBConsensusConfig(params.USDBChainConfig.USDB)
	for i := range consensus.Activations {
		consensus.Activations[i].Versions.FeeSplitPolicyVersion = usdb.FeeSplitPolicyVersionV1
	}
	return USDBBootstrapGenesisConfig{
		ChainID:               big.NewInt(42_424_242),
		USDBConsensus:         consensus,
		DaoAddress:            common.HexToAddress("0x0000000000000000000000000000000000001001"),
		DaoCode:               []byte{0x60, 0x00, 0x60, 0x00},
		DividendAddress:       common.HexToAddress("0x0000000000000000000000000000000000001002"),
		DividendCode:          []byte{0x60, 0x01, 0x60, 0x00},
		BootstrapAdmin:        common.HexToAddress("0x0000000000000000000000000000000000001003"),
		BootstrapAdminBalance: big.NewInt(123456789),
		DividendFeeSplitBlock: big.NewInt(16),
		GenesisDifficulty:     big.NewInt(0x40000),
		MinimumDifficulty:     big.NewInt(0x20000),
	}
}

func TestReadWriteGenesisAlloc(t *testing.T) {
	var (
		db    = rawdb.NewMemoryDatabase()
		alloc = &GenesisAlloc{
			{1}: {Balance: big.NewInt(1), Storage: map[common.Hash]common.Hash{{1}: {1}}},
			{2}: {Balance: big.NewInt(2), Storage: map[common.Hash]common.Hash{{2}: {2}}},
		}
		hash, _ = alloc.deriveHash()
	)
	alloc.flush(db)

	var reload GenesisAlloc
	err := reload.UnmarshalJSON(rawdb.ReadGenesisStateSpec(db, hash))
	if err != nil {
		t.Fatalf("Failed to load genesis state %v", err)
	}
	if len(reload) != len(*alloc) {
		t.Fatal("Unexpected genesis allocation")
	}
	for addr, account := range reload {
		want, ok := (*alloc)[addr]
		if !ok {
			t.Fatal("Account is not found")
		}
		if !reflect.DeepEqual(want, account) {
			t.Fatal("Unexpected account")
		}
	}
}
