package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/internal/usdbupgrade"
	"github.com/ethereum/go-ethereum/params"
	"github.com/urfave/cli/v2"
)

func TestUSDBUpgradeConfigCLI(t *testing.T) {
	root := t.TempDir()
	source := core.DefaultUSDBGenesisBlock()
	encoded, _ := json.Marshal(source)
	var target core.Genesis
	if err := json.Unmarshal(encoded, &target); err != nil {
		t.Fatal(err)
	}
	next := target.Config.USDB.Activations[0]
	next.Block = 100
	next.BTCActivationRegistryID = strings.Repeat("a", 64)
	target.Config.USDB.Activations = append(target.Config.USDB.Activations, next)
	for name, genesis := range map[string]*core.Genesis{"source.json": source, "target.json": &target} {
		data, err := json.Marshal(genesis)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := rawdb.NewLevelDBDatabase(filepath.Join(root, "geth/chaindata"), 16, 16, "", false)
	if err != nil {
		t.Fatal(err)
	}
	source.MustCommit(db)
	db.Close()
	args := []string{"geth", "usdb-upgrade-config", "--datadir", root, "--source-genesis", filepath.Join(root, "source.json"), "--target-genesis", filepath.Join(root, "target.json")}
	run := func(args []string) (string, error) {
		app := cli.NewApp()
		app.Commands = []*cli.Command{usdbUpgradeConfigCommand}
		output := new(bytes.Buffer)
		app.Writer = output
		err := app.Run(args)
		return output.String(), err
	}
	result, err := run(args)
	if err != nil {
		t.Fatal(err)
	}
	var report usdbupgrade.Report
	if err := json.Unmarshal([]byte(result), &report); err != nil {
		t.Fatal(err)
	}
	if report.Schema != "usdb-chain-config-preflight:v1" || report.Height != 0 {
		t.Fatalf("unexpected report %s", result)
	}
	proof := filepath.Join(root, "report.json")
	if err := os.WriteFile(proof, []byte(result), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(append(append([]string{}, args...), "--apply")); err == nil {
		t.Fatal("apply without proof accepted")
	}
	if _, err := run(append(args, "--apply", "--expected-report", proof)); err != nil {
		t.Fatal(err)
	}
	db, err = rawdb.NewLevelDBDatabase(filepath.Join(root, "geth/chaindata"), 16, 16, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	config := rawdb.ReadChainConfig(db, source.ToBlock().Hash())
	if len(config.USDB.Activations) != len(params.USDBChainConfig.USDB.Activations)+1 {
		t.Fatal("CLI failed to persist checkpoint")
	}
}
