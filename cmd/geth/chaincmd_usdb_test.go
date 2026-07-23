package main

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/cmd/utils"
	internalflags "github.com/ethereum/go-ethereum/internal/flags"
	"github.com/urfave/cli/v2"
)

func TestChainCommandsExposeUSDBValidatorOperationalFlags(t *testing.T) {
	for _, command := range []*cli.Command{importCommand, exportCommand} {
		for _, expected := range []string{
			utils.EthashUSDBIndexerRPCURLFlag.Name,
			utils.EthashUSDBIndexerTimeoutFlag.Name,
		} {
			found := false
			for _, commandFlag := range command.Flags {
				for _, name := range commandFlag.Names() {
					if name == expected {
						found = true
						break
					}
				}
			}
			if !found {
				t.Errorf("%s command is missing --%s", command.Name, expected)
			}
		}
	}
}

func TestChainCommandContextReadsUSDBValidatorOperationalFlags(t *testing.T) {
	const endpoint = "http://127.0.0.1:39920"
	for _, command := range []*cli.Command{importCommand, exportCommand} {
		t.Run(command.Name, func(t *testing.T) {
			var gotEndpoint string
			var gotTimeout time.Duration
			var endpointSet bool
			var timeoutSet bool
			app := cli.NewApp()
			app.Before = func(ctx *cli.Context) error {
				internalflags.MigrateGlobalFlags(ctx)
				return nil
			}
			app.Flags = []cli.Flag{
				utils.EthashUSDBIndexerRPCURLFlag,
				utils.EthashUSDBIndexerTimeoutFlag,
			}
			app.Commands = []*cli.Command{{
				Name:  command.Name,
				Flags: command.Flags,
				Action: func(ctx *cli.Context) error {
					endpointSet = ctx.IsSet(utils.EthashUSDBIndexerRPCURLFlag.Name)
					timeoutSet = ctx.IsSet(utils.EthashUSDBIndexerTimeoutFlag.Name)
					gotEndpoint = ctx.String(utils.EthashUSDBIndexerRPCURLFlag.Name)
					gotTimeout = ctx.Duration(utils.EthashUSDBIndexerTimeoutFlag.Name)
					return nil
				},
			}}
			err := app.Run([]string{
				"geth",
				command.Name,
				"--" + utils.EthashUSDBIndexerRPCURLFlag.Name,
				endpoint,
				"--" + utils.EthashUSDBIndexerTimeoutFlag.Name,
				"1s",
			})
			if err != nil {
				t.Fatalf("parse %s flags: %v", command.Name, err)
			}
			if gotEndpoint != endpoint {
				t.Fatalf("unexpected endpoint: have %q want %q", gotEndpoint, endpoint)
			}
			if !endpointSet || !timeoutSet {
				t.Fatalf("operational flags were not marked set: endpoint=%t timeout=%t", endpointSet, timeoutSet)
			}
			if gotTimeout != time.Second {
				t.Fatalf("unexpected timeout: have %s want %s", gotTimeout, time.Second)
			}
		})
	}
}
