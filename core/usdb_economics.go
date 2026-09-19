package core

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/usdbstate"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
)

// ErrUSDBEconomicsUnsupported prevents unknown rules from acquiring a v1 qualification.
var ErrUSDBEconomicsUnsupported = errors.New("unsupported USDB economics policy")

// USDBEconomicsSelector identifies the historical Miner Pass used by this block.
type USDBEconomicsSelector struct {
	PassID        string `json:"pass_id"`
	BTCHeight     uint32 `json:"btc_height"`
	BTCAnchorAge  uint32 `json:"btc_anchor_age_blocks"`
	SnapshotID    string `json:"snapshot_id"`
	SystemStateID string `json:"system_state_id"`
	Registry      string `json:"activation_registry_id"`
}

// USDBTransactionEconomics records refund-adjusted fees, including reverted calls.
// Atom amounts are decimal strings to preserve uint256 precision in browsers.
type USDBTransactionEconomics struct {
	Hash     common.Hash `json:"hash"`
	Status   uint64      `json:"status"`
	GasUsed  string      `json:"gas_used"`
	GasPrice string      `json:"effective_gas_price_atoms"`
	Fee      string      `json:"fee_atoms"`
	MinerFee string      `json:"miner_fee_atoms"`
	DAOFee   string      `json:"dao_fee_atoms"`
	Route    string      `json:"fee_route"`
}

// USDBBlockEconomics is a block-bound replay report, not a circulating-supply estimate.
type USDBBlockEconomics struct {
	Schema       string                        `json:"schema_version"`
	Status       string                        `json:"status"`
	Hash         common.Hash                   `json:"block_hash"`
	Number       string                        `json:"block_number"`
	ParentHash   common.Hash                   `json:"parent_hash"`
	StateRoot    common.Hash                   `json:"state_root"`
	ReceiptsRoot common.Hash                   `json:"receipts_root"`
	Versions     *params.USDBConsensusVersions `json:"versions,omitempty"`
	Selector     *USDBEconomicsSelector        `json:"selector,omitempty"`
	Miner        common.Address                `json:"miner"`
	Dividend     common.Address                `json:"dividend"`
	Amounts      *USDBEconomicAmounts          `json:"amounts,omitempty"`
	Transactions []USDBTransactionEconomics    `json:"transactions"`
}

// USDBEconomicAmounts separates new emission from redistributed transaction fees.
type USDBEconomicAmounts struct {
	IssuedBefore  string `json:"issued_before_atoms"`
	IssuedAfter   string `json:"issued_after_atoms"`
	Emission      string `json:"emission_atoms"`
	MinerEmission string `json:"miner_emission_atoms"`
	Fees          string `json:"fees_atoms"`
	MinerFees     string `json:"miner_fees_atoms"`
	DAOFees       string `json:"dao_fees_atoms"`
}

// USDBEconomicsIdentity returns metadata without assigning any economic amounts.
func USDBEconomicsIdentity(block *types.Block) *USDBBlockEconomics {
	return &USDBBlockEconomics{Schema: "usdb-block-economics:v1", Hash: block.Hash(),
		Number: strconv.FormatUint(block.NumberU64(), 10), ParentHash: block.ParentHash(),
		StateRoot: block.Root(), ReceiptsRoot: block.ReceiptHash(), Miner: block.Coinbase(),
		Transactions: []USDBTransactionEconomics{}}
}

// ReplayUSDBEconomics executes one block against a disposable parent state. It never
// commits state, imports blocks or changes consensus rules. A report is released only
// after the replay matches the committed state root, receipt root, bloom and gas.
func ReplayUSDBEconomics(ctx context.Context, chain consensus.ChainHeaderReader, engine consensus.Engine, block *types.Block, parentState *state.StateDB) (*USDBBlockEconomics, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, header := chain.Config(), block.Header()
	activation, err := config.USDBActivationAt(block.NumberU64())
	if err != nil {
		return nil, err
	}
	if block.NumberU64() == 0 || activation == nil {
		return nil, ErrUSDBEconomicsUnsupported
	}
	v := activation.Versions
	if v.RewardRuleVersion != 1 || v.CoinbaseEmissionPolicyVersion != 1 || v.FeeSplitPolicyVersion > 1 ||
		v.CollaborationEfficiencyPolicyVersion > 1 || v.PricePolicyVersion != 1 || v.AuxPoolPolicyVersion != 0 ||
		v.QuotePolicyVersion != 0 || v.BTCAnchorPolicyVersion != 1 || v.DifficultyPolicyVersion != 1 {
		return nil, ErrUSDBEconomicsUnsupported
	}
	if err := usdb.ValidateProfileSelectorPayload(header.Extra, v.PayloadVersion, v.DifficultyPolicyVersion); err != nil {
		return nil, err
	}
	var selector usdb.ProfileSelectorPayload
	if err := selector.UnmarshalBinary(header.Extra); err != nil {
		return nil, err
	}
	parent := chain.GetHeader(block.ParentHash(), block.NumberU64()-1)
	if parent == nil {
		return nil, errors.New("economics parent state mismatch")
	}
	parentRoot := parentState.IntermediateRoot(config.IsEIP158(parent.Number))
	if err := parentState.Error(); err != nil {
		return nil, err
	}
	if parentRoot != parent.Root {
		return nil, errors.New("economics parent state mismatch")
	}
	statedb := parentState.Copy()
	if err := usdbstate.ValidateSystemAccount(statedb); err != nil {
		return nil, economicsStateError(statedb, err)
	}
	issuedBefore, err := usdbstate.ReadUint256(statedb, usdbstate.IssuedUSDBAtomsSlot)
	if err != nil {
		return nil, err
	}
	report := USDBEconomicsIdentity(block)
	report.Versions, report.Dividend = &v, config.DividendAddress
	report.Selector = &USDBEconomicsSelector{PassID: selector.PassID.String(), BTCHeight: selector.BTCHeight,
		BTCAnchorAge: selector.BTCAnchorAgeBlocks, SnapshotID: selector.SnapshotIDHex(),
		SystemStateID: selector.SystemStateIDHex(), Registry: activation.BTCActivationRegistryID}
	if config.DAOForkSupport && config.DAOForkBlock != nil && config.DAOForkBlock.Cmp(header.Number) == 0 {
		misc.ApplyDAOHardFork(statedb)
	}
	chainContext := economicsChainContext{chain, engine}
	evm := vm.NewEVM(NewEVMBlockContext(header, chainContext, nil), vm.TxContext{}, statedb, config, vm.Config{})
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			evm.Cancel()
		case <-done:
		}
	}()
	gp := new(GasPool).AddGas(block.GasLimit())
	var usedGas uint64
	var receipts types.Receipts
	total, minerTotal, daoTotal := new(big.Int), new(big.Int), new(big.Int)
	for i, tx := range block.Transactions() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Resolve at the same pre-transaction state as TransitionDb, including a
		// possible readiness change by a preceding transaction in this block.
		route, err := resolveTransactionFeeRoute(config, header.Number, statedb)
		if err != nil {
			return nil, economicsStateError(statedb, err)
		}
		if route == legacyTransactionFeeRoute {
			return nil, ErrUSDBEconomicsUnsupported
		}
		msg, err := tx.AsMessage(types.MakeSigner(config, header.Number), header.BaseFee)
		if err != nil {
			return nil, err
		}
		statedb.Prepare(tx.Hash(), i)
		receipt, err := applyTransaction(msg, config, nil, gp, statedb, header.Number, block.Hash(), tx, &usedGas, evm)
		if err != nil {
			return nil, economicsStateError(statedb, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
		fee := new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), msg.GasPrice())
		minerFee, daoFee, routeName := new(big.Int).Set(fee), new(big.Int), "miner_only"
		if route == usdbSplitTransactionFeeRoute {
			split, err := usdb.SplitTransactionFeeV1(fee)
			if err != nil {
				return nil, err
			}
			minerFee, daoFee, routeName = split.MinerAtoms, split.DAOAtoms, "miner_and_dividend"
		}
		total.Add(total, fee)
		minerTotal.Add(minerTotal, minerFee)
		daoTotal.Add(daoTotal, daoFee)
		report.Transactions = append(report.Transactions, USDBTransactionEconomics{Hash: tx.Hash(), Status: receipt.Status,
			GasUsed: strconv.FormatUint(receipt.GasUsed, 10), GasPrice: msg.GasPrice().String(), Fee: fee.String(),
			MinerFee: minerFee.String(), DAOFee: daoFee.String(), Route: routeName})
	}
	// Observe only finalization's credit: ordinary transfers, contract calls and
	// fees may all change the miner's balance earlier in the block.
	minerBefore := new(big.Int).Set(statedb.GetBalance(header.Coinbase))
	issuedPreFinalize, err := usdbstate.ReadUint256(statedb, usdbstate.IssuedUSDBAtomsSlot)
	if err != nil || issuedPreFinalize.Cmp(issuedBefore) != 0 {
		return nil, errors.New("issued counter changed during transactions")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// FinalizeAndAssemble propagates resolver errors; Finalize cannot return them.
	assembled, err := engine.FinalizeAndAssemble(chain, header, statedb, block.Transactions(), block.Uncles(), receipts)
	if err != nil {
		return nil, economicsStateError(statedb, fmt.Errorf("economics reward finalization: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if statedb.Error() != nil {
		return nil, statedb.Error()
	}
	root := statedb.IntermediateRoot(config.IsEIP158(header.Number))
	if assembled == nil || root != block.Root() || assembled.Root() != block.Root() ||
		types.DeriveSha(receipts, trie.NewStackTrie(nil)) != block.ReceiptHash() ||
		types.CreateBloom(receipts) != block.Bloom() || usedGas != block.GasUsed() {
		return nil, errors.New("economics replay commitment mismatch")
	}
	issuedAfter, err := usdbstate.ReadUint256(statedb, usdbstate.IssuedUSDBAtomsSlot)
	if err != nil {
		return nil, err
	}
	emission := new(big.Int).Sub(issuedAfter, issuedBefore)
	minerEmission := new(big.Int).Sub(statedb.GetBalance(header.Coinbase), minerBefore)
	if emission.Sign() < 0 || emission.Cmp(minerEmission) != 0 {
		return nil, errors.New("economics emission credit mismatch")
	}
	report.Amounts = &USDBEconomicAmounts{IssuedBefore: issuedBefore.String(), IssuedAfter: issuedAfter.String(),
		Emission: emission.String(), MinerEmission: minerEmission.String(), Fees: total.String(), MinerFees: minerTotal.String(), DAOFees: daoTotal.String()}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	report.Status = "verified"
	return report, nil
}

type economicsChainContext struct {
	consensus.ChainHeaderReader
	engine consensus.Engine
}

func (c economicsChainContext) Engine() consensus.Engine { return c.engine }

// Prefer a retained-state read failure to an apparent execution/rule mismatch.
func economicsStateError(statedb *state.StateDB, err error) error {
	if dbErr := statedb.Error(); dbErr != nil {
		return dbErr
	}
	return err
}
