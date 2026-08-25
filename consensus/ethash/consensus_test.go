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

package ethash

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/usdbstate"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
)

type diffTest struct {
	ParentTimestamp    uint64
	ParentDifficulty   *big.Int
	CurrentTimestamp   uint64
	CurrentBlocknumber *big.Int
	CurrentDifficulty  *big.Int
}

func (d *diffTest) UnmarshalJSON(b []byte) (err error) {
	var ext struct {
		ParentTimestamp    string
		ParentDifficulty   string
		CurrentTimestamp   string
		CurrentBlocknumber string
		CurrentDifficulty  string
	}
	if err := json.Unmarshal(b, &ext); err != nil {
		return err
	}

	d.ParentTimestamp = math.MustParseUint64(ext.ParentTimestamp)
	d.ParentDifficulty = math.MustParseBig256(ext.ParentDifficulty)
	d.CurrentTimestamp = math.MustParseUint64(ext.CurrentTimestamp)
	d.CurrentBlocknumber = math.MustParseBig256(ext.CurrentBlocknumber)
	d.CurrentDifficulty = math.MustParseBig256(ext.CurrentDifficulty)

	return nil
}

func TestCalcDifficulty(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "tests", "testdata", "BasicTests", "difficulty.json"))
	if err != nil {
		t.Skip(err)
	}
	defer file.Close()

	tests := make(map[string]diffTest)
	err = json.NewDecoder(file).Decode(&tests)
	if err != nil {
		t.Fatal(err)
	}

	config := &params.ChainConfig{HomesteadBlock: big.NewInt(1150000)}

	for name, test := range tests {
		number := new(big.Int).Sub(test.CurrentBlocknumber, big.NewInt(1))
		diff := CalcDifficulty(config, test.CurrentTimestamp, &types.Header{
			Number:     number,
			Time:       test.ParentTimestamp,
			Difficulty: test.ParentDifficulty,
		})
		if diff.Cmp(test.CurrentDifficulty) != 0 {
			t.Error(name, "failed. Expected", test.CurrentDifficulty, "and calculated", diff)
		}
	}
}

func randSlice(min, max uint32) []byte {
	var b = make([]byte, 4)
	rand.Read(b)
	a := binary.LittleEndian.Uint32(b)
	size := min + a%(max-min)
	out := make([]byte, size)
	rand.Read(out)
	return out
}

func TestDifficultyCalculators(t *testing.T) {
	rand.Seed(2)
	for i := 0; i < 5000; i++ {
		// 1 to 300 seconds diff
		var timeDelta = uint64(1 + rand.Uint32()%3000)
		diffBig := new(big.Int).SetBytes(randSlice(2, 10))
		if diffBig.Cmp(params.MinimumDifficulty) < 0 {
			diffBig.Set(params.MinimumDifficulty)
		}
		//rand.Read(difficulty)
		header := &types.Header{
			Difficulty: diffBig,
			Number:     new(big.Int).SetUint64(rand.Uint64() % 50_000_000),
			Time:       rand.Uint64() - timeDelta,
		}
		if rand.Uint32()&1 == 0 {
			header.UncleHash = types.EmptyUncleHash
		}
		bombDelay := new(big.Int).SetUint64(rand.Uint64() % 50_000_000)
		for i, pair := range []struct {
			bigFn  func(time uint64, parent *types.Header) *big.Int
			u256Fn func(time uint64, parent *types.Header) *big.Int
		}{
			{FrontierDifficultyCalculator, CalcDifficultyFrontierU256},
			{HomesteadDifficultyCalculator, CalcDifficultyHomesteadU256},
			{DynamicDifficultyCalculator(bombDelay), MakeDifficultyCalculatorU256(bombDelay)},
		} {
			time := header.Time + timeDelta
			want := pair.bigFn(time, header)
			have := pair.u256Fn(time, header)
			if want.BitLen() > 256 {
				continue
			}
			if want.Cmp(have) != 0 {
				t.Fatalf("pair %d: want %x have %x\nparent.Number: %x\np.Time: %x\nc.Time: %x\nBombdelay: %v\n", i, want, have,
					header.Number, header.Time, time, bombDelay)
			}
		}
	}
}

func TestCalcDifficultyKeepsEthPoWTransitionReset(t *testing.T) {
	config := &params.ChainConfig{
		EthPoWForkBlock:   big.NewInt(100),
		EthPoWForkSupport: true,
	}
	parentAtForkBoundary := &types.Header{
		Number:     big.NewInt(99),
		Time:       1000,
		Difficulty: new(big.Int).Set(params.MinimumDifficulty),
	}
	if diff := CalcDifficulty(config, 1009, parentAtForkBoundary); diff.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("unexpected transition reset difficulty at fork: have %v want 1", diff)
	}

	parentAtRebaseBoundary := &types.Header{
		Number:     big.NewInt(2147),
		Time:       2000,
		Difficulty: new(big.Int).Set(params.MinimumDifficulty),
	}
	if diff := CalcDifficulty(config, 2009, parentAtRebaseBoundary); diff.Cmp(params.ETHWStartDifficulty) != 0 {
		t.Fatalf("unexpected transition reset difficulty at fork+2048: have %v want %v", diff, params.ETHWStartDifficulty)
	}
}

func TestCalcDifficultySkipsEthPoWTransitionResetForGenesisChain(t *testing.T) {
	config := params.USDBChainConfig

	parentAtFirstBlock := &types.Header{
		Number:     big.NewInt(0),
		Time:       1000,
		Difficulty: big.NewInt(8192),
	}
	if diff := CalcDifficulty(config, 1009, parentAtFirstBlock); diff.Cmp(big.NewInt(1)) == 0 {
		t.Fatalf("genesis-start PoW chain must not reset difficulty to 1")
	}

	parentAtFormerRebaseHeight := &types.Header{
		Number:     big.NewInt(2047),
		Time:       2000,
		Difficulty: new(big.Int).Set(params.MinimumDifficulty),
	}
	want := calcDifficultyEthPoW(2009, parentAtFormerRebaseHeight, config.EthPoWMinimumDifficulty())
	if diff := CalcDifficulty(config, 2009, parentAtFormerRebaseHeight); diff.Cmp(want) != 0 {
		t.Fatalf("unexpected difficulty for genesis-start PoW chain: have %v want %v", diff, want)
	}
	if want.Cmp(params.ETHWStartDifficulty) == 0 {
		t.Fatalf("test invariant broken: expected normal difficulty path, not ETHW reset value")
	}
}

func TestCalcDifficultyUsesUSDBMinimumDifficulty(t *testing.T) {
	config := params.USDBChainConfig
	parent := &types.Header{
		Number:     big.NewInt(1),
		Time:       1000,
		Difficulty: new(big.Int).Set(params.USDBGenesisDifficulty),
	}
	diff := CalcDifficulty(config, 1009, parent)
	if diff.Cmp(params.USDBMinimumDifficulty) < 0 {
		t.Fatalf("unexpected USDB minimum difficulty floor: have %v want >= %v", diff, params.USDBMinimumDifficulty)
	}
	if diff.Cmp(params.MinimumDifficulty) >= 0 {
		t.Fatalf("USDB chain should stay below the legacy global minimum difficulty when bootstrap difficulty is low")
	}
}

func TestCalcDifficultyUsesOverrideMinimumDifficulty(t *testing.T) {
	config := *params.USDBChainConfig
	overrideMinimum := big.NewInt(0x40000)
	config.EthPoWMinimumDifficultyOverride = overrideMinimum

	parent := &types.Header{
		Number:     big.NewInt(1),
		Time:       1000,
		Difficulty: big.NewInt(0x20000),
	}
	diff := CalcDifficulty(&config, 1100, parent)
	if diff.Cmp(overrideMinimum) != 0 {
		t.Fatalf("unexpected overridden minimum difficulty floor: have %v want %v", diff, overrideMinimum)
	}
}

func BenchmarkDifficultyCalculator(b *testing.B) {
	x1 := makeDifficultyCalculator(big.NewInt(1000000))
	x2 := MakeDifficultyCalculatorU256(big.NewInt(1000000))
	h := &types.Header{
		ParentHash: common.Hash{},
		UncleHash:  types.EmptyUncleHash,
		Difficulty: big.NewInt(0xffffff),
		Number:     big.NewInt(500000),
		Time:       1000000,
	}
	b.Run("big-frontier", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			calcDifficultyFrontier(1000014, h)
		}
	})
	b.Run("u256-frontier", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			CalcDifficultyFrontierU256(1000014, h)
		}
	})
	b.Run("big-homestead", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			calcDifficultyHomestead(1000014, h)
		}
	})
	b.Run("u256-homestead", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			CalcDifficultyHomesteadU256(1000014, h)
		}
	})
	b.Run("big-generic", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			x1(1000014, h)
		}
	})
	b.Run("u256-generic", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			x2(1000014, h)
		}
	})
}

type stubChainHeaderReader struct {
	config *params.ChainConfig
	header *types.Header
}

func (s *stubChainHeaderReader) Config() *params.ChainConfig  { return s.config }
func (s *stubChainHeaderReader) CurrentHeader() *types.Header { return nil }
func (s *stubChainHeaderReader) GetHeader(hash common.Hash, number uint64) *types.Header {
	if s.header != nil && s.header.Hash() == hash && s.header.Number.Uint64() == number {
		return s.header
	}
	return nil
}
func (s *stubChainHeaderReader) GetHeaderByNumber(uint64) *types.Header    { return nil }
func (s *stubChainHeaderReader) GetHeaderByHash(common.Hash) *types.Header { return nil }
func (s *stubChainHeaderReader) GetTd(common.Hash, uint64) *big.Int        { return nil }
func (s *stubChainHeaderReader) GetBlock(common.Hash, uint64) *types.Block { return nil }

type stubProfileResolver struct {
	resolved     *usdb.ResolvedConsensusProfile
	err          error
	lastRegistry string
	lastExtra    []byte
	calls        int
}

func (s *stubProfileResolver) ResolveProfile(_ context.Context, btcActivationRegistryID string, headerExtra []byte) (*usdb.ResolvedConsensusProfile, error) {
	s.lastRegistry = btcActivationRegistryID
	s.lastExtra = append([]byte(nil), headerExtra...)
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.resolved, nil
}

func (s *stubProfileResolver) Close() {}

func newTestStateDB(t *testing.T) *state.StateDB {
	t.Helper()

	statedb, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatalf("failed to create state db: %v", err)
	}
	return statedb
}

func newTestPayloadBytes(t *testing.T) []byte {
	t.Helper()
	return newTestPayloadBytesForDifficultyVersion(t, usdb.DifficultyPolicyVersionV1)
}

func newTestPayloadBytesForDifficultyVersion(t *testing.T, difficultyPolicyVersion uint16) []byte {
	t.Helper()

	payload, err := usdb.NewProfileSelectorPayload(
		difficultyPolicyVersion,
		123,
		0,
		common.HexToHash("0x1111").Hex()[2:],
		common.HexToHash("0x2222").Hex()[2:],
		common.HexToHash("0x3333").Hex()[2:]+"i7",
	)
	if err != nil {
		t.Fatalf("failed to build payload: %v", err)
	}
	encoded, err := payload.MarshalBinary()
	if err != nil {
		t.Fatalf("failed to encode payload: %v", err)
	}
	return encoded
}

func newTestUSDBChainConfig() *params.ChainConfig {
	return &params.ChainConfig{
		HomesteadBlock: big.NewInt(0),
		USDB: &params.USDBConsensusConfig{
			BTCNetworkID:         "btc-regtest",
			BTCIndexOriginHeight: 1,
			Activations: []params.USDBConsensusActivation{{
				BTCActivationRegistryID: usdb.BTCRegtestActivationRegistryIDV1,
				BTCAnchorMaxAgeBlocks:   params.USDBDevelopmentBTCAnchorMaxAgeBlocks,
				Versions: params.USDBConsensusVersions{
					PayloadVersion:          usdb.ProfileSelectorPayloadVersionV1,
					BTCAnchorPolicyVersion:  usdb.BTCAnchorPolicyVersionV1,
					DifficultyPolicyVersion: usdb.DifficultyPolicyVersionV1,
				},
			}},
		},
	}
}

func newTestUSDBRewardChainConfig() *params.ChainConfig {
	config := newTestUSDBChainConfig()
	config.ChainID = big.NewInt(20_260_323)
	config.USDB.Activations[0].Versions.RewardRuleVersion = usdb.RewardRuleVersionV1
	config.USDB.Activations[0].Versions.CoinbaseEmissionPolicyVersion = usdb.CoinbaseEmissionPolicyVersionV1
	config.USDB.Activations[0].Versions.CollaborationEfficiencyPolicyVersion = usdb.CollaborationEfficiencyPolicyVersionV1
	config.USDB.Activations[0].Versions.PricePolicyVersion = usdb.PricePolicyVersionV1
	return config
}

func initializeTestUSDBRewardState(t *testing.T, statedb *state.StateDB, config *params.ChainConfig, issued *big.Int) {
	t.Helper()
	statedb.SetNonce(usdbstate.SystemStateAddress, usdbstate.SystemStateNonce)
	statedb.SetState(
		usdbstate.SystemStateAddress,
		usdbstate.SystemStateSchemaVersionSlot,
		common.BigToHash(new(big.Int).SetUint64(usdbstate.SystemStateSchemaVersion)),
	)
	statedb.SetState(
		usdbstate.SystemStateAddress,
		usdbstate.IssuedUSDBAtomsSlot,
		common.BigToHash(issued),
	)
	rangeID, err := usdb.FixedPriceRangeIDV1(config.ChainID, 0)
	if err != nil {
		t.Fatalf("failed to derive test price range: %v", err)
	}
	price := common.BigToHash(usdb.FixedPriceAtomsPerBTCV1())
	statedb.SetState(usdbstate.SystemStateAddress, usdbstate.PriceAtomsPerBTCSlot, price)
	statedb.SetState(usdbstate.SystemStateAddress, usdbstate.RealPriceAtomsPerBTCSlot, price)
	statedb.SetState(usdbstate.SystemStateAddress, usdbstate.PricePolicyVersionSlot, common.BigToHash(big.NewInt(1)))
	statedb.SetState(usdbstate.SystemStateAddress, usdbstate.PriceSourceKindSlot, common.BigToHash(big.NewInt(1)))
	statedb.SetState(usdbstate.SystemStateAddress, usdbstate.PricePolicyRangeIDSlot, rangeID)
}

func TestVerifyHeaderValidatesUsdbProfileSelectorBeforeResolution(t *testing.T) {
	config := newTestUSDBChainConfig()
	chain := &stubChainHeaderReader{config: config}
	parent := &types.Header{
		Number:     big.NewInt(0),
		Time:       1_000,
		Difficulty: big.NewInt(131_072),
		GasLimit:   30_000_000,
	}
	validExtra := newTestPayloadBytes(t)
	newHeader := func(extra []byte) *types.Header {
		return &types.Header{
			Number:     big.NewInt(1),
			Time:       1_001,
			Difficulty: CalcDifficulty(config, 1_001, parent),
			GasLimit:   parent.GasLimit,
			Extra:      append([]byte(nil), extra...),
		}
	}
	resolver := &stubProfileResolver{resolved: newTestUSDBDifficultyProfile(0)}
	engine := &Ethash{usdbProfileResolver: resolver}
	if err := engine.verifyHeader(chain, newHeader(validExtra), parent, false, false, 2_000); err != nil {
		t.Fatalf("valid selector header rejected: %v", err)
	}
	if resolver.calls != 1 {
		t.Fatalf("valid selector resolved profile %d times, want 1", resolver.calls)
	}

	tests := []struct {
		name          string
		extra         []byte
		expectedError error
	}{
		{name: "missing", expectedError: usdb.ErrMissingProfileSelector},
		{name: "wrong size", extra: validExtra[:len(validExtra)-1], expectedError: usdb.ErrProfileSelectorSize},
		{name: "wrong payload version", extra: append([]byte(nil), validExtra...), expectedError: usdb.ErrProfileSelectorVersion},
		{name: "wrong difficulty policy", extra: append([]byte(nil), validExtra...), expectedError: usdb.ErrDifficultyPolicyVersionMismatch},
	}
	tests[2].extra[0] = 2
	tests[3].extra[2] = 2
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := resolver.calls
			if err := engine.verifyHeader(chain, newHeader(test.extra), parent, false, false, 2_000); !errors.Is(err, test.expectedError) {
				t.Fatalf("expected %v, got %v", test.expectedError, err)
			}
			if resolver.calls != calls {
				t.Fatal("malformed payload reached the profile resolver")
			}
		})
	}
}

func TestVerifyHeaderKeepsLegacyExtraLimitBeforeUSDBActivation(t *testing.T) {
	config := newTestUSDBChainConfig()
	config.USDB.Activations[0].Block = 10
	parent := &types.Header{
		Number:     big.NewInt(0),
		Time:       1_000,
		Difficulty: big.NewInt(131_072),
		GasLimit:   30_000_000,
	}
	header := &types.Header{
		Number:     big.NewInt(1),
		Time:       1_001,
		Difficulty: CalcDifficulty(config, 1_001, parent),
		GasLimit:   parent.GasLimit,
		Extra:      make([]byte, params.MaximumExtraDataSize+1),
	}
	err := (&Ethash{}).verifyHeader(
		&stubChainHeaderReader{config: config},
		header,
		parent,
		false,
		false,
		2_000,
	)
	if err == nil || !strings.Contains(err.Error(), "extra-data too long: 33 > 32") {
		t.Fatalf("legacy oversized extra returned %v", err)
	}
}

func TestVerifyHeaderEnforcesBTCAnchorParentTransitionBeforeResolution(t *testing.T) {
	config := newTestUSDBChainConfig()
	config.USDB.Activations[0].BTCAnchorMaxAgeBlocks = 2
	chain := &stubChainHeaderReader{config: config}
	parent := &types.Header{
		Number:     big.NewInt(1),
		Time:       1_000,
		Difficulty: big.NewInt(131_072),
		GasLimit:   30_000_000,
		Extra:      newTestPayloadBytes(t),
	}
	newHeader := func(selector usdb.ProfileSelectorPayload) *types.Header {
		extra, err := selector.MarshalBinary()
		if err != nil {
			t.Fatalf("encode child selector: %v", err)
		}
		return &types.Header{
			ParentHash: parent.Hash(),
			Number:     big.NewInt(2),
			Time:       1_001,
			Difficulty: CalcDifficulty(config, 1_001, parent),
			GasLimit:   parent.GasLimit,
			Extra:      extra,
		}
	}
	var parentSelector usdb.ProfileSelectorPayload
	if err := parentSelector.UnmarshalBinary(parent.Extra); err != nil {
		t.Fatalf("decode parent selector: %v", err)
	}
	valid := parentSelector
	valid.BTCAnchorAgeBlocks = 1

	resolver := &stubProfileResolver{resolved: newTestUSDBDifficultyProfile(0)}
	engine := &Ethash{usdbProfileResolver: resolver}
	if err := engine.verifyHeader(chain, newHeader(valid), parent, false, false, 2_000); err != nil {
		t.Fatalf("valid same-anchor transition rejected: %v", err)
	}
	if resolver.calls != 1 {
		t.Fatalf("valid transition resolved profile %d times, want 1", resolver.calls)
	}

	tests := []struct {
		name    string
		child   usdb.ProfileSelectorPayload
		wantErr error
	}{
		{
			name:    "counter does not increment",
			child:   parentSelector,
			wantErr: usdb.ErrBTCAnchorAgeMismatch,
		},
		{
			name: "height regresses",
			child: func() usdb.ProfileSelectorPayload {
				child := parentSelector
				child.BTCHeight--
				return child
			}(),
			wantErr: usdb.ErrBTCAnchorHeightRegression,
		},
		{
			name: "same height identity changes",
			child: func() usdb.ProfileSelectorPayload {
				child := valid
				child.SnapshotID[0] ^= 0xff
				return child
			}(),
			wantErr: usdb.ErrBTCAnchorIdentityMismatch,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := resolver.calls
			if err := engine.verifyHeader(chain, newHeader(test.child), parent, false, false, 2_000); !errors.Is(err, test.wantErr) {
				t.Fatalf("expected %v, got %v", test.wantErr, err)
			}
			if resolver.calls != calls {
				t.Fatal("invalid anchor transition reached the profile resolver")
			}
		})
	}

	parentSelector.BTCAnchorAgeBlocks = 2
	parent.Extra, _ = parentSelector.MarshalBinary()
	overLimit := parentSelector
	overLimit.BTCAnchorAgeBlocks = 3
	calls := resolver.calls
	if err := engine.verifyHeader(chain, newHeader(overLimit), parent, false, false, 2_000); !errors.Is(err, usdb.ErrBTCAnchorAgeExceeded) {
		t.Fatalf("expected max-age error, got %v", err)
	}
	if resolver.calls != calls {
		t.Fatal("over-age selector reached the profile resolver")
	}

	advanced := parentSelector
	advanced.BTCHeight++
	advanced.BTCAnchorAgeBlocks = 0
	if err := engine.verifyHeader(chain, newHeader(advanced), parent, false, false, 2_000); err != nil {
		t.Fatalf("higher BTC anchor did not reset age: %v", err)
	}
}

func TestVerifyHeaderRequiresZeroBTCAnchorAgeAtFirstActiveBlock(t *testing.T) {
	config := newTestUSDBChainConfig()
	config.USDB.Activations[0].Block = 2
	parent := &types.Header{
		Number:     big.NewInt(1),
		Time:       1_000,
		Difficulty: big.NewInt(131_072),
		GasLimit:   30_000_000,
	}
	var selector usdb.ProfileSelectorPayload
	if err := selector.UnmarshalBinary(newTestPayloadBytes(t)); err != nil {
		t.Fatalf("decode selector: %v", err)
	}
	selector.BTCAnchorAgeBlocks = 1
	extra, err := selector.MarshalBinary()
	if err != nil {
		t.Fatalf("encode selector: %v", err)
	}
	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     big.NewInt(2),
		Time:       1_001,
		Difficulty: CalcDifficulty(config, 1_001, parent),
		GasLimit:   parent.GasLimit,
		Extra:      extra,
	}
	resolver := &stubProfileResolver{resolved: newTestUSDBDifficultyProfile(0)}
	engine := &Ethash{usdbProfileResolver: resolver}
	if err := engine.verifyHeader(
		&stubChainHeaderReader{config: config},
		header,
		parent,
		false,
		false,
		2_000,
	); !errors.Is(err, usdb.ErrBTCAnchorAgeMismatch) {
		t.Fatalf("expected first-active age error, got %v", err)
	}
	if resolver.calls != 0 {
		t.Fatal("invalid first-active age reached the profile resolver")
	}
}

func TestVerifyHeaderUsesExpectedVersionAtActivationBoundary(t *testing.T) {
	config := newTestUSDBChainConfig()
	config.USDB.Activations = append(config.USDB.Activations, params.USDBConsensusActivation{
		Block:                   2,
		BTCActivationRegistryID: usdb.BTCRegtestActivationRegistryIDV1,
		BTCAnchorMaxAgeBlocks:   params.USDBDevelopmentBTCAnchorMaxAgeBlocks,
		Versions: params.USDBConsensusVersions{
			PayloadVersion:          usdb.ProfileSelectorPayloadVersionV1,
			BTCAnchorPolicyVersion:  usdb.BTCAnchorPolicyVersionV1,
			DifficultyPolicyVersion: 2,
		},
	})
	parent := &types.Header{
		Number:     big.NewInt(1),
		Time:       1_000,
		Difficulty: big.NewInt(131_072),
		GasLimit:   30_000_000,
	}
	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     big.NewInt(2),
		Time:       1_001,
		Difficulty: CalcDifficulty(config, 1_001, parent),
		GasLimit:   parent.GasLimit,
		Extra:      newTestPayloadBytes(t),
	}
	resolver := &stubProfileResolver{resolved: newTestUSDBDifficultyProfile(0)}
	engine := &Ethash{usdbProfileResolver: resolver}
	err := engine.verifyHeader(&stubChainHeaderReader{config: config}, header, parent, false, false, 2_000)
	if !errors.Is(err, usdb.ErrDifficultyPolicyVersionMismatch) {
		t.Fatalf("activation-boundary mismatch returned %v", err)
	}
	if resolver.calls != 0 {
		t.Fatal("version-mismatched payload reached the profile resolver")
	}
}

func TestPrepareAndVerifyHeaderApplySameUsdbDifficultyProfile(t *testing.T) {
	config := newTestUSDBChainConfig()
	parent := &types.Header{
		Number:     big.NewInt(0),
		Time:       1_000,
		Difficulty: big.NewInt(131_072),
		GasLimit:   30_000_000,
	}
	chain := &stubChainHeaderReader{config: config, header: parent}
	resolver := &stubProfileResolver{resolved: newTestUSDBDifficultyProfile(1_000_000)}
	engine := &Ethash{
		config:              Config{Log: log.Root()},
		usdbProfileResolver: resolver,
	}
	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     big.NewInt(1),
		Time:       1_001,
		GasLimit:   parent.GasLimit,
		Extra:      newTestPayloadBytes(t),
	}
	baseDifficulty := CalcDifficulty(config, header.Time, parent)
	wantDifficulty, err := usdb.RealDifficultyV1(baseDifficulty, resolver.resolved.DifficultyFactorBps)
	if err != nil {
		t.Fatalf("failed to calculate expected real difficulty: %v", err)
	}
	if err := engine.Prepare(chain, header); err != nil {
		t.Fatalf("failed to prepare USDB header: %v", err)
	}
	if header.Difficulty.Cmp(wantDifficulty) != 0 {
		t.Fatalf("prepared difficulty mismatch: have %s want %s", header.Difficulty, wantDifficulty)
	}
	if header.Difficulty.Cmp(baseDifficulty) == 0 {
		t.Fatal("profile factor did not change the base difficulty")
	}
	if err := engine.verifyHeader(chain, header, parent, false, false, 2_000); err != nil {
		t.Fatalf("prepared header rejected by validator: %v", err)
	}
	if resolver.calls != 2 {
		t.Fatalf("miner and validator profile resolution count mismatch: have %d want 2", resolver.calls)
	}

	header.Difficulty = baseDifficulty
	if err := engine.verifyHeader(chain, header, parent, false, false, 2_000); err == nil || !strings.Contains(err.Error(), "invalid difficulty") {
		t.Fatalf("unadjusted base difficulty was not rejected: %v", err)
	}
}

func TestPrepareAndVerifyHeaderFailWhenProfileServiceIsUnavailable(t *testing.T) {
	config := newTestUSDBChainConfig()
	parent := &types.Header{
		Number:     big.NewInt(0),
		Time:       1_000,
		Difficulty: big.NewInt(131_072),
		GasLimit:   30_000_000,
	}
	chain := &stubChainHeaderReader{config: config, header: parent}
	engine := &Ethash{
		config:              Config{Log: log.Root()},
		usdbProfileResolver: &stubProfileResolver{err: context.DeadlineExceeded},
	}
	newHeader := func() *types.Header {
		return &types.Header{
			ParentHash: parent.Hash(),
			Number:     big.NewInt(1),
			Time:       1_001,
			Difficulty: CalcDifficulty(config, 1_001, parent),
			GasLimit:   parent.GasLimit,
			Extra:      newTestPayloadBytes(t),
		}
	}
	if err := engine.Prepare(chain, newHeader()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("miner prepare did not fail closed: %v", err)
	}
	if err := engine.verifyHeader(chain, newHeader(), parent, false, false, 2_000); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("validator did not fail closed: %v", err)
	}
}

func TestApplyUSDBDifficultyPolicyRejectsUnimplementedVersions(t *testing.T) {
	difficultyPolicy := &params.USDBConsensusVersions{DifficultyPolicyVersion: 2}
	if _, err := applyUSDBDifficultyPolicy(
		difficultyPolicy,
		big.NewInt(100),
		newTestUSDBQuoteDecision(usdb.QuotePolicyVersionDisabled, usdb.BasisPointDenominator),
	); err == nil {
		t.Fatalf("unimplemented difficulty policy unexpectedly accepted: %+v", difficultyPolicy)
	}

	quotePolicy := &params.USDBConsensusVersions{
		DifficultyPolicyVersion: usdb.DifficultyPolicyVersionV1,
		QuotePolicyVersion:      usdb.QuotePolicyVersionV1,
	}
	if _, err := resolveUSDBQuotePolicy(
		quotePolicy,
		&types.Header{Number: big.NewInt(1)},
		newTestUSDBDifficultyProfile(0),
	); err == nil {
		t.Fatalf("unimplemented quote policy unexpectedly accepted: %+v", quotePolicy)
	}
}

func TestFinalizeAndAssembleValidatesProfileBeforeLegacyReward(t *testing.T) {
	coinbase := common.HexToAddress("0x1001")
	resolver := &stubProfileResolver{
		resolved: &usdb.ResolvedConsensusProfile{DifficultyFactorBps: usdb.BasisPointDenominator},
	}
	engine := &Ethash{
		config: Config{
			Log: log.Root(),
		},
		usdbProfileResolver: resolver,
	}
	header := &types.Header{
		Number:   big.NewInt(1),
		Coinbase: coinbase,
		Extra:    newTestPayloadBytes(t),
	}
	statedb := newTestStateDB(t)
	chain := &stubChainHeaderReader{config: newTestUSDBChainConfig()}

	block, err := engine.FinalizeAndAssemble(chain, header, statedb, nil, nil, nil)
	if err != nil {
		t.Fatalf("FinalizeAndAssemble returned error: %v", err)
	}
	if block == nil {
		t.Fatalf("expected block to be assembled")
	}
	if resolver.calls != 1 || !bytes.Equal(resolver.lastExtra, header.Extra) {
		t.Fatalf("unexpected profile resolution: calls=%d extra=%x", resolver.calls, resolver.lastExtra)
	}
	if got := statedb.GetBalance(coinbase); got.Cmp(FrontierBlockReward) != 0 {
		t.Fatalf("unexpected miner balance: have %s want %s", got, FrontierBlockReward)
	}
}

func TestFinalizeAndAssembleReturnsErrorWhenUsdbVerifierFails(t *testing.T) {
	coinbase := common.HexToAddress("0x1002")
	engine := &Ethash{
		config: Config{
			Log: log.Root(),
		},
		usdbProfileResolver: &stubProfileResolver{err: errInvalidPoW},
	}
	header := &types.Header{
		Number:   big.NewInt(1),
		Coinbase: coinbase,
		Extra:    newTestPayloadBytes(t),
	}
	statedb := newTestStateDB(t)
	chain := &stubChainHeaderReader{config: newTestUSDBChainConfig()}

	block, err := engine.FinalizeAndAssemble(chain, header, statedb, nil, nil, nil)
	if err == nil {
		t.Fatalf("expected FinalizeAndAssemble to fail")
	}
	if block != nil {
		t.Fatalf("expected no block on verifier failure")
	}
	if got := statedb.GetBalance(coinbase); got.Sign() != 0 {
		t.Fatalf("unexpected miner balance after verifier failure: %s", got)
	}
}

func TestFinalizeAndAssembleAppliesUSDBRewardV1(t *testing.T) {
	coinbase := common.HexToAddress("0x1005")
	config := newTestUSDBRewardChainConfig()
	profile := &usdb.ResolvedConsensusProfile{
		RewardRecipient:    coinbase,
		TotalMinerBTCSats:  big.NewInt(100_000_000),
		RawEnergy:          new(big.Int),
		CollabContribution: big.NewInt(100),
		EffectiveEnergy:    big.NewInt(100),
	}
	engine := &Ethash{
		config:              Config{Log: log.Root()},
		usdbProfileResolver: &stubProfileResolver{resolved: profile},
	}
	header := &types.Header{
		Number:    big.NewInt(1),
		Coinbase:  coinbase,
		Extra:     newTestPayloadBytes(t),
		UncleHash: types.EmptyUncleHash,
	}
	statedb := newTestStateDB(t)
	initializeTestUSDBRewardState(t, statedb, config, big.NewInt(0))
	chain := &stubChainHeaderReader{config: config}

	block, err := engine.FinalizeAndAssemble(chain, header, statedb, nil, nil, nil)
	if err != nil || block == nil {
		t.Fatalf("USDB reward v1 finalization failed: block=%v err=%v", block, err)
	}
	const wantEmission = "634195839675291730"
	if got := statedb.GetBalance(coinbase); got.String() != wantEmission {
		t.Fatalf("unexpected USDB reward balance: have %s want %s", got, wantEmission)
	}
	if got, _ := usdbstate.ReadUint256(statedb, usdbstate.IssuedUSDBAtomsSlot); got.String() != wantEmission {
		t.Fatalf("unexpected issued supply: have %s want %s", got, wantEmission)
	}
	for slot, want := range map[common.Hash]string{
		usdbstate.KWindowSumSlot:    "100",
		usdbstate.KWindowCountSlot:  "1",
		usdbstate.KWindowCursorSlot: "1",
		usdbstate.KLastCESlot:       "100",
		usdbstate.KLastAESlot:       "0",
		usdbstate.KLastKBpsSlot:     "10000",
		usdbstate.KCERingSlot(0):    "100",
	} {
		got, readErr := usdbstate.ReadUint256(statedb, slot)
		if readErr != nil || got.String() != want {
			t.Fatalf("unexpected K slot %s: have %v want %s err=%v", slot, got, want, readErr)
		}
	}
}

func TestUSDBRewardStateRevertsWithParentRoot(t *testing.T) {
	coinbase := common.HexToAddress("0x1007")
	config := newTestUSDBRewardChainConfig()
	next := config.USDB.Activations[0]
	next.Block = 1
	config.USDB.Activations = append(config.USDB.Activations, next)

	stateDatabase := state.NewDatabase(rawdb.NewMemoryDatabase())
	parentState, err := state.New(common.Hash{}, stateDatabase, nil)
	if err != nil {
		t.Fatalf("failed to create parent state: %v", err)
	}
	initializeTestUSDBRewardState(t, parentState, config, big.NewInt(0))
	initialRangeID := parentState.GetState(usdbstate.SystemStateAddress, usdbstate.PricePolicyRangeIDSlot)
	parentRoot, err := parentState.Commit(false)
	if err != nil {
		t.Fatalf("failed to commit parent state: %v", err)
	}
	if err := stateDatabase.TrieDB().Commit(parentRoot, false, nil); err != nil {
		t.Fatalf("failed to persist parent state: %v", err)
	}
	statedb, err := state.New(parentRoot, stateDatabase, nil)
	if err != nil {
		t.Fatalf("failed to reopen parent state: %v", err)
	}

	engine := &Ethash{
		config: Config{Log: log.Root()},
		usdbProfileResolver: &stubProfileResolver{resolved: &usdb.ResolvedConsensusProfile{
			RewardRecipient:    coinbase,
			TotalMinerBTCSats:  big.NewInt(100_000_000),
			RawEnergy:          new(big.Int),
			CollabContribution: big.NewInt(250),
			EffectiveEnergy:    big.NewInt(250),
		}},
	}
	header := &types.Header{
		Number:    big.NewInt(1),
		Coinbase:  coinbase,
		Extra:     newTestPayloadBytes(t),
		UncleHash: types.EmptyUncleHash,
	}
	if block, err := engine.FinalizeAndAssemble(
		&stubChainHeaderReader{config: config},
		header,
		statedb,
		nil,
		nil,
		nil,
	); err != nil || block == nil {
		t.Fatalf("failed to apply reward state before rollback: block=%v err=%v", block, err)
	}
	if got := statedb.GetBalance(coinbase); got.Sign() == 0 {
		t.Fatalf("reward balance was not written before rollback")
	}
	if got := statedb.GetState(usdbstate.SystemStateAddress, usdbstate.KWindowCountSlot); got == (common.Hash{}) {
		t.Fatalf("K state was not written before rollback")
	}
	if got := statedb.GetState(usdbstate.SystemStateAddress, usdbstate.PricePolicyRangeIDSlot); got == initialRangeID {
		t.Fatalf("price range was not advanced before rollback")
	}

	childRoot, err := statedb.Commit(false)
	if err != nil {
		t.Fatalf("failed to commit child state: %v", err)
	}
	if childRoot == parentRoot {
		t.Fatalf("reward transition did not change state root")
	}
	statedb, err = state.New(parentRoot, stateDatabase, nil)
	if err != nil {
		t.Fatalf("failed to restore parent state after reorg: %v", err)
	}

	if got := statedb.GetBalance(coinbase); got.Sign() != 0 {
		t.Fatalf("reward balance survived rollback: %s", got)
	}
	if got, err := usdbstate.ReadUint256(statedb, usdbstate.IssuedUSDBAtomsSlot); err != nil || got.Sign() != 0 {
		t.Fatalf("issued supply survived rollback: value=%v err=%v", got, err)
	}
	for _, slot := range []common.Hash{
		usdbstate.KWindowSumSlot,
		usdbstate.KWindowCountSlot,
		usdbstate.KWindowCursorSlot,
		usdbstate.KLastCESlot,
		usdbstate.KLastAESlot,
		usdbstate.KLastKBpsSlot,
		usdbstate.KCERingSlot(0),
	} {
		if got := statedb.GetState(usdbstate.SystemStateAddress, slot); got != (common.Hash{}) {
			t.Fatalf("K state survived rollback: slot=%s value=%s", slot, got)
		}
	}
	if got := statedb.GetState(usdbstate.SystemStateAddress, usdbstate.PricePolicyRangeIDSlot); got != initialRangeID {
		t.Fatalf("price range did not roll back: have %s want %s", got, initialRangeID)
	}
}

func TestUSDBRewardV1RecipientMismatchLeavesStateUnchanged(t *testing.T) {
	config := newTestUSDBRewardChainConfig()
	coinbase := common.HexToAddress("0x1006")
	statedb := newTestStateDB(t)
	initializeTestUSDBRewardState(t, statedb, config, big.NewInt(7))
	engine := &Ethash{
		config: Config{Log: log.Root()},
		usdbProfileResolver: &stubProfileResolver{resolved: &usdb.ResolvedConsensusProfile{
			RewardRecipient:    common.HexToAddress("0x2006"),
			TotalMinerBTCSats:  big.NewInt(100_000_000),
			CollabContribution: big.NewInt(1),
		}},
	}
	header := &types.Header{
		Number:    big.NewInt(1),
		Coinbase:  coinbase,
		Extra:     newTestPayloadBytes(t),
		UncleHash: types.EmptyUncleHash,
	}
	block, err := engine.FinalizeAndAssemble(
		&stubChainHeaderReader{config: config},
		header,
		statedb,
		nil,
		nil,
		nil,
	)
	if err == nil || block != nil || !strings.Contains(err.Error(), "reward recipient mismatch") {
		t.Fatalf("recipient mismatch was not rejected: block=%v err=%v", block, err)
	}
	if got := statedb.GetBalance(coinbase); got.Sign() != 0 {
		t.Fatalf("recipient mismatch changed balance: %s", got)
	}
	if got, _ := usdbstate.ReadUint256(statedb, usdbstate.IssuedUSDBAtomsSlot); got.Cmp(big.NewInt(7)) != 0 {
		t.Fatalf("recipient mismatch changed issued supply: %s", got)
	}
	if got := statedb.GetState(usdbstate.SystemStateAddress, usdbstate.KWindowCountSlot); got != (common.Hash{}) {
		t.Fatalf("recipient mismatch changed K state: %s", got)
	}
}

func TestUSDBRewardV1DisablesUncles(t *testing.T) {
	config := newTestUSDBRewardChainConfig()
	chain := &stubChainHeaderReader{config: config}
	engine := &Ethash{config: Config{Log: log.Root()}}
	parent := &types.Header{
		Number:     big.NewInt(0),
		Time:       1_000,
		Difficulty: big.NewInt(131_072),
		GasLimit:   30_000_000,
	}
	header := &types.Header{
		Number:     big.NewInt(1),
		Time:       1_001,
		Difficulty: CalcDifficulty(config, 1_001, parent),
		GasLimit:   parent.GasLimit,
		Extra:      newTestPayloadBytes(t),
		UncleHash:  common.HexToHash("0x1234"),
	}
	if err := engine.verifyHeader(chain, header, parent, false, false, 2_000); !errors.Is(err, errUSDBUnclesDisabled) {
		t.Fatalf("non-empty uncle hash was not rejected: %v", err)
	}

	uncle := &types.Header{Number: big.NewInt(0)}
	block := types.NewBlock(header, nil, []*types.Header{uncle}, nil, trie.NewStackTrie(nil))
	if err := engine.VerifyUncles(chain, block); !errors.Is(err, errUSDBUnclesDisabled) {
		t.Fatalf("uncle body was not rejected: %v", err)
	}

	statedb := newTestStateDB(t)
	initializeTestUSDBRewardState(t, statedb, config, big.NewInt(0))
	engine.usdbProfileResolver = &stubProfileResolver{resolved: &usdb.ResolvedConsensusProfile{
		RewardRecipient:    header.Coinbase,
		TotalMinerBTCSats:  big.NewInt(1),
		CollabContribution: big.NewInt(0),
	}}
	if _, err := engine.FinalizeAndAssemble(chain, header, statedb, nil, []*types.Header{uncle}, nil); !errors.Is(err, errUSDBUnclesDisabled) {
		t.Fatalf("uncle reward transition was not rejected: %v", err)
	}
}

func TestPrepareKTransitionUsesPriorFullWindowAndReplacesOldestSample(t *testing.T) {
	statedb := newTestStateDB(t)
	const (
		windowSum = 5_040_000
		oldSample = 100
		currentCE = 50
	)
	statedb.SetState(usdbstate.SystemStateAddress, usdbstate.KWindowSumSlot, common.BigToHash(big.NewInt(windowSum)))
	statedb.SetState(
		usdbstate.SystemStateAddress,
		usdbstate.KWindowCountSlot,
		common.BigToHash(new(big.Int).SetUint64(usdb.KWindowBlocks)),
	)
	statedb.SetState(usdbstate.SystemStateAddress, usdbstate.KWindowCursorSlot, common.Hash{})
	statedb.SetState(usdbstate.SystemStateAddress, usdbstate.KCERingSlot(0), common.BigToHash(big.NewInt(oldSample)))

	kBps, writes, err := prepareKTransition(
		usdb.CollaborationEfficiencyPolicyVersionV1,
		statedb,
		big.NewInt(currentCE),
	)
	if err != nil {
		t.Fatalf("full-window K transition failed: %v", err)
	}
	if kBps != 9_090 {
		t.Fatalf("K used the wrong historical average: have %d want 9090", kBps)
	}
	for _, write := range writes {
		statedb.SetState(usdbstate.SystemStateAddress, write.slot, write.value)
	}
	for slot, want := range map[common.Hash]string{
		usdbstate.KWindowSumSlot:    "5039950",
		usdbstate.KWindowCountSlot:  "50400",
		usdbstate.KWindowCursorSlot: "1",
		usdbstate.KCERingSlot(0):    "50",
		usdbstate.KLastCESlot:       "50",
		usdbstate.KLastAESlot:       "100",
		usdbstate.KLastKBpsSlot:     "9090",
	} {
		got, readErr := usdbstate.ReadUint256(statedb, slot)
		if readErr != nil || got.String() != want {
			t.Fatalf("unexpected K slot %s: have %v want %s err=%v", slot, got, want, readErr)
		}
	}
}

func TestPrepareKTransitionRejectsCorruptWindowState(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*state.StateDB)
	}{
		{
			name: "warmup cursor mismatch",
			setup: func(statedb *state.StateDB) {
				statedb.SetState(usdbstate.SystemStateAddress, usdbstate.KWindowCountSlot, common.BigToHash(big.NewInt(1)))
			},
		},
		{
			name: "warmup sum exceeds initialized bounds",
			setup: func(statedb *state.StateDB) {
				statedb.SetState(usdbstate.SystemStateAddress, usdbstate.KWindowSumSlot, common.BigToHash(big.NewInt(1)))
			},
		},
		{
			name: "warmup next slot already initialized",
			setup: func(statedb *state.StateDB) {
				statedb.SetState(usdbstate.SystemStateAddress, usdbstate.KCERingSlot(0), common.BigToHash(big.NewInt(1)))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statedb := newTestStateDB(t)
			test.setup(statedb)
			if _, writes, err := prepareKTransition(
				usdb.CollaborationEfficiencyPolicyVersionV1,
				statedb,
				big.NewInt(1),
			); err == nil || len(writes) != 0 {
				t.Fatalf("corrupt K state was accepted: writes=%v err=%v", writes, err)
			}
		})
	}
}

func TestPrepareFixedPriceTransitionUsesParentStateAndWritesActiveRange(t *testing.T) {
	config := newTestUSDBRewardChainConfig()
	next := config.USDB.Activations[0]
	next.Block = 10
	config.USDB.Activations = append(config.USDB.Activations, next)
	statedb := newTestStateDB(t)
	initializeTestUSDBRewardState(t, statedb, config, big.NewInt(0))

	price, writes, err := prepareFixedPriceTransition(
		config,
		&config.USDB.Activations[1],
		statedb,
		10,
	)
	if err != nil {
		t.Fatalf("fixed-price activation transition failed: %v", err)
	}
	if price.Cmp(usdb.FixedPriceAtomsPerBTCV1()) != 0 {
		t.Fatalf("reward did not use parent fixed price: %s", price)
	}
	for _, write := range writes {
		statedb.SetState(usdbstate.SystemStateAddress, write.slot, write.value)
	}
	wantRange, err := usdb.FixedPriceRangeIDV1(config.ChainID, 10)
	if err != nil {
		t.Fatalf("failed to derive active range: %v", err)
	}
	if got := statedb.GetState(usdbstate.SystemStateAddress, usdbstate.PricePolicyRangeIDSlot); got != wantRange {
		t.Fatalf("child price range mismatch: have %s want %s", got, wantRange)
	}
}

func TestPrepareFixedPriceTransitionRejectsParentStateMismatch(t *testing.T) {
	config := newTestUSDBRewardChainConfig()
	statedb := newTestStateDB(t)
	initializeTestUSDBRewardState(t, statedb, config, big.NewInt(0))
	statedb.SetState(usdbstate.SystemStateAddress, usdbstate.PriceAtomsPerBTCSlot, common.BigToHash(big.NewInt(1)))

	price, writes, err := prepareFixedPriceTransition(
		config,
		&config.USDB.Activations[0],
		statedb,
		1,
	)
	if err == nil || price != nil || len(writes) != 0 {
		t.Fatalf("invalid parent price state was accepted: price=%v writes=%v err=%v", price, writes, err)
	}
}

func TestFinalizeAndAssembleRejectsUnsupportedUSDBRewardVersion(t *testing.T) {
	config := newTestUSDBRewardChainConfig()
	config.USDB.Activations[0].Versions.RewardRuleVersion = 2
	engine := &Ethash{
		config:              Config{Log: log.Root()},
		usdbProfileResolver: &stubProfileResolver{resolved: &usdb.ResolvedConsensusProfile{}},
	}
	header := &types.Header{
		Number:    big.NewInt(1),
		Extra:     newTestPayloadBytes(t),
		UncleHash: types.EmptyUncleHash,
	}
	block, err := engine.FinalizeAndAssemble(
		&stubChainHeaderReader{config: config},
		header,
		newTestStateDB(t),
		nil,
		nil,
		nil,
	)
	if err == nil || block != nil || !strings.Contains(err.Error(), "unsupported USDB reward rule version") {
		t.Fatalf("unsupported reward version was not rejected: block=%v err=%v", block, err)
	}
}

func TestFinalizeLeavesStateUnchangedWhenUsdbVerifierFails(t *testing.T) {
	coinbase := common.HexToAddress("0x1003")
	engine := &Ethash{
		config: Config{
			Log: log.Root(),
		},
		usdbProfileResolver: &stubProfileResolver{err: errInvalidPoW},
	}
	header := &types.Header{
		Number:   big.NewInt(1),
		Coinbase: coinbase,
		Extra:    newTestPayloadBytes(t),
	}
	statedb := newTestStateDB(t)
	chain := &stubChainHeaderReader{config: newTestUSDBChainConfig()}

	engine.Finalize(chain, header, statedb, nil, nil)
	if got := statedb.GetBalance(coinbase); got.Sign() != 0 {
		t.Fatalf("unexpected miner balance after finalize failure: %s", got)
	}
	if header.Root == (common.Hash{}) {
		t.Fatalf("expected finalize to still compute a state root")
	}
}

func TestFinalizeUsesLegacyRewardWhenChainConfigDoesNotActivateUsdb(t *testing.T) {
	coinbase := common.HexToAddress("0x1004")
	resolver := &stubProfileResolver{err: errors.New("must not be called")}
	engine := &Ethash{
		config:              Config{Log: log.Root()},
		usdbProfileResolver: resolver,
	}
	header := &types.Header{
		Number:   big.NewInt(1),
		Coinbase: coinbase,
	}
	statedb := newTestStateDB(t)
	chain := &stubChainHeaderReader{config: &params.ChainConfig{}}

	block, err := engine.FinalizeAndAssemble(chain, header, statedb, nil, nil, nil)
	if err != nil {
		t.Fatalf("legacy finalization failed: %v", err)
	}
	if block == nil {
		t.Fatal("expected legacy block to be assembled")
	}
	if resolver.calls != 0 {
		t.Fatalf("USDB resolver called %d times on inactive chain", resolver.calls)
	}
	if got := statedb.GetBalance(coinbase); got.Cmp(FrontierBlockReward) != 0 {
		t.Fatalf("unexpected legacy reward: have %s want %s", got, FrontierBlockReward)
	}
}
