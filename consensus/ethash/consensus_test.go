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
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
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

type stubProfileResolver struct {
	resolved  *usdb.ResolvedConsensusProfile
	err       error
	lastExtra []byte
	calls     int
}

func (s *stubProfileResolver) ResolveProfile(_ context.Context, headerExtra []byte) (*usdb.ResolvedConsensusProfile, error) {
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

	payload, err := usdb.NewProfileSelectorPayload(
		usdb.DifficultyPolicyVersionV1,
		123,
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
			BTCActivationRegistryID: usdb.BTCRegtestActivationRegistryIDV1,
			Activations: []params.USDBConsensusActivation{{
				Versions: params.USDBConsensusVersions{
					PayloadVersion:          usdb.ProfileSelectorPayloadVersionV1,
					DifficultyPolicyVersion: usdb.DifficultyPolicyVersionV1,
				},
			}},
		},
	}
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
	resolver := &stubProfileResolver{resolved: &usdb.ResolvedConsensusProfile{DifficultyFactorBps: usdb.BasisPointDenominator}}
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

func TestVerifyHeaderUsesExpectedVersionAtActivationBoundary(t *testing.T) {
	config := newTestUSDBChainConfig()
	config.USDB.Activations = append(config.USDB.Activations, params.USDBConsensusActivation{
		Block: 2,
		Versions: params.USDBConsensusVersions{
			PayloadVersion:          usdb.ProfileSelectorPayloadVersionV1,
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
	resolver := &stubProfileResolver{resolved: &usdb.ResolvedConsensusProfile{DifficultyFactorBps: usdb.BasisPointDenominator}}
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
	resolver := &stubProfileResolver{resolved: &usdb.ResolvedConsensusProfile{DifficultyFactorBps: 9_900}}
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
	profile := &usdb.ResolvedConsensusProfile{DifficultyFactorBps: usdb.BasisPointDenominator}
	for _, policy := range []*params.USDBConsensusVersions{
		{DifficultyPolicyVersion: 2},
		{DifficultyPolicyVersion: usdb.DifficultyPolicyVersionV1, QuotePolicyVersion: 1},
	} {
		if _, err := applyUSDBDifficultyPolicy(policy, big.NewInt(100), profile); err == nil {
			t.Fatalf("unimplemented policy unexpectedly accepted: %+v", policy)
		}
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

func TestFinalizeAndAssembleRejectsUnimplementedRewardActivation(t *testing.T) {
	coinbase := common.HexToAddress("0x1005")
	config := newTestUSDBChainConfig()
	config.USDB.Activations[0].Versions.RewardRuleVersion = 1
	config.USDB.Activations[0].Versions.CoinbaseEmissionPolicyVersion = 1
	engine := &Ethash{
		config:              Config{Log: log.Root()},
		usdbProfileResolver: &stubProfileResolver{resolved: &usdb.ResolvedConsensusProfile{}},
	}
	header := &types.Header{Number: big.NewInt(1), Coinbase: coinbase, Extra: newTestPayloadBytes(t)}
	statedb := newTestStateDB(t)
	chain := &stubChainHeaderReader{config: config}

	block, err := engine.FinalizeAndAssemble(chain, header, statedb, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported usdb reward policies") {
		t.Fatalf("unimplemented reward activation did not fail closed: block=%v err=%v", block, err)
	}
	if got := statedb.GetBalance(coinbase); got.Sign() != 0 {
		t.Fatalf("unimplemented reward activation changed balance: %s", got)
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
