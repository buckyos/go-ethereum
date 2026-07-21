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
	if policy == nil || policy.PayloadVersion != 1 || policy.DifficultyPolicyVersion != 1 {
		t.Fatalf("unexpected built-in USDB policy: %+v", policy)
	}
	policy.DifficultyPolicyVersion = 9
	if USDBChainConfig.USDB.DifficultyPolicyVersion != 1 {
		t.Fatal("policy lookup must return a copy of chain config state")
	}

	for _, config := range []*ChainConfig{
		{USDB: &USDBConsensusConfig{DifficultyPolicyVersion: 1}},
		{USDB: &USDBConsensusConfig{PayloadVersion: 1}},
	} {
		if _, err := config.USDBConsensusAt(7); err == nil {
			t.Fatalf("expected invalid USDB policy to fail: %+v", config.USDB)
		}
	}
}

func TestUSDBConsensusConfigJSONRoundTrip(t *testing.T) {
	encoded, err := json.Marshal(USDBChainConfig)
	if err != nil {
		t.Fatalf("failed to encode USDB chain config: %v", err)
	}
	var decoded ChainConfig
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("failed to decode USDB chain config: %v", err)
	}
	policy, err := decoded.USDBConsensusAt(19)
	if err != nil {
		t.Fatalf("failed to resolve round-tripped USDB policy: %v", err)
	}
	if policy == nil || policy.PayloadVersion != 1 || policy.DifficultyPolicyVersion != 1 {
		t.Fatalf("unexpected round-tripped USDB policy: %+v", policy)
	}
	if banner := decoded.String(); !strings.Contains(banner, "USDB profile selector: payload v1, difficulty policy v1 (genesis)") {
		t.Fatalf("USDB consensus versions missing from chain banner:\n%s", banner)
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
