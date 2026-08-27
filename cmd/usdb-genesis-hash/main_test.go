// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/core"
)

func writeTestGenesis(t *testing.T, suffix string) string {
	t.Helper()
	encoded, err := json.Marshal(core.DefaultUSDBGenesisBlock())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "genesis.json")
	if err := os.WriteFile(path, append(encoded, []byte(suffix)...), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunPrintsCanonicalGenesisHash(t *testing.T) {
	path := writeTestGenesis(t, "\n")
	var output bytes.Buffer
	if err := run([]string{path}, &output); err != nil {
		t.Fatal(err)
	}
	want := core.DefaultUSDBGenesisBlock().ToBlock().Hash().Hex() + "\n"
	if output.String() != want {
		t.Fatalf("unexpected genesis hash: have %q want %q", output.String(), want)
	}
}

func TestRunRejectsTrailingJSON(t *testing.T) {
	path := writeTestGenesis(t, "\n{}\n")
	if err := run([]string{path}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "more than one value") {
		t.Fatalf("expected trailing JSON rejection, got %v", err)
	}
}

func TestRunRequiresOnePath(t *testing.T) {
	if err := run(nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("expected usage error, got %v", err)
	}
}
