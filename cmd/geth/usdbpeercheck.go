package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/ethereum/go-ethereum/internal/peercheck"
	"github.com/urfave/cli/v2"
)

var usdbPeerCheckCommand = &cli.Command{
	Name:        "usdb-peer-check",
	Usage:       "Diagnose a peer without changing node state (JSON request on stdin)",
	Description: "Read enode, network_id, chain_id, genesis_hash, genesis and timeout_secs from JSON stdin. Emit usdb-peer-check:v1 JSON. No chain database or persistent identity is used.",
	Action: func(ctx *cli.Context) error {
		if ctx.Args().Len() != 0 {
			return errors.New("usdb-peer-check takes its request from stdin")
		}
		probeCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return runPeerCheck(probeCtx, os.Stdin, ctx.App.Writer)
	},
}

func runPeerCheck(ctx context.Context, input io.Reader, output io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(input, 2*1024*1024+1))
	if err != nil {
		return err
	}
	if len(data) > 2*1024*1024 {
		return errors.New("peer check request exceeds 2 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var req peercheck.Request
	if err := decoder.Decode(&req); err != nil {
		return err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("request must contain exactly one JSON object")
	}
	report, err := peercheck.Run(ctx, req)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(report)
}
