// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package usdbrelease

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/internal/usdbacceptance"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
)

const (
	testReleaseID = "usdb-public-release-e2e-v1"
	testNetworkID = uint64(20260323)
	testKeyID     = "release-e2e-key-1"
)

type releaseFixture struct {
	files      InputFiles
	acceptance *usdbacceptance.Artifact
	privateKey ed25519.PrivateKey
	trusted    *TrustedKeys
}

func TestCreateSignAndVerify(t *testing.T) {
	fixture := newReleaseFixture(t)
	manifest, err := Create(testReleaseID, testNetworkID, fixture.files)
	if err != nil {
		t.Fatalf("create release manifest: %v", err)
	}
	manifestJSON, err := EncodeManifest(manifest)
	if err != nil {
		t.Fatalf("encode release manifest: %v", err)
	}
	signature, err := Sign(manifestJSON, testKeyID, fixture.privateKey)
	if err != nil {
		t.Fatalf("sign release manifest: %v", err)
	}
	if err := VerifySignature(manifestJSON, signature, fixture.trusted); err != nil {
		t.Fatalf("verify release signature: %v", err)
	}
	if err := Verify(manifest, testReleaseID, testNetworkID, fixture.files); err != nil {
		t.Fatalf("verify release manifest: %v", err)
	}
	if manifest.Acceptance.ConfirmationDepth != fixture.acceptance.ConfirmationDepth {
		t.Fatalf("confirmation depth=%d, want %d", manifest.Acceptance.ConfirmationDepth, fixture.acceptance.ConfirmationDepth)
	}
	if manifest.FeePolicy.DividendAddress != fixture.acceptance.Bootstrap.Validation.DividendAddress {
		t.Fatalf("manifest Dividend address does not match acceptance")
	}
}

func TestVerifyRejectsArtifactDrift(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(t *testing.T, fixture *releaseFixture)
	}{
		{
			name: "genesis",
			tamper: func(t *testing.T, fixture *releaseFixture) {
				content, err := os.ReadFile(fixture.files.GenesisJSON)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(fixture.files.GenesisJSON, append(content, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "acceptance",
			tamper: func(t *testing.T, fixture *releaseFixture) {
				replacement := *fixture.acceptance
				replacement.Checkpoint.Hash = common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
				if err := usdbacceptance.WriteArtifact(fixture.files.AcceptanceArtifact, &replacement); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "bootnodes",
			tamper: func(t *testing.T, fixture *releaseFixture) {
				second := testENode(t)
				file, err := os.OpenFile(fixture.files.Bootnodes, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				if _, err := file.WriteString(second + "\n"); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReleaseFixture(t)
			manifest, err := Create(testReleaseID, testNetworkID, fixture.files)
			if err != nil {
				t.Fatal(err)
			}
			test.tamper(t, fixture)
			if err := Verify(manifest, testReleaseID, testNetworkID, fixture.files); err == nil {
				t.Fatalf("tampered %s was accepted", test.name)
			}
		})
	}
}

func TestVerifyRejectsWrongReleaseAndNetwork(t *testing.T) {
	fixture := newReleaseFixture(t)
	manifest, err := Create(testReleaseID, testNetworkID, fixture.files)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(manifest, "different-release", testNetworkID, fixture.files); err == nil {
		t.Fatal("wrong release ID was accepted")
	}
	if err := Verify(manifest, testReleaseID, testNetworkID+1, fixture.files); err == nil {
		t.Fatal("wrong network ID was accepted")
	}
}

func TestVerifySignatureRejectsTamperingAndUntrustedKey(t *testing.T) {
	fixture := newReleaseFixture(t)
	manifest, err := Create(testReleaseID, testNetworkID, fixture.files)
	if err != nil {
		t.Fatal(err)
	}
	manifestJSON, err := EncodeManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := Sign(manifestJSON, testKeyID, fixture.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	tamperedManifest := append([]byte(nil), manifestJSON...)
	tamperedManifest[len(tamperedManifest)-2] ^= 1
	if err := VerifySignature(tamperedManifest, signature, fixture.trusted); err == nil {
		t.Fatal("tampered manifest was accepted")
	}

	rawSignature, err := base64.StdEncoding.DecodeString(signature.SignatureBase64)
	if err != nil {
		t.Fatal(err)
	}
	rawSignature[0] ^= 1
	tamperedSignature := *signature
	tamperedSignature.SignatureBase64 = base64.StdEncoding.EncodeToString(rawSignature)
	if err := VerifySignature(manifestJSON, &tamperedSignature, fixture.trusted); err == nil {
		t.Fatal("tampered signature was accepted")
	}

	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	untrusted := &TrustedKeys{
		SchemaVersion: TrustedKeysSchemaVersion,
		Keys: []TrustedKey{{
			KeyID:           "different-key",
			Algorithm:       SignatureAlgorithm,
			PublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey),
		}},
	}
	if err := VerifySignature(manifestJSON, signature, untrusted); err == nil {
		t.Fatal("signature from an untrusted key was accepted")
	}
}

func TestPublicReleaseRequiresConfirmations(t *testing.T) {
	fixture := newReleaseFixture(t)
	fixture.acceptance.ConfirmationDepth = 0
	if err := usdbacceptance.WriteArtifact(fixture.files.AcceptanceArtifact, fixture.acceptance); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(testReleaseID, testNetworkID, fixture.files); err == nil {
		t.Fatal("zero-confirmation acceptance was promoted to public release")
	}
}

func TestStrictArtifactReadersRejectDuplicateKeys(t *testing.T) {
	fixture := newReleaseFixture(t)
	manifest, err := Create(testReleaseID, testNetworkID, fixture.files)
	if err != nil {
		t.Fatal(err)
	}
	manifestJSON, err := EncodeManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := bytesWithDuplicateSchema(manifestJSON)
	path := filepath.Join(t.TempDir(), "duplicate-manifest.json")
	if err := os.WriteFile(path, duplicate, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadManifest(path); err == nil {
		t.Fatal("manifest with duplicate schema_version was accepted")
	}
}

func TestReadPrivateKey(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "release-key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadPrivateKey(path)
	if err != nil {
		t.Fatalf("read private key: %v", err)
	}
	if !loaded.Equal(privateKey) {
		t.Fatal("loaded release private key differs")
	}
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	root := t.TempDir()
	dividendAddress := common.HexToAddress("0x0000000000000000000000000000000000001002")
	dividendCodeHash := common.HexToHash("0x83a65baf939d7c7703d15fcaf7e7e6c0102d7fc7ddbf378b9166c2be5d2ff046")
	config := *params.AllEthashProtocolChanges
	config.ChainID = new(big.Int).SetUint64(testNetworkID)
	config.DividendFeeSplitBlock = big.NewInt(256)
	config.DividendAddress = dividendAddress
	config.DividendCodeHash = dividendCodeHash
	genesisPath := filepath.Join(root, "genesis.json")
	writeJSONFile(t, genesisPath, map[string]interface{}{"config": &config})
	genesisSHA256 := fileDigest(t, genesisPath)

	modules := make(map[string]usdbacceptance.ModuleIdentity)
	for index, name := range []string{"acquired", "committee", "devToken", "dividend", "lockup", "normalToken", "project"} {
		address := common.BigToAddress(big.NewInt(int64(index + 0x2001)))
		if name == "dividend" {
			address = dividendAddress
		}
		modules[name] = usdbacceptance.ModuleIdentity{
			Address: address,
			Version: "1",
		}
	}
	validation := usdbacceptance.ValidationIdentity{
		ChainID:         testNetworkID,
		DAOAddress:      common.HexToAddress("0x0000000000000000000000000000000000001001"),
		DividendAddress: dividendAddress,
		BootstrapAdmin:  common.HexToAddress("0xabCd35AfbB4561213fEAfF01B5F91e18F8Df7c37"),
		Modules:         modules,
	}
	validationJSON, err := json.Marshal(validation)
	if err != nil {
		t.Fatal(err)
	}
	validationDigest := sha256.Sum256(validationJSON)
	acceptance := &usdbacceptance.Artifact{
		SchemaVersion: usdbacceptance.SchemaVersion,
		ChainID:       testNetworkID,
		Genesis: usdbacceptance.GenesisCommitment{
			JSONSHA256: genesisSHA256,
			BlockHash:  common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
		},
		Checkpoint: usdbacceptance.BlockIdentity{
			Number:    100,
			Hash:      common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
			StateRoot: common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333"),
		},
		ConfirmationDepth: 6,
		Bootstrap: usdbacceptance.BootstrapCommitment{
			ConfigSHA256:             strings.Repeat("a", 64),
			StateSHA256:              strings.Repeat("b", 64),
			ValidationIdentitySHA256: hex.EncodeToString(validationDigest[:]),
			MaxOperationBlock:        90,
			OperationTransactions: []common.Hash{
				common.HexToHash("0x4444444444444444444444444444444444444444444444444444444444444444"),
			},
			Validation: validation,
		},
	}
	acceptancePath := filepath.Join(root, "acceptance.json")
	if err := usdbacceptance.WriteArtifact(acceptancePath, acceptance); err != nil {
		t.Fatalf("write acceptance fixture: %v", err)
	}
	bootnodesPath := filepath.Join(root, "bootnodes.txt")
	if err := os.WriteFile(bootnodesPath, []byte(testENode(t)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &releaseFixture{
		files: InputFiles{
			GenesisJSON:        genesisPath,
			AcceptanceArtifact: acceptancePath,
			Bootnodes:          bootnodesPath,
		},
		acceptance: acceptance,
		privateKey: privateKey,
		trusted: &TrustedKeys{
			SchemaVersion: TrustedKeysSchemaVersion,
			Keys: []TrustedKey{{
				KeyID:           testKeyID,
				Algorithm:       SignatureAlgorithm,
				PublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey),
			}},
		},
	}
}

func testENode(t *testing.T) string {
	t.Helper()
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return enode.NewV4(&privateKey.PublicKey, net.ParseIP("127.0.0.1"), 31303, 31303).String()
}

func writeJSONFile(t *testing.T, path string, value interface{}) {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func bytesWithDuplicateSchema(input []byte) []byte {
	needle := []byte(`"schema_version": "` + ManifestSchemaVersion + `"`)
	replacement := []byte(`"schema_version": "` + ManifestSchemaVersion + `", "schema_version": "` + ManifestSchemaVersion + `"`)
	return bytes.Replace(input, needle, replacement, 1)
}
