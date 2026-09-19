package core

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/usdbstate"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"
)

func TestUSDBEconomicsReplayExecutionAndRestart(t *testing.T) {
	t.Run("dividend_gate", func(t *testing.T) { testUSDBEconomicsReplay(t, false) })
	t.Run("policy_activation", func(t *testing.T) { testUSDBEconomicsReplay(t, true) })
}

func testUSDBEconomicsReplay(t *testing.T, staged bool) {
	miner, dividend := common.HexToAddress("0x1111111111111111111111111111111111111111"), common.HexToAddress("0x2222")
	revert, refund := common.HexToAddress("0x3333"), common.HexToAddress("0x4444")
	key := mustUSDBFeeTestKey(t)
	genesis := newUSDBImportTestGenesis(t)
	genesis.Config.USDB.Activations[0].Versions.FeeSplitPolicyVersion = 1
	genesis.Config.DividendAddress, genesis.Config.DividendCodeHash = dividend, crypto.Keccak256Hash([]byte{0})
	genesis.Config.DividendFeeSplitBlock = big.NewInt(2)
	if staged {
		checkpoint := genesis.Config.USDB.Activations[0]
		checkpoint.Block = 2
		genesis.Config.USDB.Activations[0].Versions.FeeSplitPolicyVersion = 0
		genesis.Config.USDB.Activations = append(genesis.Config.USDB.Activations, checkpoint)
	}
	genesis.Alloc[dividend] = GenesisAccount{Balance: new(big.Int), Code: []byte{0}, Storage: map[common.Hash]common.Hash{usdbstate.DividendBootstrapFinalizedSlot: common.BigToHash(big.NewInt(1))}}
	genesis.Alloc[crypto.PubkeyToAddress(key.PublicKey)] = GenesisAccount{Balance: new(big.Int).Exp(big.NewInt(10), big.NewInt(21), nil)}
	genesis.Alloc[revert] = GenesisAccount{Balance: new(big.Int), Code: common.FromHex("0x60006000fd")}
	genesis.Alloc[refund] = GenesisAccount{Balance: new(big.Int), Code: common.FromHex("0x600060005500"), Storage: map[common.Hash]common.Hash{{}: common.BigToHash(big.NewInt(1))}}
	// Genesis allocations are included in the issued counter.
	delete(genesis.Alloc, usdbstate.SystemStateAddress)
	if err := initializeUSDBGenesisSystemState(genesis.Alloc, genesis.Config); err != nil {
		t.Fatal(err)
	}
	profile := newUSDBImportTestProfile(t, miner, "10000000000000000")
	server := newUSDBImportTestProfileServer(t, profile)
	defer server.Close()
	engine := newUSDBImportTestEngine(genesis.Config, server.URL)
	defer engine.Close()
	db := rawdb.NewMemoryDatabase()
	genesisBlock := genesis.MustCommit(db)
	blocks, recorded := GenerateChain(genesis.Config, genesisBlock, engine, db, 3, func(i int, b *BlockGen) {
		b.SetCoinbase(miner)
		b.SetExtra(newUSDBImportTestSelector(t, uint32(i)))
		b.header.UncleHash = types.EmptyUncleHash
		if i == 2 {
			return
		} // An empty block must still reconcile issuance.
		for j, to := range []common.Address{miner, revert, refund} {
			tx := types.NewTransaction(uint64(i*3+j), to, big.NewInt(11), 60000, big.NewInt(1000000001), nil)
			if j == 0 {
				tx = types.NewTx(&types.DynamicFeeTx{ChainID: genesis.Config.ChainID, Nonce: uint64(i*3 + j), To: &to,
					Value: big.NewInt(11), Gas: 60000, GasFeeCap: big.NewInt(2000000000), GasTipCap: big.NewInt(3)})
			}
			b.AddTx(signUSDBFeeTestTransaction(t, genesis.Config, key, tx))
		}
	})
	chain, err := NewBlockChain(db, &CacheConfig{TrieDirtyDisabled: true}, genesis.Config, engine, vm.Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chain.InsertChain(blocks); err != nil {
		chain.Stop()
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		for i, block := range blocks {
			parent := chain.GetBlockByHash(block.ParentHash())
			st, err := chain.StateAt(parent.Root())
			if err != nil {
				t.Fatal(err)
			}
			report, err := ReplayUSDBEconomics(context.Background(), chain, engine, block, st)
			if err != nil {
				t.Fatalf("block %d round %d: %v", i+1, round, err)
			}
			if report.Status != "verified" || report.Hash != block.Hash() || report.Selector.BTCHeight != 123 || report.Amounts.Emission == "0" {
				t.Fatalf("unexpected report: %+v", report)
			}
			if st.IntermediateRoot(true) != parent.Root() {
				t.Fatal("query changed input state")
			}
			expectedFee, expectedDAO := new(big.Int), new(big.Int)
			for j, receipt := range recorded[i] {
				price := big.NewInt(1000000001)
				if j == 0 {
					price.Add(block.BaseFee(), big.NewInt(3))
				}
				fee := new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), price)
				expectedFee.Add(expectedFee, fee)
				dao := new(big.Int)
				if i == 1 {
					dao.Div(new(big.Int).Mul(fee, big.NewInt(4000)), big.NewInt(10000))
					expectedDAO.Add(expectedDAO, dao)
				}
				tx := report.Transactions[j]
				if tx.GasPrice != price.String() || tx.Fee != fee.String() || tx.DAOFee != dao.String() || tx.Status != receipt.Status {
					t.Fatalf("fee mismatch: %+v", tx)
				}
			}
			if report.Amounts.Fees != expectedFee.String() || report.Amounts.DAOFees != expectedDAO.String() {
				t.Fatal("aggregate mismatch")
			}
			if i < 2 && (report.Transactions[1].Status != 0 || report.Transactions[1].Fee == "0") {
				t.Fatal("reverted call fee lost")
			}
			if i == 0 && recorded[i][2].GasUsed >= 26000 {
				t.Fatal("storage refund fixture did not refund gas")
			}
			// The finalization credit is isolated from the 11-atom transfer and fees.
			if report.Amounts.Emission != report.Amounts.MinerEmission {
				t.Fatal("ordinary transfer leaked into emission")
			}
		}
		chain.Stop()
		if round == 0 {
			chain, err = NewBlockChain(db, &CacheConfig{TrieDirtyDisabled: true}, genesis.Config, engine, vm.Config{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestUSDBEconomicsRejectsUnverifiedResults(t *testing.T) {
	miner := common.HexToAddress("0x1111111111111111111111111111111111111111")
	genesis := newUSDBImportTestGenesis(t)
	server := newUSDBImportTestProfileServer(t, newUSDBImportTestProfile(t, miner, "100000000"))
	defer server.Close()
	engine := newUSDBImportTestEngine(genesis.Config, server.URL)
	defer engine.Close()
	db := rawdb.NewMemoryDatabase()
	genesisBlock := genesis.MustCommit(db)
	blocks := generateUSDBImportTestBlocks(t, genesis.Config, genesisBlock, engine, db, miner, 1)
	chain, err := NewBlockChain(db, nil, genesis.Config, engine, vm.Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()
	for _, field := range []string{"root", "receipts", "gas", "bloom", "selector"} {
		t.Run(field, func(t *testing.T) {
			h := blocks[0].Header()
			switch field {
			case "root":
				h.Root = common.Hash{1}
			case "receipts":
				h.ReceiptHash = common.Hash{1}
			case "gas":
				h.GasUsed++
			case "bloom":
				h.Bloom[0] = 1
			case "selector":
				h.Extra = nil
			}
			st, _ := chain.StateAt(genesisBlock.Root())
			if report, err := ReplayUSDBEconomics(context.Background(), chain, engine, types.NewBlockWithHeader(h), st); err == nil || report != nil {
				t.Fatal("unverified report escaped")
			}
		})
	}
	st, _ := chain.StateAt(genesisBlock.Root())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReplayUSDBEconomics(ctx, chain, engine, blocks[0], st); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	genesis.Config.USDB.Activations[0].Versions.AuxPoolPolicyVersion = 1
	if _, err := ReplayUSDBEconomics(context.Background(), chain, engine, blocks[0], st); !errors.Is(err, ErrUSDBEconomicsUnsupported) {
		t.Fatal(err)
	}
	// The empty-block fixture commits an actual empty receipt trie, not an absent root.
	if blocks[0].ReceiptHash() != types.DeriveSha(types.Receipts{}, trie.NewStackTrie(nil)) {
		t.Fatal("fixture receipt root")
	}
}
