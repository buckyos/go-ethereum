package usdb

import (
	"errors"
	"math/big"
	"testing"
)

func TestCoinbaseEmissionV1GoldenVectors(t *testing.T) {
	vectors := []struct {
		id          string
		totalSats   string
		price       string
		issued      string
		kBps        uint64
		target      string
		remaining   string
		emission    string
		issuedAfter string
	}{
		{
			id:          "zero-total",
			totalSats:   "0",
			price:       FixedPriceAtomsPerBTCDecimalV1,
			issued:      "0",
			kBps:        10_000,
			target:      "0",
			remaining:   "0",
			emission:    "0",
			issuedAfter: "0",
		},
		{
			id:          "issued-above-target",
			totalSats:   "100000000",
			price:       FixedPriceAtomsPerBTCDecimalV1,
			issued:      "200000000000000000000000",
			kBps:        10_000,
			target:      "100000000000000000000000",
			remaining:   "0",
			emission:    "0",
			issuedAfter: "200000000000000000000000",
		},
		{
			id:          "baseline-one-btc",
			totalSats:   "100000000",
			price:       FixedPriceAtomsPerBTCDecimalV1,
			issued:      "0",
			kBps:        10_000,
			target:      "100000000000000000000000",
			remaining:   "100000000000000000000000",
			emission:    "634195839675291730",
			issuedAfter: "634195839675291730",
		},
		{
			id:          "penalized-partial-target",
			totalSats:   "1234567890",
			price:       FixedPriceAtomsPerBTCDecimalV1,
			issued:      "234567890000000000000000",
			kBps:        8_001,
			target:      "1234567890000000000000000",
			remaining:   "1000000000000000000000000",
			emission:    "5074200913242009132",
			issuedAfter: "234572964200913242009132",
		},
		{
			id:          "single-sat-rounding",
			totalSats:   "1",
			price:       FixedPriceAtomsPerBTCDecimalV1,
			issued:      "0",
			kBps:        10_000,
			target:      "1000000000000000",
			remaining:   "1000000000000000",
			emission:    "6341958396",
			issuedAfter: "6341958396",
		},
		{
			id:          "remaining-one-atom",
			totalSats:   "2100000000000000",
			price:       FixedPriceAtomsPerBTCDecimalV1,
			issued:      "2099999999999999999999999999999",
			kBps:        20_000,
			target:      "2100000000000000000000000000000",
			remaining:   "1",
			emission:    "0",
			issuedAfter: "2099999999999999999999999999999",
		},
	}
	for _, vector := range vectors {
		t.Run(vector.id, func(t *testing.T) {
			result, err := CalculateCoinbaseEmissionV1(
				mustDecimal(t, vector.totalSats),
				mustDecimal(t, vector.price),
				mustDecimal(t, vector.issued),
				vector.kBps,
			)
			if err != nil {
				t.Fatalf("CalculateCoinbaseEmissionV1 failed: %v", err)
			}
			assertDecimalEqual(t, "target", result.TargetSupplyAtoms, vector.target)
			assertDecimalEqual(t, "remaining", result.RemainingTargetAtoms, vector.remaining)
			assertDecimalEqual(t, "emission", result.CoinbaseEmissionAtoms, vector.emission)
			assertDecimalEqual(t, "issued_after", result.IssuedUSDBAtomsAfter, vector.issuedAfter)
		})
	}
}

func TestCalculateKBpsV1GoldenVectors(t *testing.T) {
	vectors := []struct {
		current string
		average string
		want    uint64
	}{
		{current: "0", average: "0", want: 10_000},
		{current: "100", average: "0", want: 10_000},
		{current: "0", average: "100", want: 8_001},
		{current: "50", average: "100", want: 9_090},
		{current: "99", average: "100", want: 9_983},
		{current: "100", average: "100", want: 10_000},
		{current: "150", average: "100", want: 15_000},
		{current: "200", average: "100", want: 20_000},
		{current: "201", average: "100", want: 20_000},
	}
	for _, vector := range vectors {
		got, err := CalculateKBpsV1(mustDecimal(t, vector.current), mustDecimal(t, vector.average))
		if err != nil {
			t.Fatalf("CalculateKBpsV1(%s,%s) failed: %v", vector.current, vector.average, err)
		}
		if got != vector.want {
			t.Fatalf("CalculateKBpsV1(%s,%s): have %d want %d", vector.current, vector.average, got, vector.want)
		}
	}
}

func TestSplitTransactionFeeV1GoldenVectors(t *testing.T) {
	vectors := []struct {
		fee   string
		miner string
		dao   string
	}{
		{fee: "0", miner: "0", dao: "0"},
		{fee: "1", miner: "1", dao: "0"},
		{fee: "2", miner: "2", dao: "0"},
		{fee: "3", miner: "2", dao: "1"},
		{fee: "10001", miner: "6001", dao: "4000"},
	}
	for _, vector := range vectors {
		split, err := SplitTransactionFeeV1(mustDecimal(t, vector.fee))
		if err != nil {
			t.Fatalf("SplitTransactionFeeV1(%s) failed: %v", vector.fee, err)
		}
		assertDecimalEqual(t, "miner", split.MinerAtoms, vector.miner)
		assertDecimalEqual(t, "dao", split.DAOAtoms, vector.dao)
	}
}

func TestFixedPriceRangeIDV1GoldenVector(t *testing.T) {
	rangeID, err := FixedPriceRangeIDV1(big.NewInt(20_260_323), 0)
	if err != nil {
		t.Fatalf("FixedPriceRangeIDV1 failed: %v", err)
	}
	const expected = "0x2ae45cafae84cc892d1d4354f02a0869f97dfd6ca2c757ba511c57680b8bfaf4"
	if rangeID.Hex() != expected {
		t.Fatalf("range id mismatch: have %s want %s", rangeID, expected)
	}
}

func TestRewardFormulaRejectsInvalidInputs(t *testing.T) {
	maxPlusOne := new(big.Int).Lsh(big.NewInt(1), 256)
	for _, test := range []struct {
		name   string
		total  *big.Int
		price  *big.Int
		issued *big.Int
		kBps   uint64
	}{
		{name: "nil total", total: nil, price: big.NewInt(1), issued: big.NewInt(0), kBps: KBpsBase},
		{name: "negative total", total: big.NewInt(-1), price: big.NewInt(1), issued: big.NewInt(0), kBps: KBpsBase},
		{name: "total overflow", total: new(big.Int).Lsh(big.NewInt(1), 64), price: big.NewInt(1), issued: big.NewInt(0), kBps: KBpsBase},
		{name: "zero price", total: big.NewInt(1), price: big.NewInt(0), issued: big.NewInt(0), kBps: KBpsBase},
		{name: "price overflow", total: big.NewInt(1), price: maxPlusOne, issued: big.NewInt(0), kBps: KBpsBase},
		{name: "issued overflow", total: big.NewInt(1), price: big.NewInt(1), issued: maxPlusOne, kBps: KBpsBase},
		{name: "low K", total: big.NewInt(1), price: big.NewInt(1), issued: big.NewInt(0), kBps: KBpsMin - 1},
		{name: "high K", total: big.NewInt(1), price: big.NewInt(1), issued: big.NewInt(0), kBps: KBpsMax + 1},
		{name: "target overflow", total: new(big.Int).SetUint64(^uint64(0)), price: new(big.Int).Sub(maxPlusOne, big.NewInt(1)), issued: big.NewInt(0), kBps: KBpsBase},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := CalculateCoinbaseEmissionV1(test.total, test.price, test.issued, test.kBps); err == nil {
				t.Fatal("invalid formula input was accepted")
			}
		})
	}
	if _, err := SplitTransactionFeeV1(big.NewInt(-1)); !errors.Is(err, ErrInvalidRewardInput) {
		t.Fatalf("negative fee returned unexpected error: %v", err)
	}
	if _, err := CalculateKBpsV1(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1)); !errors.Is(err, ErrInvalidRewardInput) {
		t.Fatalf("energy overflow returned unexpected error: %v", err)
	}
}

func mustDecimal(t *testing.T, value string) *big.Int {
	t.Helper()
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok {
		t.Fatalf("invalid test decimal %q", value)
	}
	return parsed
}

func assertDecimalEqual(t *testing.T, field string, got *big.Int, want string) {
	t.Helper()
	if got == nil || got.String() != want {
		t.Fatalf("%s: have %v want %s", field, got, want)
	}
}
