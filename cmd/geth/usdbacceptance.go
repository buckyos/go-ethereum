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
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/internal/usdbacceptance"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/urfave/cli/v2"
)

const bootstrapAcceptanceRPCTimeout = 30 * time.Second

var (
	bootstrapAcceptanceRPCURLFlag = &cli.StringFlag{
		Name:     "rpc-url",
		Usage:    "USDB JSON-RPC endpoint to inspect",
		Required: true,
	}
	bootstrapAcceptanceGenesisFlag = &cli.PathFlag{
		Name:     "genesis",
		Usage:    "Canonical USDB genesis JSON",
		Required: true,
	}
	bootstrapAcceptanceConfigFlag = &cli.PathFlag{
		Name:     "bootstrap-config",
		Usage:    "Frozen SourceDAO full-bootstrap config",
		Required: true,
	}
	bootstrapAcceptanceStateFlag = &cli.PathFlag{
		Name:     "bootstrap-state",
		Usage:    "Completed SourceDAO full-bootstrap state",
		Required: true,
	}
	bootstrapAcceptanceValidationFlag = &cli.PathFlag{
		Name:     "validation",
		Usage:    "Successful SourceDAO strict validation summary",
		Required: true,
	}
	bootstrapAcceptanceArtifactFlag = &cli.PathFlag{
		Name:     "artifact",
		Usage:    "USDB bootstrap acceptance checkpoint artifact",
		Required: true,
	}
	bootstrapAcceptanceCheckpointFlag = &cli.StringFlag{
		Name:  "checkpoint-block",
		Usage: "Checkpoint block number in decimal/hex, or latest",
		Value: "latest",
	}
	bootstrapAcceptanceConfirmationsFlag = &cli.Uint64Flag{
		Name:  "min-confirmations",
		Usage: "Minimum blocks required above the acceptance checkpoint",
		Value: 0,
	}
	usdbBootstrapAcceptanceCommand = &cli.Command{
		Name:  "usdb-bootstrap-acceptance",
		Usage: "Create or verify a UIP-0010 bootstrap acceptance checkpoint",
		Subcommands: []*cli.Command{
			{
				Name:   "create",
				Usage:  "Create an artifact after SourceDAO strict validation",
				Action: createUSDBBootstrapAcceptance,
				Flags: []cli.Flag{
					bootstrapAcceptanceRPCURLFlag,
					bootstrapAcceptanceGenesisFlag,
					bootstrapAcceptanceConfigFlag,
					bootstrapAcceptanceStateFlag,
					bootstrapAcceptanceValidationFlag,
					bootstrapAcceptanceArtifactFlag,
					bootstrapAcceptanceCheckpointFlag,
					bootstrapAcceptanceConfirmationsFlag,
				},
			},
			{
				Name:   "verify",
				Usage:  "Reject a chain or release bundle that differs from an accepted checkpoint",
				Action: verifyUSDBBootstrapAcceptance,
				Flags: []cli.Flag{
					bootstrapAcceptanceRPCURLFlag,
					bootstrapAcceptanceGenesisFlag,
					bootstrapAcceptanceConfigFlag,
					bootstrapAcceptanceStateFlag,
					bootstrapAcceptanceValidationFlag,
					bootstrapAcceptanceArtifactFlag,
				},
			},
		},
	}
)

func createUSDBBootstrapAcceptance(ctx *cli.Context) error {
	checkpoint, err := parseAcceptanceCheckpoint(ctx.String(bootstrapAcceptanceCheckpointFlag.Name))
	if err != nil {
		return err
	}
	chain, err := observeAcceptanceChain(
		ctx.Context,
		ctx.String(bootstrapAcceptanceRPCURLFlag.Name),
		checkpoint,
		ctx.Uint64(bootstrapAcceptanceConfirmationsFlag.Name),
	)
	if err != nil {
		return err
	}
	artifact, err := usdbacceptance.Create(acceptanceInputFiles(ctx), chain)
	if err != nil {
		return fmt.Errorf("create USDB bootstrap acceptance artifact: %w", err)
	}
	if err := usdbacceptance.WriteArtifact(ctx.Path(bootstrapAcceptanceArtifactFlag.Name), artifact); err != nil {
		return err
	}
	fmt.Printf(
		"Created USDB bootstrap acceptance checkpoint: chain=%d block=%d hash=%s stateRoot=%s\n",
		artifact.ChainID,
		artifact.Checkpoint.Number,
		artifact.Checkpoint.Hash,
		artifact.Checkpoint.StateRoot,
	)
	return nil
}

func verifyUSDBBootstrapAcceptance(ctx *cli.Context) error {
	artifact, err := usdbacceptance.ReadArtifact(ctx.Path(bootstrapAcceptanceArtifactFlag.Name))
	if err != nil {
		return err
	}
	checkpoint := artifact.Checkpoint.Number
	chain, err := observeAcceptanceChain(
		ctx.Context,
		ctx.String(bootstrapAcceptanceRPCURLFlag.Name),
		&checkpoint,
		artifact.ConfirmationDepth,
	)
	if err != nil {
		return err
	}
	if err := usdbacceptance.Verify(artifact, acceptanceInputFiles(ctx), chain); err != nil {
		return fmt.Errorf("USDB bootstrap acceptance rejected: %w", err)
	}
	fmt.Printf(
		"Verified USDB bootstrap acceptance checkpoint: chain=%d block=%d hash=%s stateRoot=%s\n",
		artifact.ChainID,
		artifact.Checkpoint.Number,
		artifact.Checkpoint.Hash,
		artifact.Checkpoint.StateRoot,
	)
	return nil
}

func acceptanceInputFiles(ctx *cli.Context) usdbacceptance.InputFiles {
	return usdbacceptance.InputFiles{
		GenesisJSON:     ctx.Path(bootstrapAcceptanceGenesisFlag.Name),
		BootstrapConfig: ctx.Path(bootstrapAcceptanceConfigFlag.Name),
		BootstrapState:  ctx.Path(bootstrapAcceptanceStateFlag.Name),
		Validation:      ctx.Path(bootstrapAcceptanceValidationFlag.Name),
	}
}

func parseAcceptanceCheckpoint(value string) (*uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "latest" {
		return nil, nil
	}
	number, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid checkpoint block %q", value)
	}
	return &number, nil
}

func observeAcceptanceChain(parent context.Context, rpcURL string, checkpoint *uint64, confirmations uint64) (usdbacceptance.ChainIdentity, error) {
	ctx, cancel := context.WithTimeout(parent, bootstrapAcceptanceRPCTimeout)
	defer cancel()

	rpcClient, err := rpc.DialContext(ctx, rpcURL)
	if err != nil {
		return usdbacceptance.ChainIdentity{}, fmt.Errorf("connect to USDB RPC: %w", err)
	}
	defer rpcClient.Close()
	client := ethclient.NewClient(rpcClient)

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return usdbacceptance.ChainIdentity{}, fmt.Errorf("read USDB chain ID: %w", err)
	}
	if !chainID.IsUint64() || chainID.Sign() <= 0 {
		return usdbacceptance.ChainIdentity{}, errors.New("USDB chain ID does not fit uint64")
	}
	head, err := client.BlockNumber(ctx)
	if err != nil {
		return usdbacceptance.ChainIdentity{}, fmt.Errorf("read USDB head: %w", err)
	}
	checkpointNumber := head
	if checkpoint != nil {
		checkpointNumber = *checkpoint
	}
	if checkpointNumber > head {
		return usdbacceptance.ChainIdentity{}, fmt.Errorf("checkpoint block %d is above head %d", checkpointNumber, head)
	}
	genesisHeader, err := client.HeaderByNumber(ctx, big.NewInt(0))
	if err != nil {
		return usdbacceptance.ChainIdentity{}, fmt.Errorf("read USDB genesis header: %w", err)
	}
	checkpointHeader, err := client.HeaderByNumber(ctx, new(big.Int).SetUint64(checkpointNumber))
	if err != nil {
		return usdbacceptance.ChainIdentity{}, fmt.Errorf("read USDB checkpoint header %d: %w", checkpointNumber, err)
	}
	var transactions []common.Hash
	for number := uint64(1); number <= checkpointNumber; number++ {
		block, err := client.BlockByNumber(ctx, new(big.Int).SetUint64(number))
		if err != nil {
			return usdbacceptance.ChainIdentity{}, fmt.Errorf("read USDB bootstrap block %d: %w", number, err)
		}
		for _, transaction := range block.Transactions() {
			transactions = append(transactions, transaction.Hash())
		}
	}
	sort.Slice(transactions, func(left, right int) bool {
		return strings.Compare(transactions[left].Hex(), transactions[right].Hex()) < 0
	})
	currentCheckpointHeader, err := client.HeaderByNumber(ctx, new(big.Int).SetUint64(checkpointNumber))
	if err != nil {
		return usdbacceptance.ChainIdentity{}, fmt.Errorf("re-read USDB checkpoint header %d: %w", checkpointNumber, err)
	}
	if currentCheckpointHeader.Hash() != checkpointHeader.Hash() || currentCheckpointHeader.Root != checkpointHeader.Root {
		return usdbacceptance.ChainIdentity{}, fmt.Errorf("USDB checkpoint block %d changed during acceptance inspection", checkpointNumber)
	}
	head, err = client.BlockNumber(ctx)
	if err != nil {
		return usdbacceptance.ChainIdentity{}, fmt.Errorf("re-read USDB head: %w", err)
	}
	return usdbacceptance.ChainIdentity{
		ChainID:     chainID.Uint64(),
		GenesisHash: genesisHeader.Hash(),
		HeadNumber:  head,
		Checkpoint: usdbacceptance.BlockIdentity{
			Number:    checkpointNumber,
			Hash:      checkpointHeader.Hash(),
			StateRoot: checkpointHeader.Root,
		},
		Confirmations: confirmations,
		Transactions:  transactions,
	}, nil
}
