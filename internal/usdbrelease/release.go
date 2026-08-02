// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package usdbrelease defines the signed artifact bundle used to promote an
// accepted USDB bootstrap history into a named public network release.
package usdbrelease

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/internal/usdbacceptance"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
)

const (
	// ManifestSchemaVersion identifies the first signed public release manifest.
	ManifestSchemaVersion = "uip-0010-public-release-manifest:v1"
	// SignatureSchemaVersion identifies the detached manifest signature format.
	SignatureSchemaVersion = "uip-0010-public-release-signature:v1"
	// TrustedKeysSchemaVersion identifies the release-signing trust set format.
	TrustedKeysSchemaVersion = "uip-0010-public-release-trusted-keys:v1"
	// SignatureAlgorithm is the only release signature algorithm supported by v1.
	SignatureAlgorithm = "ed25519"
)

var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// InputFiles names the exact artifacts committed by a public release manifest.
type InputFiles struct {
	GenesisJSON        string
	AcceptanceArtifact string
	Bootnodes          string
}

// GenesisCommitment binds canonical genesis bytes and the resulting block hash.
type GenesisCommitment struct {
	JSONSHA256 string      `json:"json_sha256"`
	BlockHash  common.Hash `json:"block_hash"`
}

// AcceptanceCommitment binds the accepted bootstrap history promoted by release.
type AcceptanceCommitment struct {
	ArtifactSHA256    string                       `json:"artifact_sha256"`
	SchemaVersion     string                       `json:"schema_version"`
	Checkpoint        usdbacceptance.BlockIdentity `json:"checkpoint"`
	ConfirmationDepth uint64                       `json:"confirmation_depth"`
}

// BootnodesCommitment binds both the distributed file and normalized node URLs.
type BootnodesCommitment struct {
	FileSHA256 string   `json:"file_sha256"`
	ENodes     []string `json:"enodes"`
}

// FeePolicyCommitment exposes the genesis-fixed Dividend gate to release tools.
type FeePolicyCommitment struct {
	DividendAddress  common.Address `json:"dividend_address"`
	DividendCodeHash common.Hash    `json:"dividend_code_hash"`
	ActivationBlock  uint64         `json:"activation_block"`
}

// Manifest is the deterministic unsigned body covered by the release signature.
type Manifest struct {
	SchemaVersion string               `json:"schema_version"`
	ReleaseID     string               `json:"release_id"`
	NetworkID     uint64               `json:"network_id"`
	ChainID       uint64               `json:"chain_id"`
	Genesis       GenesisCommitment    `json:"genesis"`
	Acceptance    AcceptanceCommitment `json:"acceptance"`
	Bootnodes     BootnodesCommitment  `json:"bootnodes"`
	FeePolicy     FeePolicyCommitment  `json:"fee_policy"`
}

// Signature is the detached Ed25519 signature over the exact manifest bytes.
type Signature struct {
	SchemaVersion   string `json:"schema_version"`
	Algorithm       string `json:"algorithm"`
	KeyID           string `json:"key_id"`
	ManifestSHA256  string `json:"manifest_sha256"`
	SignatureBase64 string `json:"signature_base64"`
}

// TrustedKey binds a release key identifier to one Ed25519 public key.
type TrustedKey struct {
	KeyID           string `json:"key_id"`
	Algorithm       string `json:"algorithm"`
	PublicKeyBase64 string `json:"public_key_base64"`
}

// TrustedKeys is the explicit trust set used by a public network joiner.
type TrustedKeys struct {
	SchemaVersion string       `json:"schema_version"`
	Keys          []TrustedKey `json:"keys"`
}

type genesisEnvelope struct {
	Config *params.ChainConfig `json:"config"`
}

// Create derives a deterministic release manifest from canonical local files.
func Create(releaseID string, networkID uint64, files InputFiles) (*Manifest, error) {
	if !identifierPattern.MatchString(releaseID) {
		return nil, fmt.Errorf("invalid release ID %q", releaseID)
	}
	if networkID == 0 {
		return nil, errors.New("network ID must be non-zero")
	}
	if files.GenesisJSON == "" || files.AcceptanceArtifact == "" || files.Bootnodes == "" {
		return nil, errors.New("genesis, acceptance, and bootnodes files are required")
	}
	acceptance, err := usdbacceptance.ReadArtifact(files.AcceptanceArtifact)
	if err != nil {
		return nil, fmt.Errorf("read bootstrap acceptance artifact: %w", err)
	}
	if acceptance.ConfirmationDepth == 0 {
		return nil, errors.New("public release requires a non-zero acceptance confirmation depth")
	}
	genesisSHA256, config, err := readGenesis(files.GenesisJSON)
	if err != nil {
		return nil, err
	}
	if genesisSHA256 != acceptance.Genesis.JSONSHA256 {
		return nil, errors.New("canonical genesis JSON does not match bootstrap acceptance")
	}
	if config.ChainID == nil || !config.ChainID.IsUint64() || config.ChainID.Sign() <= 0 {
		return nil, errors.New("canonical genesis chain ID does not fit uint64")
	}
	if config.ChainID.Uint64() != acceptance.ChainID {
		return nil, fmt.Errorf("genesis chain ID %d does not match acceptance chain ID %d", config.ChainID.Uint64(), acceptance.ChainID)
	}
	if config.DividendFeeSplitBlock == nil ||
		!config.DividendFeeSplitBlock.IsUint64() ||
		config.DividendFeeSplitBlock.Sign() <= 0 ||
		config.DividendAddress == (common.Address{}) ||
		config.DividendCodeHash == (common.Hash{}) {
		return nil, errors.New("canonical genesis has incomplete Dividend fee policy")
	}
	if config.DividendAddress != acceptance.Bootstrap.Validation.DividendAddress {
		return nil, errors.New("genesis Dividend address does not match accepted validation identity")
	}
	bootnodesSHA256, bootnodes, err := readBootnodes(files.Bootnodes)
	if err != nil {
		return nil, err
	}
	acceptanceSHA256, err := fileSHA256(files.AcceptanceArtifact)
	if err != nil {
		return nil, fmt.Errorf("hash bootstrap acceptance artifact: %w", err)
	}
	manifest := &Manifest{
		SchemaVersion: ManifestSchemaVersion,
		ReleaseID:     releaseID,
		NetworkID:     networkID,
		ChainID:       acceptance.ChainID,
		Genesis: GenesisCommitment{
			JSONSHA256: genesisSHA256,
			BlockHash:  acceptance.Genesis.BlockHash,
		},
		Acceptance: AcceptanceCommitment{
			ArtifactSHA256:    acceptanceSHA256,
			SchemaVersion:     acceptance.SchemaVersion,
			Checkpoint:        acceptance.Checkpoint,
			ConfirmationDepth: acceptance.ConfirmationDepth,
		},
		Bootnodes: BootnodesCommitment{
			FileSHA256: bootnodesSHA256,
			ENodes:     bootnodes,
		},
		FeePolicy: FeePolicyCommitment{
			DividendAddress:  config.DividendAddress,
			DividendCodeHash: config.DividendCodeHash,
			ActivationBlock:  config.DividendFeeSplitBlock.Uint64(),
		},
	}
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

// Verify checks a manifest against the expected public network and local files.
func Verify(manifest *Manifest, expectedReleaseID string, expectedNetworkID uint64, files InputFiles) error {
	if manifest == nil {
		return errors.New("release manifest is nil")
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if manifest.ReleaseID != expectedReleaseID {
		return fmt.Errorf("release ID mismatch: have=%q want=%q", manifest.ReleaseID, expectedReleaseID)
	}
	if manifest.NetworkID != expectedNetworkID {
		return fmt.Errorf("network ID mismatch: have=%d want=%d", manifest.NetworkID, expectedNetworkID)
	}
	expected, err := Create(expectedReleaseID, expectedNetworkID, files)
	if err != nil {
		return err
	}
	actualJSON, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode release manifest: %w", err)
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		return fmt.Errorf("encode expected release manifest: %w", err)
	}
	if !bytes.Equal(actualJSON, expectedJSON) {
		return errors.New("release manifest does not match canonical local artifacts")
	}
	return nil
}

// EncodeManifest returns the canonical bytes that must be signed and published.
func EncodeManifest(manifest *Manifest) ([]byte, error) {
	if manifest == nil {
		return nil, errors.New("release manifest is nil")
	}
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode release manifest: %w", err)
	}
	return append(encoded, '\n'), nil
}

// Sign creates a detached Ed25519 signature over exact manifest bytes.
func Sign(manifestJSON []byte, keyID string, privateKey ed25519.PrivateKey) (*Signature, error) {
	if !identifierPattern.MatchString(keyID) {
		return nil, fmt.Errorf("invalid signing key ID %q", keyID)
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid Ed25519 private key length %d", len(privateKey))
	}
	digest := sha256.Sum256(manifestJSON)
	return &Signature{
		SchemaVersion:   SignatureSchemaVersion,
		Algorithm:       SignatureAlgorithm,
		KeyID:           keyID,
		ManifestSHA256:  hex.EncodeToString(digest[:]),
		SignatureBase64: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifestJSON)),
	}, nil
}

// VerifySignature verifies exact manifest bytes against an explicit trust set.
func VerifySignature(manifestJSON []byte, signature *Signature, trusted *TrustedKeys) error {
	if err := validateSignature(signature); err != nil {
		return err
	}
	if err := validateTrustedKeys(trusted); err != nil {
		return err
	}
	digest := sha256.Sum256(manifestJSON)
	if signature.ManifestSHA256 != hex.EncodeToString(digest[:]) {
		return errors.New("release manifest digest does not match detached signature")
	}
	var selected *TrustedKey
	for index := range trusted.Keys {
		if trusted.Keys[index].KeyID == signature.KeyID {
			selected = &trusted.Keys[index]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("release signing key %q is not trusted", signature.KeyID)
	}
	publicKey, err := base64.StdEncoding.DecodeString(selected.PublicKeyBase64)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("trusted release key %q has invalid public key", selected.KeyID)
	}
	rawSignature, err := base64.StdEncoding.DecodeString(signature.SignatureBase64)
	if err != nil || len(rawSignature) != ed25519.SignatureSize {
		return errors.New("release signature has invalid encoding")
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), manifestJSON, rawSignature) {
		return errors.New("release manifest signature verification failed")
	}
	return nil
}

// ReadPrivateKey reads an unencrypted PKCS#8 PEM Ed25519 signing key.
func ReadPrivateKey(path string) (ed25519.PrivateKey, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read release signing key: %w", err)
	}
	block, trailing := pem.Decode(content)
	if block == nil || len(bytes.TrimSpace(trailing)) != 0 {
		return nil, errors.New("release signing key must contain one PKCS#8 PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse release signing key: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("release signing key is not an Ed25519 private key")
	}
	return privateKey, nil
}

// ReadManifest decodes a manifest while rejecting unknown or duplicate fields.
func ReadManifest(path string) (*Manifest, []byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read release manifest: %w", err)
	}
	var manifest Manifest
	if err := decodeStrictJSON(content, &manifest); err != nil {
		return nil, nil, fmt.Errorf("decode release manifest: %w", err)
	}
	if err := validateManifest(&manifest); err != nil {
		return nil, nil, err
	}
	return &manifest, content, nil
}

// ReadSignature decodes and validates a detached release signature.
func ReadSignature(path string) (*Signature, error) {
	var signature Signature
	if err := readStrictJSON(path, &signature); err != nil {
		return nil, fmt.Errorf("read release signature: %w", err)
	}
	if err := validateSignature(&signature); err != nil {
		return nil, err
	}
	return &signature, nil
}

// ReadTrustedKeys decodes and validates a public release trust set.
func ReadTrustedKeys(path string) (*TrustedKeys, error) {
	var trusted TrustedKeys
	if err := readStrictJSON(path, &trusted); err != nil {
		return nil, fmt.Errorf("read release trusted keys: %w", err)
	}
	if err := validateTrustedKeys(&trusted); err != nil {
		return nil, err
	}
	return &trusted, nil
}

// WriteManifest writes the deterministic bytes consumed by Sign.
func WriteManifest(path string, manifest *Manifest) ([]byte, error) {
	encoded, err := EncodeManifest(manifest)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return nil, fmt.Errorf("write release manifest: %w", err)
	}
	return encoded, nil
}

// WriteSignature writes a deterministic detached signature artifact.
func WriteSignature(path string, signature *Signature) error {
	if err := validateSignature(signature); err != nil {
		return err
	}
	return writeJSON(path, signature)
}

func readGenesis(path string) (string, *params.ChainConfig, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("read canonical genesis JSON: %w", err)
	}
	if err := rejectDuplicateJSONKeys(content); err != nil {
		return "", nil, fmt.Errorf("decode canonical genesis JSON: %w", err)
	}
	var envelope genesisEnvelope
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&envelope); err != nil {
		return "", nil, fmt.Errorf("decode canonical genesis JSON: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return "", nil, fmt.Errorf("decode canonical genesis JSON: %w", err)
	}
	if envelope.Config == nil {
		return "", nil, errors.New("canonical genesis JSON has no chain config")
	}
	if err := envelope.Config.CheckConfigForkOrder(); err != nil {
		return "", nil, fmt.Errorf("invalid canonical genesis chain config: %w", err)
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), envelope.Config, nil
}

func readBootnodes(path string) (string, []string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("read bootnodes file: %w", err)
	}
	values := strings.FieldsFunc(string(content), func(character rune) bool {
		return character == ',' || character == '\n' || character == '\r' ||
			character == '\t' || character == ' '
	})
	if len(values) == 0 {
		return "", nil, errors.New("public release requires at least one bootnode")
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		node, err := enode.ParseV4(value)
		if err != nil {
			return "", nil, fmt.Errorf("invalid bootnode %q: %w", value, err)
		}
		canonical := node.String()
		if _, exists := seen[canonical]; exists {
			return "", nil, fmt.Errorf("duplicate bootnode %q", canonical)
		}
		seen[canonical] = struct{}{}
		normalized = append(normalized, canonical)
	}
	sort.Strings(normalized)
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), normalized, nil
}

func validateManifest(manifest *Manifest) error {
	if manifest == nil {
		return errors.New("release manifest is nil")
	}
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("unsupported release manifest schema %q", manifest.SchemaVersion)
	}
	if !identifierPattern.MatchString(manifest.ReleaseID) {
		return fmt.Errorf("invalid release ID %q", manifest.ReleaseID)
	}
	if manifest.NetworkID == 0 || manifest.ChainID == 0 {
		return errors.New("release manifest has incomplete network identity")
	}
	if !isSHA256(manifest.Genesis.JSONSHA256) ||
		manifest.Genesis.BlockHash == (common.Hash{}) {
		return errors.New("release manifest has incomplete genesis commitment")
	}
	if !isSHA256(manifest.Acceptance.ArtifactSHA256) ||
		manifest.Acceptance.SchemaVersion != usdbacceptance.SchemaVersion ||
		manifest.Acceptance.Checkpoint.Hash == (common.Hash{}) ||
		manifest.Acceptance.Checkpoint.StateRoot == (common.Hash{}) ||
		manifest.Acceptance.ConfirmationDepth == 0 {
		return errors.New("release manifest has incomplete bootstrap acceptance commitment")
	}
	if !isSHA256(manifest.Bootnodes.FileSHA256) || len(manifest.Bootnodes.ENodes) == 0 {
		return errors.New("release manifest has incomplete bootnodes commitment")
	}
	for index, value := range manifest.Bootnodes.ENodes {
		node, err := enode.ParseV4(value)
		if err != nil || node.String() != value {
			return fmt.Errorf("release manifest has invalid canonical bootnode %q", value)
		}
		if index > 0 && strings.Compare(manifest.Bootnodes.ENodes[index-1], value) >= 0 {
			return errors.New("release manifest bootnodes are not unique and canonically sorted")
		}
	}
	if manifest.FeePolicy.DividendAddress == (common.Address{}) ||
		manifest.FeePolicy.DividendCodeHash == (common.Hash{}) ||
		manifest.FeePolicy.ActivationBlock == 0 {
		return errors.New("release manifest has incomplete Dividend fee policy")
	}
	return nil
}

func validateSignature(signature *Signature) error {
	if signature == nil {
		return errors.New("release signature is nil")
	}
	if signature.SchemaVersion != SignatureSchemaVersion {
		return fmt.Errorf("unsupported release signature schema %q", signature.SchemaVersion)
	}
	if signature.Algorithm != SignatureAlgorithm {
		return fmt.Errorf("unsupported release signature algorithm %q", signature.Algorithm)
	}
	if !identifierPattern.MatchString(signature.KeyID) {
		return fmt.Errorf("invalid release signing key ID %q", signature.KeyID)
	}
	if !isSHA256(signature.ManifestSHA256) {
		return errors.New("release signature has invalid manifest digest")
	}
	raw, err := base64.StdEncoding.DecodeString(signature.SignatureBase64)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return errors.New("release signature has invalid base64 payload")
	}
	return nil
}

func validateTrustedKeys(trusted *TrustedKeys) error {
	if trusted == nil {
		return errors.New("release trusted keys are nil")
	}
	if trusted.SchemaVersion != TrustedKeysSchemaVersion {
		return fmt.Errorf("unsupported release trusted-keys schema %q", trusted.SchemaVersion)
	}
	if len(trusted.Keys) == 0 {
		return errors.New("release trusted-keys set is empty")
	}
	seen := make(map[string]struct{}, len(trusted.Keys))
	for _, key := range trusted.Keys {
		if !identifierPattern.MatchString(key.KeyID) {
			return fmt.Errorf("invalid trusted release key ID %q", key.KeyID)
		}
		if _, exists := seen[key.KeyID]; exists {
			return fmt.Errorf("duplicate trusted release key ID %q", key.KeyID)
		}
		seen[key.KeyID] = struct{}{}
		if key.Algorithm != SignatureAlgorithm {
			return fmt.Errorf("unsupported trusted release key algorithm %q", key.Algorithm)
		}
		publicKey, err := base64.StdEncoding.DecodeString(key.PublicKeyBase64)
		if err != nil || len(publicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("trusted release key %q has invalid public key", key.KeyID)
		}
	}
	return nil
}

func writeJSON(path string, value interface{}) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func readStrictJSON(path string, output interface{}) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return decodeStrictJSON(content, output)
}

func decodeStrictJSON(content []byte, output interface{}) error {
	if err := rejectDuplicateJSONKeys(content); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return fmt.Errorf("invalid trailing JSON data: %w", err)
		}
		return errors.New("unexpected trailing JSON value")
	}
	return nil
}

func rejectDuplicateJSONKeys(input []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()

	var walkValue func() error
	walkValue = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, isDelimiter := token.(json.Delim)
		if !isDelimiter {
			return nil
		}
		switch delimiter {
		case '{':
			keys := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("JSON object key is not a string")
				}
				if _, exists := keys[key]; exists {
					return fmt.Errorf("duplicate JSON key %q", key)
				}
				keys[key] = struct{}{}
				if err := walkValue(); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil {
				return err
			}
			if end != json.Delim('}') {
				return errors.New("invalid JSON object terminator")
			}
		case '[':
			for decoder.More() {
				if err := walkValue(); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil {
				return err
			}
			if end != json.Delim(']') {
				return errors.New("invalid JSON array terminator")
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
		}
		return nil
	}
	if err := walkValue(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("unexpected trailing JSON token")
	}
	return nil
}
