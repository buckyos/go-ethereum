package eth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/trie"
)

type economicsRPCError struct {
	code    int
	message string
}

func (e *economicsRPCError) Error() string  { return e.message }
func (e *economicsRPCError) ErrorCode() int { return e.code }

// economicsCache bounds expensive replays and caches only hash-bound verified reports.
type economicsCache struct {
	mu     sync.Mutex
	active int
	values map[common.Hash]*core.USDBBlockEconomics
	order  []common.Hash
}

// GetUSDBBlockEconomics verifies one canonical block using retained parent state.
// It accepts only a hash, never pending/latest, and does not regenerate pruned state.
// Errors intentionally omit indexer endpoints and raw upstream response data.
func (api *EthereumAPI) GetUSDBBlockEconomics(ctx context.Context, hash common.Hash) (*core.USDBBlockEconomics, error) {
	chain := api.e.BlockChain()
	block := chain.GetBlockByHash(hash)
	if block == nil {
		return nil, &economicsRPCError{-32060, "BLOCK_NOT_FOUND"}
	}
	canonical := func() bool { return chain.GetCanonicalHash(block.NumberU64()) == hash }
	if !canonical() {
		return nil, &economicsRPCError{-32061, "BLOCK_NOT_CANONICAL"}
	}
	if block.NumberU64() == 0 {
		report := core.USDBEconomicsIdentity(block)
		report.Status = "genesis_not_applicable"
		return report, nil
	}
	if len(block.Transactions()) > 2000 || block.GasUsed() > 60000000 {
		return nil, &economicsRPCError{-32066, "ECONOMICS_REPLAY_LIMIT"}
	}
	cache := &api.economics
	cache.mu.Lock()
	if report := cache.values[hash]; report != nil {
		cache.mu.Unlock()
		return report, nil
	}
	if cache.active >= 2 {
		cache.mu.Unlock()
		return nil, &economicsRPCError{-32067, "ECONOMICS_BUSY"}
	}
	cache.active++
	cache.mu.Unlock()
	defer func() { cache.mu.Lock(); cache.active--; cache.mu.Unlock() }()
	parent := chain.GetBlock(block.ParentHash(), block.NumberU64()-1)
	if parent == nil {
		return nil, &economicsRPCError{-32062, "HISTORICAL_STATE_UNAVAILABLE"}
	}
	statedb, err := chain.StateAt(parent.Root())
	if err != nil {
		return nil, &economicsRPCError{-32062, "HISTORICAL_STATE_UNAVAILABLE"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	report, err := core.ReplayUSDBEconomics(ctx, chain, api.e.Engine(), block, statedb)
	if err != nil {
		code, message := -32064, "ECONOMICS_VERIFICATION_FAILED"
		var missing *trie.MissingNodeError
		if errors.As(err, &missing) {
			code, message = -32062, "HISTORICAL_STATE_UNAVAILABLE"
		}
		if errors.Is(err, core.ErrUSDBEconomicsUnsupported) {
			code, message = -32063, "ECONOMICS_POLICY_UNSUPPORTED"
		}
		if ctx.Err() != nil {
			code, message = -32065, "ECONOMICS_TIMEOUT"
		}
		log.Warn("USDB economics replay unavailable", "block", hash, "code", message)
		return nil, &economicsRPCError{code, message}
	}
	if !canonical() {
		return nil, &economicsRPCError{-32061, "BLOCK_NOT_CANONICAL"}
	}
	cache.mu.Lock()
	if cache.values == nil {
		cache.values = make(map[common.Hash]*core.USDBBlockEconomics)
	}
	if cache.values[hash] == nil {
		if len(cache.order) >= 16 {
			delete(cache.values, cache.order[0])
			cache.order = cache.order[1:]
		}
		cache.values[hash] = report
		cache.order = append(cache.order, hash)
	}
	cache.mu.Unlock()
	return report, nil
}
