// Copyright 2026 The go-ethereum Authors
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
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/log"
)

type capturedLogRecords struct {
	mu      sync.Mutex
	records []log.Record
}

func (c *capturedLogRecords) handler() log.Handler {
	return log.FuncHandler(func(record *log.Record) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.records = append(c.records, *record)
		return nil
	})
}

func (c *capturedLogRecords) find(message string) *log.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	for index := range c.records {
		if c.records[index].Msg == message {
			record := c.records[index]
			return &record
		}
	}
	return nil
}

func (c *capturedLogRecords) string() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fmt.Sprint(c.records)
}

func logContextValue(record *log.Record, key string) interface{} {
	for index := 0; index+1 < len(record.Ctx); index += 2 {
		if record.Ctx[index] == key {
			return record.Ctx[index+1]
		}
	}
	return nil
}

func TestUSDBStartupLogsConsensusIdentityWithoutEndpointSecrets(t *testing.T) {
	chainConfig := newTestUSDBChainConfig()
	endpoint := "http://alice:password@127.0.0.1:18548/private-token?api_key=query-secret"
	var captured capturedLogRecords
	logger := log.New()
	logger.SetHandler(captured.handler())
	engine := NewWithChainConfig(Config{
		CachesInMem:   1,
		DatasetsInMem: 1,
		PowMode:       ModeFake,
		Log:           logger,
		USDBIndexer: USDBIndexerConfig{
			RPCURL: endpoint,
		},
	}, chainConfig, nil, false)
	defer engine.Close()

	identity := captured.find("USDB consensus configuration loaded")
	if identity == nil ||
		logContextValue(identity, "btc_network") != chainConfig.USDB.BTCNetworkID ||
		logContextValue(identity, "activation_count") != len(chainConfig.USDB.Activations) {
		t.Fatalf("missing USDB consensus identity log: %+v", identity)
	}
	activation := captured.find("USDB consensus activation configured")
	if activation == nil || logContextValue(activation, "btc_registry") != usdb.BTCRegtestActivationRegistryIDV1 {
		t.Fatalf("missing USDB activation identity log: %+v", activation)
	}
	resolver := captured.find("USDB profile resolver initialized")
	if resolver == nil || logContextValue(resolver, "endpoint") != "http://127.0.0.1:18548" ||
		logContextValue(resolver, "query_timeout") != usdb.DefaultQueryTimeout {
		t.Fatalf("missing USDB resolver identity log: %+v", resolver)
	}
	output := captured.string()
	for _, secret := range []string{"alice", "password", "private-token", "query-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("startup logs exposed %q: %s", secret, output)
		}
	}
}
