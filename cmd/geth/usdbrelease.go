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
	"fmt"

	"github.com/ethereum/go-ethereum/internal/usdbrelease"
	"github.com/urfave/cli/v2"
)

var (
	usdbReleaseIDFlag = &cli.StringFlag{
		Name:     "release-id",
		Usage:    "Expected stable USDB public release identifier",
		Required: true,
	}
	usdbReleaseNetworkIDFlag = &cli.Uint64Flag{
		Name:     "network-id",
		Usage:    "Expected USDB devp2p network ID",
		Required: true,
	}
	usdbReleaseGenesisFlag = &cli.PathFlag{
		Name:     "genesis",
		Usage:    "Canonical USDB genesis JSON",
		Required: true,
	}
	usdbReleaseAcceptanceFlag = &cli.PathFlag{
		Name:     "acceptance",
		Usage:    "Verified UIP-0010 bootstrap acceptance artifact",
		Required: true,
	}
	usdbReleaseBootnodesFlag = &cli.PathFlag{
		Name:     "bootnodes",
		Usage:    "Canonical comma/newline-separated enode list",
		Required: true,
	}
	usdbReleaseManifestFlag = &cli.PathFlag{
		Name:     "manifest",
		Usage:    "Signed USDB public release manifest",
		Required: true,
	}
	usdbReleaseSignatureFlag = &cli.PathFlag{
		Name:     "signature",
		Usage:    "Detached USDB public release manifest signature",
		Required: true,
	}
	usdbReleasePrivateKeyFlag = &cli.PathFlag{
		Name:     "private-key",
		Usage:    "Unencrypted PKCS#8 PEM Ed25519 release signing key",
		Required: true,
	}
	usdbReleaseKeyIDFlag = &cli.StringFlag{
		Name:     "key-id",
		Usage:    "Release signing key identifier",
		Required: true,
	}
	usdbReleaseTrustedKeysFlag = &cli.PathFlag{
		Name:     "trusted-keys",
		Usage:    "Trusted USDB public release Ed25519 keys",
		Required: true,
	}
	usdbReleaseManifestCommand = &cli.Command{
		Name:  "usdb-release-manifest",
		Usage: "Create or verify a signed UIP-0010 public release bundle",
		Subcommands: []*cli.Command{
			{
				Name:   "create",
				Usage:  "Create and sign a release manifest from accepted artifacts",
				Action: createUSDBReleaseManifest,
				Flags: []cli.Flag{
					usdbReleaseIDFlag,
					usdbReleaseNetworkIDFlag,
					usdbReleaseGenesisFlag,
					usdbReleaseAcceptanceFlag,
					usdbReleaseBootnodesFlag,
					usdbReleaseManifestFlag,
					usdbReleaseSignatureFlag,
					usdbReleasePrivateKeyFlag,
					usdbReleaseKeyIDFlag,
				},
			},
			{
				Name:   "verify",
				Usage:  "Verify release provenance and exact local artifact commitments",
				Action: verifyUSDBReleaseManifest,
				Flags: []cli.Flag{
					usdbReleaseIDFlag,
					usdbReleaseNetworkIDFlag,
					usdbReleaseGenesisFlag,
					usdbReleaseAcceptanceFlag,
					usdbReleaseBootnodesFlag,
					usdbReleaseManifestFlag,
					usdbReleaseSignatureFlag,
					usdbReleaseTrustedKeysFlag,
				},
			},
		},
	}
)

func createUSDBReleaseManifest(ctx *cli.Context) error {
	files := releaseInputFiles(ctx)
	manifest, err := usdbrelease.Create(
		ctx.String(usdbReleaseIDFlag.Name),
		ctx.Uint64(usdbReleaseNetworkIDFlag.Name),
		files,
	)
	if err != nil {
		return fmt.Errorf("create USDB public release manifest: %w", err)
	}
	manifestJSON, err := usdbrelease.WriteManifest(ctx.Path(usdbReleaseManifestFlag.Name), manifest)
	if err != nil {
		return err
	}
	privateKey, err := usdbrelease.ReadPrivateKey(ctx.Path(usdbReleasePrivateKeyFlag.Name))
	if err != nil {
		return err
	}
	signature, err := usdbrelease.Sign(manifestJSON, ctx.String(usdbReleaseKeyIDFlag.Name), privateKey)
	if err != nil {
		return err
	}
	if err := usdbrelease.WriteSignature(ctx.Path(usdbReleaseSignatureFlag.Name), signature); err != nil {
		return err
	}
	fmt.Printf(
		"Created signed USDB public release: release=%s network=%d chain=%d checkpoint=%d key=%s\n",
		manifest.ReleaseID,
		manifest.NetworkID,
		manifest.ChainID,
		manifest.Acceptance.Checkpoint.Number,
		signature.KeyID,
	)
	return nil
}

func verifyUSDBReleaseManifest(ctx *cli.Context) error {
	manifest, manifestJSON, err := usdbrelease.ReadManifest(ctx.Path(usdbReleaseManifestFlag.Name))
	if err != nil {
		return err
	}
	signature, err := usdbrelease.ReadSignature(ctx.Path(usdbReleaseSignatureFlag.Name))
	if err != nil {
		return err
	}
	trustedKeys, err := usdbrelease.ReadTrustedKeys(ctx.Path(usdbReleaseTrustedKeysFlag.Name))
	if err != nil {
		return err
	}
	if err := usdbrelease.VerifySignature(manifestJSON, signature, trustedKeys); err != nil {
		return fmt.Errorf("verify USDB public release signature: %w", err)
	}
	if err := usdbrelease.Verify(
		manifest,
		ctx.String(usdbReleaseIDFlag.Name),
		ctx.Uint64(usdbReleaseNetworkIDFlag.Name),
		releaseInputFiles(ctx),
	); err != nil {
		return fmt.Errorf("verify USDB public release artifacts: %w", err)
	}
	fmt.Printf(
		"Verified signed USDB public release: release=%s network=%d chain=%d checkpoint=%d key=%s\n",
		manifest.ReleaseID,
		manifest.NetworkID,
		manifest.ChainID,
		manifest.Acceptance.Checkpoint.Number,
		signature.KeyID,
	)
	return nil
}

func releaseInputFiles(ctx *cli.Context) usdbrelease.InputFiles {
	return usdbrelease.InputFiles{
		GenesisJSON:        ctx.Path(usdbReleaseGenesisFlag.Name),
		AcceptanceArtifact: ctx.Path(usdbReleaseAcceptanceFlag.Name),
		Bootnodes:          ctx.Path(usdbReleaseBootnodesFlag.Name),
	}
}
