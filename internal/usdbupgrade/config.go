// Package usdbupgrade performs stopped-node, forward-only chain configuration updates.
package usdbupgrade

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/params"
)

// Report pins the actual database boundary; it is also the idempotent apply token.
type Report struct {
	Schema       string         `json:"schema_version"`
	Genesis      common.Hash    `json:"genesis"`
	SourceConfig string         `json:"source_config_sha256"`
	TargetConfig string         `json:"target_config_sha256"`
	Heads        [3]common.Hash `json:"heads"`
	Height       uint64         `json:"checked_height"`
}

func digest(config *params.ChainConfig) string {
	data, _ := json.Marshal(config)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// Inspect rejects historical edits even when CheckCompatible would allow a rewind.
func Inspect(db ethdb.Database, source, target *core.Genesis) (*Report, error) {
	if source == nil || target == nil || source.Config == nil || target.Config == nil || source.Config.USDB == nil || target.Config.USDB == nil {
		return nil, errors.New("both genesis files must contain USDB chain configurations")
	}
	if err := target.Config.CheckConfigForkOrder(); err != nil {
		return nil, err
	}
	before, after := source.Config.USDB.Activations, target.Config.USDB.Activations
	if len(after) < len(before) || !reflect.DeepEqual(before, after[:len(before)]) {
		return nil, errors.New("USDB checkpoints must retain the complete source prefix")
	}
	stripped := *target.Config
	scope := *target.Config.USDB
	scope.Activations = before
	stripped.USDB = &scope
	if !reflect.DeepEqual(source.Config, &stripped) {
		return nil, errors.New("only appended USDB checkpoints are supported; chain identity or other configuration changed")
	}
	genesis := rawdb.ReadCanonicalHash(db, 0)
	if genesis == (common.Hash{}) || source.ToBlock().Hash() != genesis || target.ToBlock().Hash() != genesis {
		return nil, errors.New("genesis block identity differs from the existing database")
	}
	stored := rawdb.ReadChainConfig(db, genesis)
	if stored == nil || (!reflect.DeepEqual(stored, source.Config) && !reflect.DeepEqual(stored, target.Config)) {
		return nil, errors.New("stored chain configuration matches neither source nor target; preserve data and inspect")
	}
	report := &Report{Schema: "usdb-chain-config-preflight:v1", Genesis: genesis,
		SourceConfig: digest(source.Config), TargetConfig: digest(target.Config),
		Heads: [3]common.Hash{rawdb.ReadHeadHeaderHash(db), rawdb.ReadHeadBlockHash(db), rawdb.ReadHeadFastBlockHash(db)}}
	for _, hash := range report.Heads {
		number := rawdb.ReadHeaderNumber(db, hash)
		if hash == (common.Hash{}) || number == nil || rawdb.ReadHeader(db, hash, *number) == nil || rawdb.ReadCanonicalHash(db, *number) != hash {
			return nil, fmt.Errorf("missing or noncanonical stored chain head: %s", hash)
		}
		if *number > report.Height {
			report.Height = *number
		}
	}
	if err := source.Config.CheckCompatible(target.Config, report.Height); err != nil {
		return nil, fmt.Errorf("chain configuration requires explicit derived-data rebuild at head=%d: %w; no rewind or deletion performed", report.Height, err)
	}
	return report, nil
}

// configRecord captures rawdb's canonical metadata encoding without duplicating its key format.
type configRecord struct{ key, value []byte }

func (r *configRecord) Put(key, value []byte) error {
	r.key, r.value = common.CopyBytes(key), common.CopyBytes(value)
	return nil
}
func (r *configRecord) Delete([]byte) error { return errors.New("unexpected config deletion") }

// Run never initializes missing data. Apply rechecks the saved heads under the DB lock.
func Run(datadir string, source, target *core.Genesis, expected *Report, apply bool) (*Report, error) {
	if apply && expected == nil {
		return nil, errors.New("apply requires the saved preflight")
	}
	if apply {
		// A writable LevelDB open itself may rotate LOG or recover journals. Reject
		// corrupt/moved data using a read-only open before any writable handle.
		if _, err := Run(datadir, source, target, expected, false); err != nil {
			return nil, err
		}
	}
	path := filepath.Join(datadir, "geth", "chaindata")
	if info, err := os.Stat(filepath.Join(path, "CURRENT")); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("existing LevelDB CURRENT file is required: %s", path)
	}
	kv, err := leveldb.NewForOfflineUpgrade(path, !apply)
	if err != nil {
		return nil, err
	}
	ancient := filepath.Join(path, "ancient")
	var db ethdb.Database
	if _, err := os.Stat(ancient); err == nil {
		db, err = rawdb.NewDatabaseWithFreezer(kv, ancient, "", true)
		if err != nil {
			kv.Close()
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		kv.Close()
		return nil, err
	} else {
		db = rawdb.NewDatabase(kv)
	}
	defer db.Close()
	report, err := Inspect(db, source, target)
	if err != nil {
		return nil, err
	}
	if expected != nil && !reflect.DeepEqual(expected, report) {
		return nil, fmt.Errorf("chain boundary changed since preflight: expected=%+v actual=%+v", expected, report)
	}
	if apply {
		var record configRecord
		rawdb.WriteChainConfig(&record, report.Genesis, target.Config)
		if err := kv.PutSync(record.key, record.value); err != nil {
			return nil, err
		}
	}
	return report, nil
}
