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

// usdb-genesis-hash prints the canonical block-zero hash derived from a genesis
// JSON file. Release automation uses it to verify the network bundle with the
// exact go-ethereum revision selected by the release tag.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
)

func readGenesis(path string) (*core.Genesis, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open genesis JSON: %w", err)
	}
	defer file.Close()

	var genesis core.Genesis
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&genesis); err != nil {
		return nil, fmt.Errorf("decode genesis JSON: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("genesis JSON contains more than one value")
		}
		return nil, fmt.Errorf("decode trailing genesis JSON: %w", err)
	}
	if genesis.Config == nil {
		return nil, errors.New("genesis JSON is missing chain config")
	}
	if genesis.Number != 0 {
		return nil, fmt.Errorf("genesis block number must be zero, have %d", genesis.Number)
	}
	return &genesis, nil
}

func blockHash(genesis *core.Genesis) (hash common.Hash, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("derive genesis block hash: %v", recovered)
		}
	}()
	return genesis.ToBlock().Hash(), nil
}

func run(args []string, output io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: usdb-genesis-hash <genesis.json>")
	}
	genesis, err := readGenesis(args[0])
	if err != nil {
		return err
	}
	hash, err := blockHash(genesis)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, hash.Hex())
	return err
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
