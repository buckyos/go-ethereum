// Package usdbstate defines the consensus-owned USDB system account and its
// reserved storage layout.
package usdbstate

import (
	"encoding/binary"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

const (
	// SystemStateAddressHex is the network-independent reserved account used by
	// USDB consensus state transitions.
	SystemStateAddressHex = "0x0000000000000000000000000000000000001000"
	// SystemStateNonce prevents CREATE and CREATE2 from deploying code at the
	// reserved system-state account.
	SystemStateNonce uint64 = 1
	// SystemStateSchemaVersion identifies the initial storage namespace.
	SystemStateSchemaVersion uint64 = 1

	systemStateSchemaVersionDomain = "usdb.system.state.v1/meta/schema-version"
	issuedUSDBAtomsDomain          = "usdb.system.state.v1/reward/issued-usdb-atoms"

	priceAtomsPerBTCDomain       = "usdb.system.state.v1/price/price-atoms-per-btc"
	realPriceAtomsPerBTCDomain   = "usdb.system.state.v1/price/real-price-atoms-per-btc"
	pricePolicyVersionDomain     = "usdb.system.state.v1/price/policy-version"
	priceSourceKindDomain        = "usdb.system.state.v1/price/source-kind"
	pricePolicyRangeIDDomain     = "usdb.system.state.v1/price/policy-range-id"
	kWindowSumDomain             = "usdb.system.state.v1/collaboration/window-sum"
	kWindowCountDomain           = "usdb.system.state.v1/collaboration/window-count"
	kWindowCursorDomain          = "usdb.system.state.v1/collaboration/window-cursor"
	kCERingBaseDomain            = "usdb.system.state.v1/collaboration/ce-ring"
	kLastCEDomain                = "usdb.system.state.v1/collaboration/last-ce"
	kLastAEDomain                = "usdb.system.state.v1/collaboration/last-ae"
	kLastKBpsDomain              = "usdb.system.state.v1/collaboration/last-k-bps"
	quotePolicyVersionDomain     = "usdb.system.state.v1/quote/policy-version"
	quoteWindowBlocksDomain      = "usdb.system.state.v1/quote/window-blocks"
	leaderLastQuoteMapBaseDomain = "usdb.system.state.v1/quote/leader-last-valid-block"

	dividendBootstrapFinalizedDomain = "sourcedao.dividend.bootstrap-finalized:v1"
)

var (
	// SystemStateAddress is the reserved USDB consensus-state account.
	SystemStateAddress = common.HexToAddress(SystemStateAddressHex)

	// SystemStateSchemaVersionSlot records SystemStateSchemaVersion.
	SystemStateSchemaVersionSlot = slotForDomain(systemStateSchemaVersionDomain)
	// IssuedUSDBAtomsSlot records cumulative genesis allocation plus CoinBase emission.
	IssuedUSDBAtomsSlot = slotForDomain(issuedUSDBAtomsDomain)

	// PriceAtomsPerBTCSlot records the algorithmic USDB-atoms-per-BTC price.
	PriceAtomsPerBTCSlot = slotForDomain(priceAtomsPerBTCDomain)
	// RealPriceAtomsPerBTCSlot records the source-derived USDB-atoms-per-BTC price.
	RealPriceAtomsPerBTCSlot = slotForDomain(realPriceAtomsPerBTCDomain)
	// PricePolicyVersionSlot records the active UIP-0013 policy version.
	PricePolicyVersionSlot = slotForDomain(pricePolicyVersionDomain)
	// PriceSourceKindSlot records the active UIP-0013 source kind.
	PriceSourceKindSlot = slotForDomain(priceSourceKindDomain)
	// PricePolicyRangeIDSlot records the immutable active price range identity.
	PricePolicyRangeIDSlot = slotForDomain(pricePolicyRangeIDDomain)

	// KWindowSumSlot records the sum of samples in the UIP-0012 rolling window.
	KWindowSumSlot = slotForDomain(kWindowSumDomain)
	// KWindowCountSlot records the number of initialized samples.
	KWindowCountSlot = slotForDomain(kWindowCountDomain)
	// KWindowCursorSlot records the next ring position to overwrite.
	KWindowCursorSlot = slotForDomain(kWindowCursorDomain)
	// KCERingSlotBase is the mapping base for indexed collaboration samples.
	KCERingSlotBase = slotForDomain(kCERingBaseDomain)
	// KLastCESlot records the most recently accepted CE sample for audit.
	KLastCESlot = slotForDomain(kLastCEDomain)
	// KLastAESlot records the most recently resolved AE value for audit.
	KLastAESlot = slotForDomain(kLastAEDomain)
	// KLastKBpsSlot records the most recently resolved K value for audit.
	KLastKBpsSlot = slotForDomain(kLastKBpsDomain)

	// QuotePolicyVersionSlot records the active UIP-0014 quote policy.
	QuotePolicyVersionSlot = slotForDomain(quotePolicyVersionDomain)
	// LeaderQuoteWindowBlocksSlot is reserved for a future formal UIP-0014
	// quote window. Policy zero and activation conformance do not write it.
	LeaderQuoteWindowBlocksSlot = slotForDomain(quoteWindowBlocksDomain)
	// LeaderLastValidQuoteBlockMapBase is reserved for future per-pass quote
	// state. Its slot identity does not activate or freeze mapping semantics.
	LeaderLastValidQuoteBlockMapBase = slotForDomain(leaderLastQuoteMapBaseDomain)

	// DividendBootstrapFinalizedSlot is the SourceDAO Dividend unstructured
	// storage marker that gates UIP-0011 fee splitting.
	DividendBootstrapFinalizedSlot = slotForDomain(dividendBootstrapFinalizedDomain)
)

var maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// StateReader is the subset of EVM state access needed to validate and read
// the reserved USDB system account.
type StateReader interface {
	GetNonce(common.Address) uint64
	GetCodeHash(common.Address) common.Hash
	GetCodeSize(common.Address) int
	GetState(common.Address, common.Hash) common.Hash
}

// StateWriter extends StateReader with deterministic system-slot writes.
type StateWriter interface {
	StateReader
	SetState(common.Address, common.Hash, common.Hash)
}

func slotForDomain(domain string) common.Hash {
	return crypto.Keccak256Hash([]byte(domain))
}

// EncodeUint256 converts a non-negative uint256 into one EVM storage word.
func EncodeUint256(value *big.Int) (common.Hash, error) {
	if value == nil {
		return common.Hash{}, errors.New("USDB system-state uint256 is nil")
	}
	if value.Sign() < 0 {
		return common.Hash{}, errors.New("USDB system-state uint256 is negative")
	}
	if value.Cmp(maxUint256) > 0 {
		return common.Hash{}, errors.New("USDB system-state uint256 overflows 256 bits")
	}
	return common.BigToHash(value), nil
}

// GenesisStorage returns the mandatory v1 system-state words for a genesis
// allocation. Zero-valued future policy slots remain absent from the trie.
func GenesisStorage(issuedUSDBAtoms *big.Int) (map[common.Hash]common.Hash, error) {
	issued, err := EncodeUint256(issuedUSDBAtoms)
	if err != nil {
		return nil, err
	}
	return map[common.Hash]common.Hash{
		SystemStateSchemaVersionSlot: common.BigToHash(new(big.Int).SetUint64(SystemStateSchemaVersion)),
		IssuedUSDBAtomsSlot:          issued,
	}, nil
}

// ValidateSystemAccount checks the immutable runtime account and schema invariants.
func ValidateSystemAccount(state StateReader) error {
	if state == nil {
		return errors.New("USDB system-state reader is nil")
	}
	if nonce := state.GetNonce(SystemStateAddress); nonce != SystemStateNonce {
		return errors.New("USDB system-state account has invalid nonce")
	}
	if codeSize := state.GetCodeSize(SystemStateAddress); codeSize != 0 {
		return errors.New("USDB system-state account must not contain code")
	}
	schema := state.GetState(SystemStateAddress, SystemStateSchemaVersionSlot)
	if schema != common.BigToHash(new(big.Int).SetUint64(SystemStateSchemaVersion)) {
		return errors.New("USDB system-state account has incompatible schema")
	}
	return nil
}

// ValidateDividendReadiness checks the consensus-readable UIP-0010 fee gate.
func ValidateDividendReadiness(state StateReader, address common.Address, expectedCodeHash common.Hash) error {
	if state == nil {
		return errors.New("USDB Dividend state reader is nil")
	}
	if address == (common.Address{}) || expectedCodeHash == (common.Hash{}) {
		return errors.New("USDB Dividend identity is incomplete")
	}
	if state.GetCodeSize(address) == 0 || state.GetCodeHash(address) != expectedCodeHash {
		return errors.New("USDB Dividend runtime code does not match chain config")
	}
	if state.GetState(address, DividendBootstrapFinalizedSlot) != common.BigToHash(big.NewInt(1)) {
		return errors.New("USDB Dividend bootstrap is not finalized")
	}
	return nil
}

// ResolveDividendFeeGate validates chain-config identity and, once its height is
// reached, requires the on-chain bootstrap readiness marker.
func ResolveDividendFeeGate(state StateReader, config *params.ChainConfig, blockNumber *big.Int) (bool, error) {
	if config == nil || blockNumber == nil || blockNumber.Sign() < 0 {
		return false, errors.New("invalid USDB Dividend fee-gate context")
	}
	if config.DividendFeeSplitBlock == nil ||
		config.DividendFeeSplitBlock.Sign() <= 0 ||
		config.DividendAddress == (common.Address{}) ||
		config.DividendCodeHash == (common.Hash{}) {
		return false, errors.New("incomplete USDB Dividend fee-split config")
	}
	if blockNumber.Cmp(config.DividendFeeSplitBlock) < 0 {
		return false, nil
	}
	if err := ValidateDividendReadiness(state, config.DividendAddress, config.DividendCodeHash); err != nil {
		return false, err
	}
	return true, nil
}

// ReadUint256 reads one reserved system slot as an unsigned integer.
func ReadUint256(state StateReader, slot common.Hash) (*big.Int, error) {
	if state == nil {
		return nil, errors.New("USDB system-state reader is nil")
	}
	word := state.GetState(SystemStateAddress, slot)
	return new(big.Int).SetBytes(word[:]), nil
}

// WriteUint256 validates and writes one reserved system slot.
func WriteUint256(state StateWriter, slot common.Hash, value *big.Int) error {
	if state == nil {
		return errors.New("USDB system-state writer is nil")
	}
	word, err := EncodeUint256(value)
	if err != nil {
		return err
	}
	state.SetState(SystemStateAddress, slot, word)
	return nil
}

// ApplyFixedPriceState writes one fully resolved UIP-0013 fixed-price state.
func ApplyFixedPriceState(storage map[common.Hash]common.Hash, price *big.Int, policyVersion, sourceKind uint32, rangeID common.Hash) error {
	if storage == nil {
		return errors.New("USDB fixed-price storage is nil")
	}
	if policyVersion == 0 || sourceKind == 0 || rangeID == (common.Hash{}) {
		return errors.New("USDB fixed-price identity is incomplete")
	}
	priceWord, err := EncodeUint256(price)
	if err != nil {
		return err
	}
	storage[PriceAtomsPerBTCSlot] = priceWord
	storage[RealPriceAtomsPerBTCSlot] = priceWord
	storage[PricePolicyVersionSlot] = common.BigToHash(new(big.Int).SetUint64(uint64(policyVersion)))
	storage[PriceSourceKindSlot] = common.BigToHash(new(big.Int).SetUint64(uint64(sourceKind)))
	storage[PricePolicyRangeIDSlot] = rangeID
	return nil
}

// MappingStorageSlot derives the Solidity-compatible storage position
// keccak256(key32 || baseSlot32).
func MappingStorageSlot(key, baseSlot common.Hash) common.Hash {
	return crypto.Keccak256Hash(key[:], baseSlot[:])
}

// KCERingSlot derives the reserved storage position for one UIP-0012 sample.
func KCERingSlot(index uint64) common.Hash {
	return MappingStorageSlot(common.BigToHash(new(big.Int).SetUint64(index)), KCERingSlotBase)
}

// LeaderQuoteSubjectKey hashes the canonical UIP-0007 pass id encoding: the
// 32 bytes decoded left-to-right from the display transaction id, without a
// Bitcoin internal-byte-order reversal, followed by a big-endian uint32 index.
func LeaderQuoteSubjectKey(txID common.Hash, index uint32) common.Hash {
	encoded := make([]byte, common.HashLength+4)
	copy(encoded, txID[:])
	binary.BigEndian.PutUint32(encoded[common.HashLength:], index)
	return crypto.Keccak256Hash(encoded)
}

// LeaderLastValidQuoteBlockSlot derives the quote-state slot for one pass.
func LeaderLastValidQuoteBlockSlot(txID common.Hash, index uint32) common.Hash {
	return MappingStorageSlot(
		LeaderQuoteSubjectKey(txID, index),
		LeaderLastValidQuoteBlockMapBase,
	)
}
