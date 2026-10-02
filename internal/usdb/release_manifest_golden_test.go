package usdb

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

const (
	crossChainReleaseManifestSchemaV3 = "uip-0008-cross-chain-release-manifest:v3"
	crossChainReleaseManifestSchemaV4 = "uip-0008-cross-chain-release-manifest:v4"
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
	NetworkType          string          `json:"network_type"`
	NetworkID            string          `json:"network_id"`
	RulesScope           json.RawMessage `json:"rules_scope,omitempty"`
	Artifact             string          `json:"artifact"`
	Revision             uint32          `json:"revision"`
	Current              bool            `json:"current"`
	ActivationRegistryID string          `json:"activation_registry_id"`
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
	if manifest.SchemaVersion != crossChainReleaseManifestSchemaV3 && manifest.SchemaVersion != crossChainReleaseManifestSchemaV4 {
		return nil, fmt.Errorf("unsupported cross-chain release manifest schema %q", manifest.SchemaVersion)
	}
	if err := validateReleaseManifestScopes(&manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (binding btcRegistryReleaseBinding) rulesScope() (string, error) {
	if binding.RulesScope == nil {
		return "", nil
	}
	var scope string
	if err := json.Unmarshal(binding.RulesScope, &scope); err != nil || !validRulesScope(scope) {
		return "", fmt.Errorf("invalid release rules_scope %s", binding.RulesScope)
	}
	return scope, nil
}

// This validates audit metadata only. ChainConfig still owns consensus activation.
func validateReleaseManifestScopes(manifest *crossChainReleaseManifest) error {
	if manifest.ReleaseID == "" || manifest.Notes == "" || len(manifest.BTCActivationRegistries) == 0 || len(manifest.USDBChainConfigs) == 0 {
		return fmt.Errorf("cross-chain release manifest is incomplete")
	}
	type scopeKey struct{ network, rules string }
	groups := make(map[scopeKey]map[uint32]bool)
	current := make(map[scopeKey]int)
	registryScopes := make(map[string]scopeKey)
	for _, binding := range manifest.BTCActivationRegistries {
		scope, err := binding.rulesScope()
		if err != nil {
			return err
		}
		if manifest.SchemaVersion == crossChainReleaseManifestSchemaV3 && binding.RulesScope != nil {
			return fmt.Errorf("legacy release manifest must not declare rules_scope")
		}
		if binding.NetworkID == "" || binding.Artifact == "" || binding.Revision == 0 {
			return fmt.Errorf("invalid BTC release binding metadata")
		}
		if _, err := parseCanonicalHex32("activation_registry_id", binding.ActivationRegistryID); err != nil {
			return err
		}
		if _, duplicate := registryScopes[binding.ActivationRegistryID]; duplicate {
			return fmt.Errorf("duplicate release registry id %s", binding.ActivationRegistryID)
		}
		key := scopeKey{binding.NetworkID, scope}
		if groups[key] == nil {
			groups[key] = make(map[uint32]bool)
		}
		if groups[key][binding.Revision] {
			return fmt.Errorf("duplicate release registry revision for %+v", key)
		}
		groups[key][binding.Revision] = true
		if binding.Current {
			current[key]++
		}
		registryScopes[binding.ActivationRegistryID] = key
	}
	for key, revisions := range groups {
		if current[key] != 1 {
			return fmt.Errorf("release scope %+v requires one current revision", key)
		}
		for revision := uint32(1); revision <= uint32(len(revisions)); revision++ {
			if !revisions[revision] {
				return fmt.Errorf("non-contiguous release registry revisions for %+v", key)
			}
		}
	}
	seenChains := make(map[string]bool)
	for _, chain := range manifest.USDBChainConfigs {
		if chain.NetworkID == "" || chain.ChainID == 0 || chain.Source == "" || chain.ActivationAuthority != usdbActivationAuthority || len(chain.Activations) == 0 || seenChains[chain.NetworkID] {
			return fmt.Errorf("invalid USDB release binding %s", chain.NetworkID)
		}
		if _, err := parseCanonicalHex32("genesis_hash", chain.GenesisHash); err != nil {
			return err
		}
		seenChains[chain.NetworkID] = true
		var firstScope scopeKey
		for index, activation := range chain.Activations {
			scope, exists := registryScopes[activation.BTCActivationRegistryID]
			if !exists || activation.BTCAnchorMaxAgeBlocks == 0 || activation.Versions.PayloadVersion == 0 || activation.Versions.BTCAnchorPolicyVersion == 0 || activation.Versions.DifficultyPolicyVersion == 0 || (index > 0 && activation.Block <= chain.Activations[index-1].Block) {
				return fmt.Errorf("invalid release activation for %s at %d", chain.NetworkID, activation.Block)
			}
			if index == 0 {
				firstScope = scope
			} else if manifest.SchemaVersion == crossChainReleaseManifestSchemaV4 && scope != firstScope {
				return fmt.Errorf("USDB release chain %s changes BTC source or rules_scope", chain.NetworkID)
			}
		}
	}
	return nil
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
		scope, err := binding.rulesScope()
		if err != nil {
			t.Fatal(err)
		}
		if registry.NetworkID != binding.NetworkID || registry.RulesScope != scope ||
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

func validateReleaseRegistryCatalog(manifest *crossChainReleaseManifest, registries map[string]*btcActivationRegistry) error {
	for _, binding := range manifest.BTCActivationRegistries {
		registry := registries[binding.ActivationRegistryID]
		scope, err := binding.rulesScope()
		if err != nil {
			return err
		}
		if registry == nil || registry.NetworkID != binding.NetworkID || registry.RulesScope != scope || registry.Revision != binding.Revision || registry.Current != binding.Current {
			return fmt.Errorf("release binding differs from generated registry: %+v", binding)
		}
	}
	return nil
}

func TestScopedReleaseManifestMatchesIndependentRegistryHistories(t *testing.T) {
	blob, err := os.ReadFile("testdata/cross_chain_release_manifest_scoped.json")
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/btc_activation_scoped_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	registries, err := parseBTCActivationGolden(golden)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := parseCrossChainReleaseManifest(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateReleaseRegistryCatalog(manifest, registries); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct {
		name  string
		apply func(*crossChainReleaseManifest)
	}{
		{name: "scope relabel", apply: func(m *crossChainReleaseManifest) {
			m.BTCActivationRegistries[0].RulesScope = json.RawMessage(`"different-scope"`)
		}},
		{name: "null scope", apply: func(m *crossChainReleaseManifest) {
			m.BTCActivationRegistries[0].RulesScope = json.RawMessage(`null`)
		}},
		{name: "scope omitted", apply: func(m *crossChainReleaseManifest) {
			m.BTCActivationRegistries[0].RulesScope = nil
		}},
		{name: "legacy downgrade", apply: func(m *crossChainReleaseManifest) {
			m.SchemaVersion = crossChainReleaseManifestSchemaV3
		}},
		{name: "revision gap", apply: func(m *crossChainReleaseManifest) {
			m.BTCActivationRegistries[0].Revision = 9
		}},
		{name: "unknown registry", apply: func(m *crossChainReleaseManifest) {
			m.USDBChainConfigs[0].Activations[0].BTCActivationRegistryID = strings.Repeat("f", 64)
		}},
		{name: "chain changes scope", apply: func(m *crossChainReleaseManifest) {
			chain := &m.USDBChainConfigs[0]
			next := chain.Activations[len(chain.Activations)-1]
			next.Block++
			next.BTCActivationRegistryID = m.USDBChainConfigs[1].Activations[0].BTCActivationRegistryID
			chain.Activations = append(chain.Activations, next)
		}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			manifest, err := parseCrossChainReleaseManifest(blob)
			if err != nil {
				t.Fatal(err)
			}
			mutation.apply(manifest)
			encoded, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := parseCrossChainReleaseManifest(encoded)
			if err == nil {
				err = validateReleaseRegistryCatalog(decoded, registries)
			}
			if err == nil {
				t.Fatal("inconsistent scoped release manifest accepted")
			}
		})
	}
}
