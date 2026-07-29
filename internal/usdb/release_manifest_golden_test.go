package usdb

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

const (
	crossChainReleaseManifestSchemaV3 = "uip-0008-cross-chain-release-manifest:v3"
	usdbDevelopmentNetworkID          = "usdb-devnet-20260323"
	usdbActivationAuthority           = "chain_config.usdb.activations"
)

//go:embed cross_chain_release_manifest.json
var crossChainReleaseManifestJSON []byte

type crossChainReleaseManifest struct {
	SchemaVersion           string                          `json:"schema_version"`
	ReleaseID               string                          `json:"release_id"`
	BTCActivationRegistries []btcRegistryReleaseBinding     `json:"btc_activation_registries"`
	USDBChainConfigs        []usdbChainConfigReleaseBinding `json:"usdb_chain_configs"`
	Notes                   string                          `json:"notes"`
}

type btcRegistryReleaseBinding struct {
	NetworkType          string `json:"network_type"`
	NetworkID            string `json:"network_id"`
	Artifact             string `json:"artifact"`
	Revision             uint32 `json:"revision"`
	Current              bool   `json:"current"`
	ActivationRegistryID string `json:"activation_registry_id"`
}

type usdbChainConfigReleaseBinding struct {
	NetworkType         string                              `json:"network_type"`
	NetworkID           string                              `json:"network_id"`
	ChainID             uint64                              `json:"chain_id"`
	GenesisHash         string                              `json:"genesis_hash"`
	Activations         []usdbChainActivationReleaseBinding `json:"activations"`
	Source              string                              `json:"source"`
	ActivationAuthority string                              `json:"activation_authority"`
}

type usdbChainActivationReleaseBinding struct {
	Block                   uint64                       `json:"block"`
	BTCActivationRegistryID string                       `json:"btc_activation_registry_id"`
	BTCAnchorMaxAgeBlocks   uint32                       `json:"btc_anchor_max_age_blocks"`
	Versions                usdbConsensusVersionsBinding `json:"versions"`
}

type usdbConsensusVersionsBinding struct {
	PayloadVersion                       uint8  `json:"payload_version"`
	BTCAnchorPolicyVersion               uint16 `json:"btc_anchor_policy_version"`
	DifficultyPolicyVersion              uint16 `json:"difficulty_policy_version"`
	RewardRuleVersion                    uint16 `json:"reward_rule_version"`
	CoinbaseEmissionPolicyVersion        uint16 `json:"coinbase_emission_policy_version"`
	FeeSplitPolicyVersion                uint16 `json:"fee_split_policy_version"`
	CollaborationEfficiencyPolicyVersion uint16 `json:"collaboration_efficiency_policy_version"`
	PricePolicyVersion                   uint32 `json:"price_policy_version"`
	QuotePolicyVersion                   uint16 `json:"quote_policy_version"`
	AuxPoolPolicyVersion                 uint16 `json:"aux_pool_policy_version"`
}

func parseCrossChainReleaseManifest(input []byte) (*crossChainReleaseManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var manifest crossChainReleaseManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("invalid cross-chain release manifest golden: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	if manifest.SchemaVersion != crossChainReleaseManifestSchemaV3 {
		return nil, fmt.Errorf("unsupported cross-chain release manifest schema %q", manifest.SchemaVersion)
	}
	return &manifest, nil
}

func releaseVersionsFromConfig(versions params.USDBConsensusVersions) usdbConsensusVersionsBinding {
	return usdbConsensusVersionsBinding{
		PayloadVersion:                       versions.PayloadVersion,
		BTCAnchorPolicyVersion:               versions.BTCAnchorPolicyVersion,
		DifficultyPolicyVersion:              versions.DifficultyPolicyVersion,
		RewardRuleVersion:                    versions.RewardRuleVersion,
		CoinbaseEmissionPolicyVersion:        versions.CoinbaseEmissionPolicyVersion,
		FeeSplitPolicyVersion:                versions.FeeSplitPolicyVersion,
		CollaborationEfficiencyPolicyVersion: versions.CollaborationEfficiencyPolicyVersion,
		PricePolicyVersion:                   versions.PricePolicyVersion,
		QuotePolicyVersion:                   versions.QuotePolicyVersion,
		AuxPoolPolicyVersion:                 versions.AuxPoolPolicyVersion,
	}
}

func validateDevelopmentReleaseBinding(
	manifest *crossChainReleaseManifest,
	config *params.ChainConfig,
	genesisHash common.Hash,
) error {
	if manifest == nil || config == nil || config.ChainID == nil || !config.ChainID.IsUint64() || config.USDB == nil {
		return fmt.Errorf("development release inputs are incomplete")
	}
	var binding *usdbChainConfigReleaseBinding
	for index := range manifest.USDBChainConfigs {
		if manifest.USDBChainConfigs[index].NetworkID == usdbDevelopmentNetworkID {
			binding = &manifest.USDBChainConfigs[index]
			break
		}
	}
	if binding == nil {
		return fmt.Errorf("release manifest is missing %s", usdbDevelopmentNetworkID)
	}
	if binding.NetworkType != "devnet" ||
		binding.ChainID != config.ChainID.Uint64() ||
		binding.GenesisHash != strings.TrimPrefix(genesisHash.Hex(), "0x") ||
		binding.Source != "go-ethereum/params/config.go:USDBChainConfig" ||
		binding.ActivationAuthority != usdbActivationAuthority {
		return fmt.Errorf("development chain identity does not match Go config")
	}
	if len(binding.Activations) != len(config.USDB.Activations) {
		return fmt.Errorf("development activation count mismatch")
	}
	for index, activation := range config.USDB.Activations {
		expected := usdbChainActivationReleaseBinding{
			Block:                   activation.Block,
			BTCActivationRegistryID: activation.BTCActivationRegistryID,
			BTCAnchorMaxAgeBlocks:   activation.BTCAnchorMaxAgeBlocks,
			Versions:                releaseVersionsFromConfig(activation.Versions),
		}
		if !reflect.DeepEqual(binding.Activations[index], expected) {
			return fmt.Errorf("development activation %d does not match Go config", index)
		}
		if _, err := loadBTCActivationRegistry(activation.BTCActivationRegistryID); err != nil {
			return fmt.Errorf("development activation %d references unsupported BTC registry: %w", index, err)
		}
	}
	return nil
}

func TestCrossChainReleaseManifestMatchesGoDevelopmentConfig(t *testing.T) {
	manifest, err := parseCrossChainReleaseManifest(crossChainReleaseManifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range manifest.BTCActivationRegistries {
		registry, err := loadBTCActivationRegistry(binding.ActivationRegistryID)
		if err != nil {
			t.Fatalf("release binding references unsupported BTC registry: %v", err)
		}
		if registry.NetworkID != binding.NetworkID ||
			registry.Revision != binding.Revision ||
			registry.Current != binding.Current {
			t.Fatalf("BTC release binding does not match generated registry: binding=%+v registry=%+v", binding, registry)
		}
	}
	if err := validateDevelopmentReleaseBinding(manifest, params.USDBChainConfig, params.USDBGenesisHash); err != nil {
		t.Fatal(err)
	}
}

func TestCrossChainReleaseManifestDetectsGoConfigDrift(t *testing.T) {
	manifest, err := parseCrossChainReleaseManifest(crossChainReleaseManifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	binding := &manifest.USDBChainConfigs[0]

	binding.GenesisHash = strings.Repeat("ff", 32)
	if err := validateDevelopmentReleaseBinding(manifest, params.USDBChainConfig, params.USDBGenesisHash); err == nil {
		t.Fatal("expected genesis hash drift to be rejected")
	}

	manifest, err = parseCrossChainReleaseManifest(crossChainReleaseManifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	manifest.USDBChainConfigs[0].Activations[0].BTCAnchorMaxAgeBlocks++
	if err := validateDevelopmentReleaseBinding(manifest, params.USDBChainConfig, params.USDBGenesisHash); err == nil {
		t.Fatal("expected activation drift to be rejected")
	}

	manifest, err = parseCrossChainReleaseManifest(crossChainReleaseManifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	manifest.USDBChainConfigs[0].Activations[0].Versions.RewardRuleVersion++
	if err := validateDevelopmentReleaseBinding(manifest, params.USDBChainConfig, params.USDBGenesisHash); err == nil {
		t.Fatal("expected policy-version drift to be rejected")
	}
}
