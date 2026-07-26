package usdbstate

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestReservedSlotGoldenVectors(t *testing.T) {
	vectors := []struct {
		name string
		got  common.Hash
		want string
	}{
		{"schema version", SystemStateSchemaVersionSlot, "0x173335b1b35d7ee82b9e595fe4798c8e777ab08ec72cb8ea8a8035ee1fade3b1"},
		{"issued supply", IssuedUSDBAtomsSlot, "0xdd1651483272028cad87b8ab291a694a9deb1d7f6b60efe175f823c406233da2"},
		{"price", PriceAtomsPerBTCSlot, "0xdf22b00cd5b1ebfe143e44347701e86394b9867790d8d631d43ef36dd099f884"},
		{"real price", RealPriceAtomsPerBTCSlot, "0xca9c2c48cf84f8c36afc338940b0e06484e790e7190e255a57245056399bb792"},
		{"price policy", PricePolicyVersionSlot, "0xc65fb6e80dc7887c39c44824450f50076c21ffb398bc3abc8ec122d277f7ce03"},
		{"price source", PriceSourceKindSlot, "0x93fbc84343f98a946b33b6067ae017273d92029de3e58c3b3c6d37fb033cac9a"},
		{"price range", PricePolicyRangeIDSlot, "0xc3faa41e87f1db8d882f1a24fd36bf5f7f873e141845019088d03d0e2f487697"},
		{"K sum", KWindowSumSlot, "0xa05125c861ef555402b28fe982e4e36ddd9572a49576081d06ad23fbdcd9a3ae"},
		{"K count", KWindowCountSlot, "0x40db96c2e761efb468bcae40739cb9d71d15e53f4b46a977d213476493a0ecea"},
		{"K cursor", KWindowCursorSlot, "0xc71798c59dae3ab826f28ffa3db501face181bd2d88225baadcb87ea950c53b2"},
		{"K ring", KCERingSlotBase, "0x0c0b1b7c7641949e2f45575f48d889a70298842709e50c1070010b910fb3bc31"},
		{"K last CE", KLastCESlot, "0x1d2465ef2bfb872650e27eeb6a1327cb569d58e4fd2c4867eb4b8f38b922905c"},
		{"K last AE", KLastAESlot, "0xb4d89df049af3068c7073e80bf4918d5606bffb9df517e96c1f996f942c38f58"},
		{"K last bps", KLastKBpsSlot, "0x53264b8f3aab69de54c5a4ecadabdbff09c07064034e8fcfdb79056a55dd9954"},
		{"quote policy", QuotePolicyVersionSlot, "0x06ed1ff69c0a83234a648936403718a01fd0c0e6caabe4eea61d7735f63db832"},
		{"quote window", LeaderQuoteWindowBlocksSlot, "0x34d422b9f7b2447c9ad568159320894837919eacfd196ee5c5ede41376c56358"},
		{"quote map", LeaderLastValidQuoteBlockMapBase, "0x9f4c948c72431d7f43911f1f1231509866c87a43729568fdf10a86f9291b9cba"},
	}
	for _, vector := range vectors {
		if got := vector.got.Hex(); got != vector.want {
			t.Fatalf("%s slot mismatch: have %s want %s", vector.name, got, vector.want)
		}
	}
}

func TestDynamicSlotGoldenVectors(t *testing.T) {
	if got, want := KCERingSlot(0).Hex(), "0xb219b1bbf4732ed92adf0117a200e771f975c506d1774f83bd9ceca8d40b47af"; got != want {
		t.Fatalf("K ring slot mismatch: have %s want %s", got, want)
	}
	txID := common.HexToHash("0x0101010101010101010101010101010101010101010101010101010101010101")
	if got, want := LeaderQuoteSubjectKey(txID, 7).Hex(), "0x843e3be447dd1809885dc50b1f54731391166abab16119510ad06d4bb586e422"; got != want {
		t.Fatalf("quote subject mismatch: have %s want %s", got, want)
	}
	if got, want := LeaderLastValidQuoteBlockSlot(txID, 7).Hex(), "0x50bdd3b511e9a1d9f70ca8cc57c354965ef411fcac0be5754bf0eca94ce0de25"; got != want {
		t.Fatalf("quote slot mismatch: have %s want %s", got, want)
	}
}

func TestGenesisStorageAndUint256Bounds(t *testing.T) {
	issued := new(big.Int).Lsh(big.NewInt(1), 255)
	storage, err := GenesisStorage(issued)
	if err != nil {
		t.Fatalf("GenesisStorage failed: %v", err)
	}
	if got := storage[SystemStateSchemaVersionSlot]; got != common.BigToHash(big.NewInt(1)) {
		t.Fatalf("schema version mismatch: %s", got)
	}
	if got := storage[IssuedUSDBAtomsSlot]; got != common.BigToHash(issued) {
		t.Fatalf("issued supply mismatch: %s", got)
	}

	for _, invalid := range []*big.Int{
		nil,
		big.NewInt(-1),
		new(big.Int).Lsh(big.NewInt(1), 256),
	} {
		if _, err := EncodeUint256(invalid); err == nil {
			t.Fatalf("EncodeUint256 accepted invalid value %v", invalid)
		}
	}
}
