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

package params

import (
	"encoding/json"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

const testBTCActivationRegistryID = "22d820e6ec242b61f63473f279c41a4103af5cff13206b1925fd415cceaaf83d"

func TestCheckCompatible(t *testing.T) {
	type test struct {
		stored, new *ChainConfig
		head        uint64
		wantErr     *ConfigCompatError
	}
	tests := []test{
		{stored: AllEthashProtocolChanges, new: AllEthashProtocolChanges, head: 0, wantErr: nil},
		{stored: AllEthashProtocolChanges, new: AllEthashProtocolChanges, head: 100, wantErr: nil},
		{
			stored:  &ChainConfig{EIP150Block: big.NewInt(10)},
			new:     &ChainConfig{EIP150Block: big.NewInt(20)},
			head:    9,
			wantErr: nil,
		},
		{
			stored: AllEthashProtocolChanges,
			new:    &ChainConfig{HomesteadBlock: nil},
			head:   3,
			wantErr: &ConfigCompatError{
				What:         "Homestead fork block",
				StoredConfig: big.NewInt(0),
				NewConfig:    nil,
				RewindTo:     0,
			},
		},
		{
			stored: AllEthashProtocolChanges,
			new:    &ChainConfig{HomesteadBlock: big.NewInt(1)},
			head:   3,
			wantErr: &ConfigCompatError{
				What:         "Homestead fork block",
				StoredConfig: big.NewInt(0),
				NewConfig:    big.NewInt(1),
				RewindTo:     0,
			},
		},
		{
			stored: &ChainConfig{HomesteadBlock: big.NewInt(30), EIP150Block: big.NewInt(10)},
			new:    &ChainConfig{HomesteadBlock: big.NewInt(25), EIP150Block: big.NewInt(20)},
			head:   25,
			wantErr: &ConfigCompatError{
				What:         "EIP150 fork block",
				StoredConfig: big.NewInt(10),
				NewConfig:    big.NewInt(20),
				RewindTo:     9,
			},
		},
		{
			stored:  &ChainConfig{ConstantinopleBlock: big.NewInt(30)},
			new:     &ChainConfig{ConstantinopleBlock: big.NewInt(30), PetersburgBlock: big.NewInt(30)},
			head:    40,
			wantErr: nil,
		},
		{
			stored: &ChainConfig{ConstantinopleBlock: big.NewInt(30)},
			new:    &ChainConfig{ConstantinopleBlock: big.NewInt(30), PetersburgBlock: big.NewInt(31)},
			head:   40,
			wantErr: &ConfigCompatError{
				What:         "Petersburg fork block",
				StoredConfig: nil,
				NewConfig:    big.NewInt(31),
				RewindTo:     30,
			},
		},
	}

	for _, test := range tests {
		err := test.stored.CheckCompatible(test.new, test.head)
		if !reflect.DeepEqual(err, test.wantErr) {
			t.Errorf("error mismatch:\nstored: %v\nnew: %v\nhead: %v\nerr: %v\nwant: %v", test.stored, test.new, test.head, err, test.wantErr)
		}
	}
}

func TestEffectiveChainIDUsesLegacyAltChainID(t *testing.T) {
	cfg := &ChainConfig{
		ChainID:           big.NewInt(1),
		EthPoWForkBlock:   big.NewInt(100),
		EthPoWForkSupport: true,
		ChainID_ALT:       big.NewInt(10001),
	}
	if got := cfg.EffectiveChainID(big.NewInt(99)); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("unexpected pre-fork chain id: %v", got)
	}
	if got := cfg.EffectiveChainID(big.NewInt(100)); got.Cmp(big.NewInt(10001)) != 0 {
		t.Fatalf("unexpected post-fork chain id: %v", got)
	}
	if got := cfg.DefaultNetworkID(); got != 10001 {
		t.Fatalf("unexpected default network id: %d", got)
	}
}

func TestEffectiveChainIDKeepsSingleUSDBChainID(t *testing.T) {
	cfg := USDBChainConfig
	if got := cfg.EffectiveChainID(big.NewInt(0)); got.Cmp(cfg.ChainID) != 0 {
		t.Fatalf("unexpected genesis chain id: %v", got)
	}
	if got := cfg.EffectiveChainID(big.NewInt(100)); got.Cmp(cfg.ChainID) != 0 {
		t.Fatalf("unexpected ongoing chain id: %v", got)
	}
	if got := cfg.DefaultNetworkID(); got != USDBNetworkID {
		t.Fatalf("unexpected default network id: %d", got)
	}
	if cfg.HasEthPoWChainIDSwitch() {
		t.Fatalf("usdb chain must not inherit the legacy alt chain id switch")
	}
}

func TestHasMergeTransition(t *testing.T) {
	if USDBChainConfig.HasMergeTransition() {
		t.Fatalf("usdb chain must stay on the pure pow path")
	}
	if !SepoliaChainConfig.HasMergeTransition() {
		t.Fatalf("sepolia must advertise merge transition support")
	}
	cfg := &ChainConfig{TerminalTotalDifficultyPassed: true}
	if !cfg.HasMergeTransition() {
		t.Fatalf("post-merge debug config must still report merge transition support")
	}
}

func TestUSDBConsensusAtUsesChainConfigVersions(t *testing.T) {
	if (&ChainConfig{}).HasUSDBConsensus() {
		t.Fatal("empty chain config must not activate USDB consensus")
	}
	if policy, err := (&ChainConfig{}).USDBConsensusAt(7); err != nil || policy != nil {
		t.Fatalf("inactive config returned policy=%+v err=%v", policy, err)
	}
	policy, err := USDBChainConfig.USDBConsensusAt(7)
	if err != nil {
		t.Fatalf("failed to resolve built-in USDB policy: %v", err)
	}
	if policy == nil || policy.PayloadVersion != 1 || policy.BTCAnchorPolicyVersion != 1 ||
		policy.DifficultyPolicyVersion != 1 ||
		policy.RewardRuleVersion != 1 || policy.CoinbaseEmissionPolicyVersion != 1 ||
		policy.FeeSplitPolicyVersion != 0 ||
		policy.CollaborationEfficiencyPolicyVersion != 1 || policy.PricePolicyVersion != 1 ||
		policy.QuotePolicyVersion != 0 || policy.AuxPoolPolicyVersion != 0 {
		t.Fatalf("unexpected built-in USDB policy: %+v", policy)
	}
	policy.DifficultyPolicyVersion = 9
	if USDBChainConfig.USDB.Activations[0].Versions.DifficultyPolicyVersion != 1 {
		t.Fatal("policy lookup must return a copy of chain config state")
	}

	for _, config := range []*ChainConfig{
		{USDB: &USDBConsensusConfig{}},
		{USDB: &USDBConsensusConfig{Activations: []USDBConsensusActivation{{BTCActivationRegistryID: strings.Repeat("A", 64), Versions: testUSDBConsensusVersions(1)}}}},
		{USDB: &USDBConsensusConfig{Activations: []USDBConsensusActivation{{BTCActivationRegistryID: testBTCActivationRegistryID, Versions: USDBConsensusVersions{DifficultyPolicyVersion: 1}}}}},
		{USDB: &USDBConsensusConfig{Activations: []USDBConsensusActivation{{BTCActivationRegistryID: testBTCActivationRegistryID, Versions: USDBConsensusVersions{PayloadVersion: 1}}}}},
		{USDB: &USDBConsensusConfig{Activations: []USDBConsensusActivation{{BTCActivationRegistryID: testBTCActivationRegistryID, BTCAnchorMaxAgeBlocks: 10, Versions: USDBConsensusVersions{PayloadVersion: 1, DifficultyPolicyVersion: 1}}}}},
		{USDB: &USDBConsensusConfig{Activations: []USDBConsensusActivation{{BTCActivationRegistryID: testBTCActivationRegistryID, Versions: USDBConsensusVersions{PayloadVersion: 1, BTCAnchorPolicyVersion: 1, DifficultyPolicyVersion: 1}}}}},
		{USDB: &USDBConsensusConfig{Activations: []USDBConsensusActivation{
			testUSDBActivation(7, 1),
			testUSDBActivation(7, 2),
		}}},
	} {
		if _, err := config.USDBConsensusAt(7); err == nil {
			t.Fatalf("expected invalid USDB policy to fail: %+v", config.USDB)
		}
	}
}

func TestUSDBConsensusAtActivationBoundary(t *testing.T) {
	config := &ChainConfig{USDB: &USDBConsensusConfig{Activations: []USDBConsensusActivation{
		testUSDBActivation(10, 1),
		testUSDBActivation(20, 2),
	}}}
	config.USDB.Activations[1].BTCActivationRegistryID = strings.Repeat("a", 64)
	tests := []struct {
		block        uint64
		wantVersion  uint16
		wantRegistry string
	}{
		{block: 9, wantVersion: 0},
		{block: 10, wantVersion: 1, wantRegistry: testBTCActivationRegistryID},
		{block: 19, wantVersion: 1, wantRegistry: testBTCActivationRegistryID},
		{block: 20, wantVersion: 2, wantRegistry: strings.Repeat("a", 64)},
		{block: 21, wantVersion: 2, wantRegistry: strings.Repeat("a", 64)},
	}
	for _, test := range tests {
		activation, err := config.USDBActivationAt(test.block)
		if err != nil {
			t.Fatalf("block %d lookup failed: %v", test.block, err)
		}
		if test.wantVersion == 0 {
			if activation != nil {
				t.Fatalf("block %d activated unexpectedly: %+v", test.block, activation)
			}
			continue
		}
		if activation == nil || activation.Versions.DifficultyPolicyVersion != test.wantVersion || activation.BTCActivationRegistryID != test.wantRegistry {
			t.Fatalf("block %d returned %+v, want difficulty version %d registry %s", test.block, activation, test.wantVersion, test.wantRegistry)
		}
	}
}

func TestUSDBConsensusConfigJSONRoundTrip(t *testing.T) {
	encoded, err := json.Marshal(USDBChainConfig)
	if err != nil {
		t.Fatalf("failed to encode USDB chain config: %v", err)
	}
	for _, field := range []string{
		`"btcActivationRegistryId":"` + testBTCActivationRegistryID + `"`,
		`"btcAnchorMaxAgeBlocks":6650`,
		`"activations"`,
		`"btcAnchorPolicyVersion":1`,
		`"rewardRuleVersion":1`,
		`"coinbaseEmissionPolicyVersion":1`,
		`"feeSplitPolicyVersion":0`,
		`"collaborationEfficiencyPolicyVersion":1`,
		`"pricePolicyVersion":1`,
		`"quotePolicyVersion":0`,
		`"auxPoolPolicyVersion":0`,
	} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("encoded USDB chain config is missing %s: %s", field, encoded)
		}
	}
	var decoded ChainConfig
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("failed to decode USDB chain config: %v", err)
	}
	policy, err := decoded.USDBConsensusAt(19)
	if err != nil {
		t.Fatalf("failed to resolve round-tripped USDB policy: %v", err)
	}
	if policy == nil || policy.PayloadVersion != 1 || policy.BTCAnchorPolicyVersion != 1 || policy.DifficultyPolicyVersion != 1 {
		t.Fatalf("unexpected round-tripped USDB policy: %+v", policy)
	}
	activation, err := decoded.USDBActivationAt(19)
	if err != nil || activation == nil ||
		activation.BTCActivationRegistryID != testBTCActivationRegistryID ||
		activation.BTCAnchorMaxAgeBlocks != USDBDevelopmentBTCAnchorMaxAgeBlocks {
		t.Fatalf("round-tripped USDB activation checkpoint changed: activation=%+v err=%v", activation, err)
	}
	if banner := decoded.String(); !strings.Contains(banner, "USDB consensus: payload v1, BTC anchor policy v1/6650 blocks, difficulty policy v1 from block 0 (1 activation(s))") {
		t.Fatalf("USDB consensus versions missing from chain banner:\n%s", banner)
	}
}

func TestUSDBConsensusCheckCompatible(t *testing.T) {
	stored := &ChainConfig{USDB: &USDBConsensusConfig{Activations: []USDBConsensusActivation{
		testUSDBActivation(0, 1),
		testUSDBActivation(100, 2),
	}}}
	changedFuture := &ChainConfig{USDB: &USDBConsensusConfig{Activations: []USDBConsensusActivation{
		testUSDBActivation(0, 1),
		testUSDBActivation(100, 3),
	}}}
	if err := stored.CheckCompatible(changedFuture, 99); err != nil {
		t.Fatalf("future activation change must remain compatible: %v", err)
	}
	if err := stored.CheckCompatible(changedFuture, 100); err == nil || err.What != "USDB activation checkpoint versions" || err.RewindTo != 99 {
		t.Fatalf("active version change returned unexpected compatibility result: %+v", err)
	}

	changedReward := &ChainConfig{USDB: &USDBConsensusConfig{Activations: []USDBConsensusActivation{
		testUSDBActivation(0, 1),
		testUSDBActivation(100, 2),
	}}}
	changedReward.USDB.Activations[1].Versions.RewardRuleVersion = 2
	if err := stored.CheckCompatible(changedReward, 100); err == nil || err.RewindTo != 99 {
		t.Fatalf("active reward-version change returned unexpected compatibility result: %+v", err)
	}

	shiftedFuture := &ChainConfig{USDB: &USDBConsensusConfig{Activations: []USDBConsensusActivation{
		testUSDBActivation(0, 1),
		testUSDBActivation(120, 2),
	}}}
	if err := stored.CheckCompatible(shiftedFuture, 110); err == nil || err.RewindTo != 99 {
		t.Fatalf("shifted active boundary returned unexpected compatibility result: %+v", err)
	}

	changedGenesis := &ChainConfig{USDB: &USDBConsensusConfig{Activations: []USDBConsensusActivation{
		testUSDBActivation(0, 2),
	}}}
	if err := stored.CheckCompatible(changedGenesis, 0); err == nil || err.RewindTo != 0 {
		t.Fatalf("genesis version change returned unexpected compatibility result: %+v", err)
	}

	changedRegistry := &ChainConfig{USDB: &USDBConsensusConfig{
		Activations: []USDBConsensusActivation{
			testUSDBActivation(0, 1),
			testUSDBActivation(100, 2),
		},
	}}
	changedRegistry.USDB.Activations[0].BTCActivationRegistryID = strings.Repeat("a", 64)
	if err := stored.CheckCompatible(changedRegistry, 0); err == nil || err.What != "USDB activation checkpoint BTC registry binding" || err.RewindTo != 0 {
		t.Fatalf("genesis registry change returned unexpected compatibility result: %+v", err)
	}

	changedAnchorAge := &ChainConfig{USDB: &USDBConsensusConfig{
		Activations: []USDBConsensusActivation{
			testUSDBActivation(0, 1),
			testUSDBActivation(100, 2),
		},
	}}
	changedAnchorAge.USDB.Activations[1].BTCAnchorMaxAgeBlocks++
	if err := stored.CheckCompatible(changedAnchorAge, 99); err != nil {
		t.Fatalf("future anchor max-age change must remain compatible: %v", err)
	}
	if err := stored.CheckCompatible(changedAnchorAge, 100); err == nil || err.What != "USDB activation checkpoint BTC anchor max age" || err.RewindTo != 99 {
		t.Fatalf("active anchor max-age change returned unexpected compatibility result: %+v", err)
	}

	changedAnchorPolicy := &ChainConfig{USDB: &USDBConsensusConfig{
		Activations: []USDBConsensusActivation{
			testUSDBActivation(0, 1),
			testUSDBActivation(100, 2),
		},
	}}
	changedAnchorPolicy.USDB.Activations[1].Versions.BTCAnchorPolicyVersion++
	if err := stored.CheckCompatible(changedAnchorPolicy, 100); err == nil || err.What != "USDB activation checkpoint versions" || err.RewindTo != 99 {
		t.Fatalf("active anchor-policy change returned unexpected compatibility result: %+v", err)
	}

	futureRegistry := &ChainConfig{USDB: &USDBConsensusConfig{
		Activations: []USDBConsensusActivation{{
			Block:                   100,
			BTCActivationRegistryID: strings.Repeat("b", 64),
			BTCAnchorMaxAgeBlocks:   USDBDevelopmentBTCAnchorMaxAgeBlocks,
			Versions:                testUSDBConsensusVersions(1),
		}},
	}}
	futureRegistryChanged := &ChainConfig{USDB: &USDBConsensusConfig{
		Activations: []USDBConsensusActivation{{
			Block:                   100,
			BTCActivationRegistryID: strings.Repeat("c", 64),
			BTCAnchorMaxAgeBlocks:   USDBDevelopmentBTCAnchorMaxAgeBlocks,
			Versions:                testUSDBConsensusVersions(1),
		}},
	}}
	if err := futureRegistry.CheckCompatible(futureRegistryChanged, 99); err != nil {
		t.Fatalf("future registry binding change must remain compatible: %v", err)
	}
	if err := futureRegistry.CheckCompatible(futureRegistryChanged, 100); err == nil || err.What != "USDB activation checkpoint BTC registry binding" || err.RewindTo != 99 {
		t.Fatalf("active future registry change returned unexpected compatibility result: %+v", err)
	}

	registryUpgrade := &ChainConfig{USDB: &USDBConsensusConfig{Activations: []USDBConsensusActivation{
		testUSDBActivation(0, 1),
		testUSDBActivation(100, 1),
	}}}
	registryUpgrade.USDB.Activations[1].BTCActivationRegistryID = strings.Repeat("b", 64)
	changedRegistryUpgrade := &ChainConfig{USDB: &USDBConsensusConfig{Activations: append(
		[]USDBConsensusActivation(nil),
		registryUpgrade.USDB.Activations...,
	)}}
	changedRegistryUpgrade.USDB.Activations[1].BTCActivationRegistryID = strings.Repeat("c", 64)
	if err := registryUpgrade.CheckCompatible(changedRegistryUpgrade, 99); err != nil {
		t.Fatalf("future registry revision change must remain compatible: %v", err)
	}
	if err := registryUpgrade.CheckCompatible(changedRegistryUpgrade, 100); err == nil || err.What != "USDB activation checkpoint BTC registry binding" || err.RewindTo != 99 {
		t.Fatalf("activated registry revision change returned unexpected compatibility result: %+v", err)
	}
}

func testUSDBActivation(block uint64, version uint16) USDBConsensusActivation {
	return USDBConsensusActivation{
		Block:                   block,
		BTCActivationRegistryID: testBTCActivationRegistryID,
		BTCAnchorMaxAgeBlocks:   USDBDevelopmentBTCAnchorMaxAgeBlocks,
		Versions:                testUSDBConsensusVersions(version),
	}
}

func testUSDBConsensusVersions(version uint16) USDBConsensusVersions {
	return USDBConsensusVersions{
		PayloadVersion:                       1,
		BTCAnchorPolicyVersion:               1,
		DifficultyPolicyVersion:              version,
		RewardRuleVersion:                    1,
		CoinbaseEmissionPolicyVersion:        1,
		CollaborationEfficiencyPolicyVersion: 1,
		PricePolicyVersion:                   1,
		QuotePolicyVersion:                   1,
	}
}

func TestIsDividendFeeSplit(t *testing.T) {
	addr := common.HexToAddress("0x0000000000000000000000000000000000001000")
	cfg := &ChainConfig{}
	if cfg.IsDividendFeeSplit(big.NewInt(0)) {
		t.Fatalf("fee split must stay disabled without block and address")
	}
	cfg.DividendFeeSplitBlock = big.NewInt(10)
	if cfg.IsDividendFeeSplit(big.NewInt(10)) {
		t.Fatalf("fee split must stay disabled without a dividend address")
	}
	cfg.DividendAddress = addr
	if cfg.IsDividendFeeSplit(big.NewInt(10)) {
		t.Fatalf("fee split must stay disabled without a dividend code hash")
	}
	cfg.DividendCodeHash = common.HexToHash("0x1234")
	if cfg.IsDividendFeeSplit(big.NewInt(9)) {
		t.Fatalf("fee split activated before the configured block")
	}
	if !cfg.IsDividendFeeSplit(big.NewInt(10)) {
		t.Fatalf("fee split did not activate at the configured block")
	}
	if !cfg.IsDividendFeeSplit(big.NewInt(11)) {
		t.Fatalf("fee split did not remain active after the configured block")
	}
}
