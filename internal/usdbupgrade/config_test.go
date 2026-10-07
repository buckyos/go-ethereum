package usdbupgrade

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
)

func fixture(t *testing.T) (string, *core.Genesis, *core.Genesis) {
	t.Helper()
	var cfg params.ChainConfig
	data, _ := json.Marshal(params.USDBChainConfig)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	source := &core.Genesis{Config: &cfg, GasLimit: 10000000, Difficulty: big.NewInt(1), Alloc: core.GenesisAlloc{}}
	data, _ = json.Marshal(source)
	var target core.Genesis
	if err := json.Unmarshal(data, &target); err != nil {
		t.Fatal(err)
	}
	next := target.Config.USDB.Activations[0]
	next.Block = 10
	next.BTCActivationRegistryID = strings.Repeat("a", 64)
	target.Config.USDB.Activations = append(target.Config.USDB.Activations, next)
	root := t.TempDir()
	db, err := rawdb.NewLevelDBDatabase(filepath.Join(root, "geth/chaindata"), 16, 16, "", false)
	if err != nil {
		t.Fatal(err)
	}
	source.MustCommit(db)
	if err := db.Put([]byte("upgrade-test-business-state"), []byte("preserve")); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return root, source, &target
}

func advance(t *testing.T, root string, head uint64, headerOnly bool) {
	t.Helper()
	db, err := rawdb.NewLevelDBDatabase(filepath.Join(root, "geth/chaindata"), 16, 16, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := &types.Header{Number: new(big.Int).SetUint64(head), Difficulty: big.NewInt(1)}
	rawdb.WriteHeader(db, h)
	rawdb.WriteCanonicalHash(db, h.Hash(), head)
	rawdb.WriteHeadHeaderHash(db, h.Hash())
	if !headerOnly {
		rawdb.WriteHeadBlockHash(db, h.Hash())
		rawdb.WriteHeadFastBlockHash(db, h.Hash())
	}
}

func inspectDB(t *testing.T, root string, fn func(ethdb.Database)) {
	t.Helper()
	db, err := rawdb.NewLevelDBDatabase(filepath.Join(root, "geth/chaindata"), 16, 16, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fn(db)
}

func TestConfigurationAdoptionPreservesHistoryAndResumes(t *testing.T) {
	root, source, target := fixture(t)
	advance(t, root, 9, false)
	beforeFiles := datasetFiles(t, root)
	report, err := Run(root, source, target, nil, false)
	if !reflect.DeepEqual(beforeFiles, datasetFiles(t, root)) {
		t.Fatal("read-only preflight changed database files")
	}
	if err != nil {
		t.Fatal(err)
	}
	inspectDB(t, root, func(db ethdb.Database) {
		if !reflect.DeepEqual(rawdb.ReadChainConfig(db, report.Genesis), source.Config) {
			t.Fatal("preflight changed config")
		}
	})
	for i := 0; i < 2; i++ {
		actual, err := Run(root, source, target, report, true)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, report) {
			t.Fatal("resume report differs")
		}
	}
	inspectDB(t, root, func(db ethdb.Database) {
		if !reflect.DeepEqual(rawdb.ReadChainConfig(db, report.Genesis), target.Config) {
			t.Fatal("target config missing")
		}
		value, _ := db.Get([]byte("upgrade-test-business-state"))
		if string(value) != "preserve" {
			t.Fatal("business state changed")
		}
		if rawdb.ReadHeadHeaderHash(db) != report.Heads[0] {
			t.Fatal("head changed")
		}
	})
}

func TestLateUpgradeRejectsAllImportedHeadKinds(t *testing.T) {
	for _, headerOnly := range []bool{false, true} {
		for _, height := range []uint64{10, 11} {
			root, source, target := fixture(t)
			advance(t, root, height, headerOnly)
			if _, err := Run(root, source, target, nil, false); err == nil || !strings.Contains(err.Error(), "rebuild") {
				t.Fatalf("head=%d headerOnly=%v err=%v", height, headerOnly, err)
			}
			inspectDB(t, root, func(db ethdb.Database) {
				if !reflect.DeepEqual(rawdb.ReadChainConfig(db, source.ToBlock().Hash()), source.Config) {
					t.Fatal("rejected preflight changed config")
				}
			})
		}
	}
}

func TestBoundaryOrConfigurationChangeRefusesApply(t *testing.T) {
	root, source, target := fixture(t)
	report, err := Run(root, source, target, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	advance(t, root, 1, true)
	if _, err := Run(root, source, target, report, true); err == nil || !strings.Contains(err.Error(), "boundary changed") {
		t.Fatal(err)
	}
	target.Config.USDB.Activations[0].BTCAnchorMaxAgeBlocks++
	if _, err := Run(root, source, target, nil, false); err == nil {
		t.Fatal("edited historical checkpoint accepted")
	}
	if _, err := Run(root, target, source, nil, false); err == nil {
		t.Fatal("downgrade accepted")
	}
}

func TestMissingOrForeignDataNeverInitialized(t *testing.T) {
	root, source, target := fixture(t)
	empty := t.TempDir()
	if _, err := Run(empty, source, target, nil, false); err == nil {
		t.Fatal("missing DB accepted")
	}
	entries, _ := os.ReadDir(empty)
	if len(entries) != 0 {
		t.Fatal("missing DB initialized")
	}
	target.ExtraData = []byte("another genesis")
	if _, err := Run(root, source, target, nil, false); err == nil {
		t.Fatal("different genesis accepted")
	}
	target.ExtraData = nil
	db, err := rawdb.NewLevelDBDatabase(filepath.Join(root, "geth/chaindata"), 16, 16, "", false)
	if err != nil {
		t.Fatal(err)
	}
	rawdb.WriteHeadHeaderHash(db, common.HexToHash("0x1234"))
	db.Close()
	if _, err := Run(root, source, target, nil, false); err == nil {
		t.Fatal("missing head accepted")
	}
}

func datasetFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[name] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestCorruptDatabaseIsNotAutomaticallyRecovered(t *testing.T) {
	root, source, target := fixture(t)
	current := filepath.Join(root, "geth/chaindata/CURRENT")
	if err := os.WriteFile(current, []byte("malformed manifest"), 0600); err != nil {
		t.Fatal(err)
	}
	before := datasetFiles(t, root)
	for _, apply := range []bool{false, true} {
		if _, err := Run(root, source, target, &Report{}, apply); err == nil {
			t.Fatal("corruption accepted")
		}
		if !reflect.DeepEqual(before, datasetFiles(t, root)) {
			t.Fatal("corrupt database was changed by inspection")
		}
	}
}

// Real running nodes create ancient/chain even before any block is frozen.
func TestOfflineUpgradeWithReadOnlyAncientFiles(t *testing.T) {
	root, source, target := fixture(t)
	path := filepath.Join(root, "geth/chaindata")
	db, err := rawdb.NewLevelDBDatabaseWithFreezer(path, 16, 16, filepath.Join(path, "ancient"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawdb.WriteAncientBlocks(db, []*types.Block{source.ToBlock()}, []types.Receipts{nil}, source.Difficulty); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ancient := filepath.Join(path, "ancient")
	before := datasetFiles(t, root)
	err = filepath.Walk(ancient, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0400)
		if info.IsDir() {
			mode = 0500
		}
		t.Cleanup(func() {
			if info.IsDir() {
				os.Chmod(p, 0700)
			} else {
				os.Chmod(p, 0600)
			}
		})
		return os.Chmod(p, mode)
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := Run(root, source, target, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, datasetFiles(t, root)) {
		t.Fatal("freezer preflight changed files")
	}
	if _, err := Run(root, source, target, report, true); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyAncientFailuresNeverRepairOrInitialize(t *testing.T) {
	for _, failure := range []string{"missing-meta", "empty-meta", "invalid-index", "dangling-content"} {
		t.Run(failure, func(t *testing.T) {
			root, source, target := fixture(t)
			path := filepath.Join(root, "geth/chaindata")
			db, err := rawdb.NewLevelDBDatabaseWithFreezer(path, 16, 16, filepath.Join(path, "ancient"), "", false)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			ancient := filepath.Join(path, "ancient/chain")
			switch failure {
			case "missing-meta":
				err = os.Remove(filepath.Join(ancient, "headers.meta"))
			case "empty-meta":
				err = os.WriteFile(filepath.Join(ancient, "headers.meta"), nil, 0600)
			case "invalid-index":
				err = os.WriteFile(filepath.Join(ancient, "headers.cidx"), []byte{1}, 0600)
			case "dangling-content":
				err = os.WriteFile(filepath.Join(ancient, "headers.0000.cdat"), []byte{1}, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := datasetFiles(t, root)
			for _, apply := range []bool{false, true} {
				if _, err := Run(root, source, target, &Report{}, apply); err == nil {
					t.Fatal("damaged ancient files were accepted")
				}
				if !reflect.DeepEqual(before, datasetFiles(t, root)) {
					t.Fatal("ancient rejection changed dataset files")
				}
			}
		})
	}
}
