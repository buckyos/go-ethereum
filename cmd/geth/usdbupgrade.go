package main

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/internal/usdbupgrade"
	"github.com/urfave/cli/v2"
)

var usdbUpgradeConfigCommand = &cli.Command{
	Name:  "usdb-upgrade-config",
	Usage: "Inspect or apply an append-only USDB configuration to an existing stopped chain",
	Flags: []cli.Flag{utils.DataDirFlag,
		&cli.PathFlag{Name: "source-genesis", Required: true},
		&cli.PathFlag{Name: "target-genesis", Required: true},
		&cli.PathFlag{Name: "expected-report"},
		&cli.BoolFlag{Name: "apply"}},
	Action: func(ctx *cli.Context) error {
		if ctx.Args().Len() != 0 || !ctx.IsSet("datadir") {
			return errors.New("explicit --datadir and no positional arguments are required")
		}
		load := func(path string, target interface{}) error {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return decodeStrictJSON(data, target)
		}
		var source, target core.Genesis
		if err := load(ctx.Path("source-genesis"), &source); err != nil {
			return err
		}
		if err := load(ctx.Path("target-genesis"), &target); err != nil {
			return err
		}
		var expected *usdbupgrade.Report
		if ctx.IsSet("expected-report") {
			expected = &usdbupgrade.Report{}
			if err := load(ctx.Path("expected-report"), expected); err != nil {
				return err
			}
		}
		report, err := usdbupgrade.Run(ctx.Path("datadir"), &source, &target, expected, ctx.Bool("apply"))
		if err != nil {
			return err
		}
		return json.NewEncoder(ctx.App.Writer).Encode(report)
	},
}
