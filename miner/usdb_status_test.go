package miner

import (
	"math/big"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/params"
)

func TestUSDBWorkStatusParentStopAndSnapshotIsolation(t *testing.T) {
	w, backend := newTestWorker(t, params.AllEthashProtocolChanges, ethash.NewFaker(), rawdb.NewMemoryDatabase(), 0)
	defer w.close()
	defer backend.txPool.Stop()
	defer backend.chain.Stop()
	m := &Miner{worker: w}
	build := &usdb.MinerBuildStatus{Stage: "validated", Selection: &usdb.MinerAnchorSelection{LatestHeight: 102, SelectedHeight: 100, ConfiguredGap: 2, ActualGap: 2}}
	header := &types.Header{ParentHash: w.chain.CurrentBlock().Hash(), Number: big.NewInt(1), Difficulty: big.NewInt(1)}
	block := types.NewBlockWithHeader(header)
	atomic.StoreInt32(&w.running, 1)
	w.recordUSDBSealingWork(block, build)
	status := m.USDBWorkStatus()
	if !status.WorkCurrent || status.Work.SealHash != w.engine.SealHash(header) || status.ConfiguredGap != 2 {
		t.Fatal(status)
	}
	status.Work.Build.Selection.SelectedHeight = 0
	if m.USDBWorkStatus().Work.Build.Selection.SelectedHeight != 100 {
		t.Fatal("shared observation")
	}
	header.ParentHash = common.Hash{1}
	w.recordUSDBSealingWork(types.NewBlockWithHeader(header), build)
	if m.USDBWorkStatus().WorkCurrent {
		t.Fatal("work on stale parent marked current")
	}
	w.recordUSDBSealingWork(nil, nil)
	if m.USDBWorkStatus().Work != nil {
		t.Fatal("failed submission retained previous work")
	}
	w.recordUSDBSealingWork(block, build)
	w.stop()
	if m.USDBWorkStatus().WorkCurrent {
		t.Fatal("stopped miner marked current")
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			m.USDBWorkStatus()
		}
	}()
	for i := 0; i < 100; i++ {
		w.recordUSDBSealingWork(block, build)
	}
	wg.Wait()
}
