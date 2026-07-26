// Copyright 2019 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

// Package utils contains internal helper functions for go-ethereum commands.
package utils

import (
	"flag"
	"reflect"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/params"
	"github.com/urfave/cli/v2"
)

func Test_SplitTagsFlag(t *testing.T) {
	tests := []struct {
		name string
		args string
		want map[string]string
	}{
		{
			"2 tags case",
			"host=localhost,bzzkey=123",
			map[string]string{
				"host":   "localhost",
				"bzzkey": "123",
			},
		},
		{
			"1 tag case",
			"host=localhost123",
			map[string]string{
				"host": "localhost123",
			},
		},
		{
			"empty case",
			"",
			map[string]string{},
		},
		{
			"garbage",
			"smth=smthelse=123",
			map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SplitTagsFlag(tt.args); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitTagsFlag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func newCLIContext(t *testing.T, flagsList []cli.Flag, args ...string) *cli.Context {
	t.Helper()

	set := flag.NewFlagSet("test", flag.ContinueOnError)
	for _, f := range flagsList {
		if err := f.Apply(set); err != nil {
			t.Fatalf("failed to apply flag %v: %v", f.Names(), err)
		}
	}
	if err := set.Parse(args); err != nil {
		t.Fatalf("failed to parse args %v: %v", args, err)
	}
	return cli.NewContext(cli.NewApp(), set, nil)
}

func TestSetMinerAppliesUSDBFlags(t *testing.T) {
	ctx := newCLIContext(t, []cli.Flag{
		MinerUSDBIndexerRPCURLFlag,
		MinerUSDBPassIDFlag,
		MinerUSDBIndexerTimeoutFlag,
	}, "--miner.usdb-indexer.rpcurl", "http://127.0.0.1:8548", "--miner.usdb.passid", "abc123i7", "--miner.usdb-indexer.timeout", "4s")

	cfg := ethconfig.Defaults.Miner
	setMiner(ctx, &cfg)

	if cfg.USDB.IndexerRPCURL != "http://127.0.0.1:8548" {
		t.Fatalf("unexpected miner usdb-indexer rpc url: %s", cfg.USDB.IndexerRPCURL)
	}
	if cfg.USDB.PassID != "abc123i7" {
		t.Fatalf("unexpected miner usdb pass id: %s", cfg.USDB.PassID)
	}
	if cfg.USDB.IndexerQueryTimeout != 4*time.Second {
		t.Fatalf("unexpected miner usdb-indexer timeout: %s", cfg.USDB.IndexerQueryTimeout)
	}
}

func TestSetEthashAppliesUSDBFlags(t *testing.T) {
	ctx := newCLIContext(t, []cli.Flag{
		EthashUSDBIndexerRPCURLFlag,
		EthashUSDBIndexerTimeoutFlag,
		FakePoWFlag,
		FakePoWDelayFlag,
	}, "--ethash.usdb-indexer.rpcurl", "http://127.0.0.1:18548", "--ethash.usdb-indexer.timeout", "6s", "--fakepow", "--fakepow.delay", "250ms")

	cfg := ethconfig.Defaults
	setEthash(ctx, &cfg)

	if cfg.Ethash.USDBIndexer.RPCURL != "http://127.0.0.1:18548" {
		t.Fatalf("unexpected ethash usdb-indexer rpc url: %s", cfg.Ethash.USDBIndexer.RPCURL)
	}
	if cfg.Ethash.USDBIndexer.QueryTimeout != 6*time.Second {
		t.Fatalf("unexpected ethash usdb-indexer timeout: %s", cfg.Ethash.USDBIndexer.QueryTimeout)
	}
	if cfg.Ethash.PowMode != ethash.ModeFake {
		t.Fatalf("fake-PoW flag did not reach the node ethash config: %d", cfg.Ethash.PowMode)
	}
	if cfg.Ethash.FakeSealDelay != 250*time.Millisecond {
		t.Fatalf("unexpected fake-PoW seal delay: %s", cfg.Ethash.FakeSealDelay)
	}
}

func TestSetEthashUSDBIndexerAppliesOfflineChainFlags(t *testing.T) {
	ctx := newCLIContext(t, []cli.Flag{
		EthashUSDBIndexerRPCURLFlag,
		EthashUSDBIndexerTimeoutFlag,
	}, "--ethash.usdb-indexer.rpcurl", "http://127.0.0.1:28548", "--ethash.usdb-indexer.timeout", "7s")

	cfg := ethconfig.Defaults.Ethash
	setEthashUSDBIndexer(ctx, &cfg)

	if cfg.USDBIndexer.RPCURL != "http://127.0.0.1:28548" {
		t.Fatalf("unexpected offline-chain usdb-indexer rpc url: %s", cfg.USDBIndexer.RPCURL)
	}
	if cfg.USDBIndexer.QueryTimeout != 7*time.Second {
		t.Fatalf("unexpected offline-chain usdb-indexer timeout: %s", cfg.USDBIndexer.QueryTimeout)
	}
}

func TestMakeGenesisReturnsUSDBGenesis(t *testing.T) {
	ctx := newCLIContext(t, []cli.Flag{USDBFlag}, "--usdb")

	genesis := MakeGenesis(ctx)
	if genesis == nil {
		t.Fatalf("expected usdb genesis")
	}
	if genesis.Config != params.USDBChainConfig {
		t.Fatalf("unexpected chain config: %#v", genesis.Config)
	}
	if want := core.DefaultUSDBGenesisBlock().ToBlock().Hash(); genesis.ToBlock().Hash() != want {
		t.Fatalf("unexpected usdb genesis hash: have %s want %s", genesis.ToBlock().Hash(), want)
	}
}

func TestSetP2PConfigAppliesUSDBDefaultListenPort(t *testing.T) {
	ctx := newCLIContext(t, []cli.Flag{
		USDBFlag,
		NodeKeyHexFlag,
		NodeKeyFileFlag,
		BootnodesFlag,
		ListenPortFlag,
		DiscoveryPortFlag,
		NATFlag,
		SyncModeFlag,
		LightServeFlag,
		LightMaxPeersFlag,
		MaxPeersFlag,
		MaxPendingPeersFlag,
		NoDiscoverFlag,
		DiscoveryV5Flag,
		NetrestrictFlag,
		DeveloperFlag,
	}, "--usdb")

	cfg := node.DefaultConfig
	SetNodeConfig(ctx, &cfg)

	if cfg.P2P.ListenAddr != ":31303" {
		t.Fatalf("unexpected usdb default listen addr: %s", cfg.P2P.ListenAddr)
	}
}

func TestSetP2PConfigKeepsExplicitListenPort(t *testing.T) {
	ctx := newCLIContext(t, []cli.Flag{
		USDBFlag,
		NodeKeyHexFlag,
		NodeKeyFileFlag,
		BootnodesFlag,
		ListenPortFlag,
		DiscoveryPortFlag,
		NATFlag,
		SyncModeFlag,
		LightServeFlag,
		LightMaxPeersFlag,
		MaxPeersFlag,
		MaxPendingPeersFlag,
		NoDiscoverFlag,
		DiscoveryV5Flag,
		NetrestrictFlag,
		DeveloperFlag,
	}, "--usdb", "--port", "32000")

	cfg := node.DefaultConfig
	SetNodeConfig(ctx, &cfg)

	if cfg.P2P.ListenAddr != ":32000" {
		t.Fatalf("unexpected explicit listen addr: %s", cfg.P2P.ListenAddr)
	}
}

func TestSetNodeConfigKeepsConfigFileListenAddr(t *testing.T) {
	ctx := newCLIContext(t, []cli.Flag{
		USDBFlag,
	}, "--usdb")

	cfg := node.DefaultConfig
	cfg.P2P.ListenAddr = ":30303"
	cfg.P2PListenAddrConfigured = true
	SetNodeConfig(ctx, &cfg)

	if cfg.P2P.ListenAddr != ":30303" {
		t.Fatalf("unexpected configured listen addr override: %s", cfg.P2P.ListenAddr)
	}
}
