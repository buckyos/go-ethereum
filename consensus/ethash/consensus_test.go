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
	"context"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"
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
}

func (s *stubChainHeaderReader) Config() *params.ChainConfig                 { return s.config }
func (s *stubChainHeaderReader) CurrentHeader() *types.Header                { return nil }
func (s *stubChainHeaderReader) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (s *stubChainHeaderReader) GetHeaderByNumber(uint64) *types.Header      { return nil }
func (s *stubChainHeaderReader) GetHeaderByHash(common.Hash) *types.Header   { return nil }
func (s *stubChainHeaderReader) GetTd(common.Hash, uint64) *big.Int          { return nil }

type stubRewardVerifier struct {
	resolved    *usdb.ResolvedReward
	err         error
	lastExtra   []byte
	lastBlockNo uint64
}

func (s *stubRewardVerifier) ResolveReward(_ context.Context, headerExtra []byte, blockNumber uint64) (*usdb.ResolvedReward, error) {
	s.lastExtra = append([]byte(nil), headerExtra...)
	s.lastBlockNo = blockNumber
	if s.err != nil {
		return nil, s.err
	}
	return s.resolved, nil
}

func (s *stubRewardVerifier) Close() {}

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

	payload, err := usdb.NewRewardPayloadV1(
		123,
		common.HexToHash("0x1111").Hex(),
		common.HexToHash("0x2222").Hex(),
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

func TestFinalizeAndAssembleUsesUsdbReward(t *testing.T) {
	coinbase := common.HexToAddress("0x1001")
	verifier := &stubRewardVerifier{
		resolved: &usdb.ResolvedReward{
			BaseReward:  big.NewInt(100),
			MinerReward: big.NewInt(250),
		},
	}
	engine := &Ethash{
		config: Config{
			Log:  log.Root(),
			USDB: USDBConfig{Enabled: true},
		},
		usdbRewardVerifier: verifier,
	}
	header := &types.Header{
		Number:   big.NewInt(1),
		Coinbase: coinbase,
		Extra:    newTestPayloadBytes(t),
	}
	statedb := newTestStateDB(t)
	chain := &stubChainHeaderReader{config: params.AllEthashProtocolChanges}

	block, err := engine.FinalizeAndAssemble(chain, header, statedb, nil, nil, nil)
	if err != nil {
		t.Fatalf("FinalizeAndAssemble returned error: %v", err)
	}
	if block == nil {
		t.Fatalf("expected block to be assembled")
	}
	if verifier.lastBlockNo != 1 {
		t.Fatalf("unexpected verifier block number: have %d want 1", verifier.lastBlockNo)
	}
	if got := statedb.GetBalance(coinbase); got.Cmp(big.NewInt(250)) != 0 {
		t.Fatalf("unexpected miner balance: have %s want %s", got, "250")
	}
}

func TestFinalizeAndAssembleReturnsErrorWhenUsdbVerifierFails(t *testing.T) {
	coinbase := common.HexToAddress("0x1002")
	engine := &Ethash{
		config: Config{
			Log:  log.Root(),
			USDB: USDBConfig{Enabled: true},
		},
		usdbRewardVerifier: &stubRewardVerifier{err: errInvalidPoW},
	}
	header := &types.Header{
		Number:   big.NewInt(1),
		Coinbase: coinbase,
		Extra:    newTestPayloadBytes(t),
	}
	statedb := newTestStateDB(t)
	chain := &stubChainHeaderReader{config: params.AllEthashProtocolChanges}

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

func TestFinalizeLeavesStateUnchangedWhenUsdbVerifierFails(t *testing.T) {
	coinbase := common.HexToAddress("0x1003")
	engine := &Ethash{
		config: Config{
			Log:  log.Root(),
			USDB: USDBConfig{Enabled: true},
		},
		usdbRewardVerifier: &stubRewardVerifier{err: errInvalidPoW},
	}
	header := &types.Header{
		Number:   big.NewInt(1),
		Coinbase: coinbase,
		Extra:    newTestPayloadBytes(t),
	}
	statedb := newTestStateDB(t)
	chain := &stubChainHeaderReader{config: params.AllEthashProtocolChanges}

	engine.Finalize(chain, header, statedb, nil, nil)
	if got := statedb.GetBalance(coinbase); got.Sign() != 0 {
		t.Fatalf("unexpected miner balance after finalize failure: %s", got)
	}
	if header.Root == (common.Hash{}) {
		t.Fatalf("expected finalize to still compute a state root")
	}
}
