package usdb

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"sync"
)

const (
	// BTCTestnetV1ActivationRegistryID binds fresh USDB testnet-v1 to BTC mainnet V2 rules.
	BTCTestnetV1ActivationRegistryID = "53b4bfed53b55a4accbd947d04d113a684f1af896d39d2d9bd984e07ad543f32"

	// BTCRegtestMinerPassV2RegistryID selects an isolated development scope explicitly.
	// It is never substituted for a deployment chain's configured registry ID.
	BTCRegtestMinerPassV2RegistryID = "d53e9907cfc5abf5d8294e98bbaa838630ee070118eb0845ad3e770959279f08"
	// BTCRegtestMinerPassV2StagedRegistryID adds only a planned revision marker.
	BTCRegtestMinerPassV2StagedRegistryID = "7cbfc8e2f8684c355a3ba44e8156ae19b65b8db220e98edf5ec909aebe51f2f7"

	goActivationGoldenSchemaVersion       = "uip-0008-go-btc-activation-golden:v3"
	btcActivationRegistrySchemaV2         = "uip-0008-btc-activation-registry:v2"
	goScopedActivationGoldenSchemaVersion = "uip-0008-go-btc-activation-golden:v4"
	btcActivationRegistrySchemaV3         = "uip-0008-btc-activation-registry:v3"

	// BTCMainnetActivationRegistryIDV1 is generated from btc-mainnet.json.
	BTCMainnetActivationRegistryIDV1 = "a6350cd6a68755ea64edf537f35c1eca4421a970e2ecfd67aaa29075aae57224"
	// BTCRegtestActivationRegistryIDV1 is generated from btc-regtest.json.
	BTCRegtestActivationRegistryIDV1 = "bfd8c7e41ab4035db64e52eb9ea55050c08211c2ae4c2a88d8b2fc17ae1718b0"
	// BTCRegtestActivationRegistryIDRevision2 is the staged append-only regtest
	// revision used to exercise registry rollout without activating a new formula.
	BTCRegtestActivationRegistryIDRevision2 = "adcca18bb4eccd4715bb0d6ec69c7b3d5e09065fac0cb33b145db7b621f59fba"
)

var (
	// ErrBTCActivationRegistryNotSupported means the chain config selected a
	// registry that is not present in this binary's generated golden artifact.
	ErrBTCActivationRegistryNotSupported = errors.New("BTC activation registry not supported")
	// ErrBTCActivationRegistryMismatch means the companion response did not use
	// the registry identity committed by the USDB chain config.
	ErrBTCActivationRegistryMismatch = errors.New("BTC activation registry mismatch")
	// ErrBTCActiveVersionSetMismatch means the companion response does not match
	// the locally generated set for the payload BTC height.
	ErrBTCActiveVersionSetMismatch = errors.New("BTC active version set mismatch")

	//go:embed btc_activation_golden.json
	btcActivationGoldenJSON []byte

	// Explicit deployment catalog; selected only by a chain's committed registry ID.
	//go:embed btc_testnet_v1_activation_golden.json
	btcTestnetV1GoldenJSON []byte

	// Explicit opt-in development catalog; does not change any chain configuration.
	//go:embed testdata/miner_pass_v2_activation_golden.json
	btcMinerPassDevelopmentGoldenJSON []byte

	btcActivationGoldenOnce       sync.Once
	btcActivationGoldenRegistries map[string]*btcActivationRegistry
	btcActivationGoldenErr        error
)

type btcActivationGoldenArtifact struct {
	SchemaVersion               string                  `json:"schema_version"`
	SourceRegistrySchemaVersion string                  `json:"source_registry_schema_version"`
	Registries                  []btcActivationRegistry `json:"registries"`
}

type btcActivationRegistry struct {
	NetworkID            string               `json:"network_id"`
	RulesScope           string               `json:"rules_scope,omitempty"`
	Revision             uint32               `json:"revision"`
	Current              bool                 `json:"current"`
	StableLagBlocks      uint32               `json:"stable_lag_blocks"`
	ActivationRegistryID string               `json:"activation_registry_id"`
	Activations          []btcActivationPoint `json:"activations"`
}

type btcActivationPoint struct {
	BTCHeight          uint32           `json:"btc_height"`
	ActiveVersionSet   ActiveVersionSet `json:"active_version_set"`
	ActiveVersionSetID string           `json:"active_version_set_id"`
}

// BTCActivationRegistryDescriptor exposes the immutable identity metadata for
// one registry revision without exposing or mutating its activation records.
type BTCActivationRegistryDescriptor struct {
	// RulesScope is empty for frozen legacy registries and otherwise identifies
	// an independent USDB interpretation history on NetworkID.
	RulesScope           string
	NetworkID            string
	Revision             uint32
	Current              bool
	StableLagBlocks      uint32
	ActivationRegistryID string
}

// DescribeBTCActivationRegistry returns the generated catalog metadata for an
// immutable registry ID. Unknown IDs fail closed.
func DescribeBTCActivationRegistry(registryID string) (BTCActivationRegistryDescriptor, error) {
	registry, err := loadBTCActivationRegistry(registryID)
	if err != nil {
		return BTCActivationRegistryDescriptor{}, err
	}
	return BTCActivationRegistryDescriptor{
		NetworkID:            registry.NetworkID,
		RulesScope:           registry.RulesScope,
		Revision:             registry.Revision,
		Current:              registry.Current,
		StableLagBlocks:      registry.StableLagBlocks,
		ActivationRegistryID: registry.ActivationRegistryID,
	}, nil
}

func loadBTCActivationRegistry(registryID string) (*btcActivationRegistry, error) {
	btcActivationGoldenOnce.Do(func() {
		btcActivationGoldenRegistries, btcActivationGoldenErr = parseBTCActivationGolden(btcActivationGoldenJSON)
		if btcActivationGoldenErr != nil {
			return
		}
		for _, catalog := range [][]byte{btcMinerPassDevelopmentGoldenJSON, btcTestnetV1GoldenJSON} {
			registries, err := parseBTCActivationGolden(catalog)
			if err != nil {
				btcActivationGoldenErr = err
				return
			}
			for id, registry := range registries {
				if _, exists := btcActivationGoldenRegistries[id]; exists {
					btcActivationGoldenErr = fmt.Errorf("duplicate scoped registry ID %s", id)
					return
				}
				btcActivationGoldenRegistries[id] = registry
			}
		}
	})
	if btcActivationGoldenErr != nil {
		return nil, btcActivationGoldenErr
	}
	registry, ok := btcActivationGoldenRegistries[registryID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrBTCActivationRegistryNotSupported, registryID)
	}
	return registry, nil
}

func parseBTCActivationGolden(input []byte) (map[string]*btcActivationRegistry, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var artifact btcActivationGoldenArtifact
	if err := decoder.Decode(&artifact); err != nil {
		return nil, fmt.Errorf("invalid Go BTC activation golden artifact: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	scoped := artifact.SchemaVersion == goScopedActivationGoldenSchemaVersion
	switch artifact.SchemaVersion {
	case goActivationGoldenSchemaVersion:
		if artifact.SourceRegistrySchemaVersion != btcActivationRegistrySchemaV2 {
			return nil, fmt.Errorf("unsupported source BTC activation registry schema %q", artifact.SourceRegistrySchemaVersion)
		}
	case goScopedActivationGoldenSchemaVersion:
		if artifact.SourceRegistrySchemaVersion != btcActivationRegistrySchemaV3 {
			return nil, fmt.Errorf("unsupported scoped source BTC activation registry schema %q", artifact.SourceRegistrySchemaVersion)
		}
	default:
		return nil, fmt.Errorf("unsupported Go BTC activation golden schema %q", artifact.SchemaVersion)
	}
	if len(artifact.Registries) == 0 {
		return nil, fmt.Errorf("Go BTC activation golden artifact has no registries")
	}

	registries := make(map[string]*btcActivationRegistry, len(artifact.Registries))
	type catalogScope struct{ networkID, rulesScope string }
	networkRevisions := make(map[catalogScope][]*btcActivationRegistry)
	for index := range artifact.Registries {
		registry := &artifact.Registries[index]
		if registry.NetworkID == "" {
			return nil, fmt.Errorf("Go BTC activation registry has an empty network_id")
		}
		if scoped && !validRulesScope(registry.RulesScope) {
			return nil, fmt.Errorf("invalid Go BTC activation rules scope %q", registry.RulesScope)
		}
		if !scoped && registry.RulesScope != "" {
			return nil, fmt.Errorf("legacy Go BTC activation registry must not declare rules_scope")
		}
		if registry.Revision == 0 {
			return nil, fmt.Errorf("Go BTC activation registry %s has revision 0", registry.NetworkID)
		}
		if registry.StableLagBlocks == 0 {
			return nil, fmt.Errorf("Go BTC activation registry %s has zero stable_lag_blocks", registry.NetworkID)
		}
		if _, err := parseCanonicalHex32("activation_registry_id", registry.ActivationRegistryID); err != nil {
			return nil, err
		}
		if _, exists := registries[registry.ActivationRegistryID]; exists {
			return nil, fmt.Errorf("duplicate Go BTC activation registry id %q", registry.ActivationRegistryID)
		}
		if len(registry.Activations) == 0 {
			return nil, fmt.Errorf("Go BTC activation registry %s has no activation points", registry.NetworkID)
		}
		for activationIndex := range registry.Activations {
			activation := &registry.Activations[activationIndex]
			if activationIndex > 0 && activation.BTCHeight <= registry.Activations[activationIndex-1].BTCHeight {
				return nil, fmt.Errorf("Go BTC activation registry %s has unordered height %d", registry.NetworkID, activation.BTCHeight)
			}
			if _, err := parseCanonicalHex32("active_version_set_id", activation.ActiveVersionSetID); err != nil {
				return nil, err
			}
			versionScope, err := activation.ActiveVersionSet.rulesScope()
			if err != nil {
				return nil, err
			}
			if scoped {
				if versionScope == nil || versionScope.NetworkID != registry.NetworkID || versionScope.RulesScope != registry.RulesScope {
					return nil, fmt.Errorf("golden active_version_set scope mismatch for %s/%s at %d", registry.NetworkID, registry.RulesScope, activation.BTCHeight)
				}
			} else if versionScope != nil {
				return nil, fmt.Errorf("legacy golden active_version_set must not declare scope")
			}
			computedID, err := activation.ActiveVersionSet.ID()
			if err != nil {
				return nil, fmt.Errorf("invalid golden active_version_set for %s at %d: %w", registry.NetworkID, activation.BTCHeight, err)
			}
			if computedID != activation.ActiveVersionSetID {
				return nil, fmt.Errorf("golden active_version_set_id mismatch for %s at %d: have %s recomputed %s", registry.NetworkID, activation.BTCHeight, activation.ActiveVersionSetID, computedID)
			}
			// Decoding metadata must not imply execution support. Frozen legacy and
			// future checkpoints remain identifiable; validateIdentity rejects unsupported rules.

		}
		registries[registry.ActivationRegistryID] = registry
		scope := catalogScope{registry.NetworkID, registry.RulesScope}
		networkRevisions[scope] = append(networkRevisions[scope], registry)
	}
	for scope, revisions := range networkRevisions {
		if err := validateBTCActivationRevisionHistory(scope.networkID+"/"+scope.rulesScope, revisions); err != nil {
			return nil, err
		}
	}
	return registries, nil
}

func validateBTCActivationRevisionHistory(networkID string, revisions []*btcActivationRegistry) error {
	sort.Slice(revisions, func(i, j int) bool { return revisions[i].Revision < revisions[j].Revision })
	currentCount := 0
	for index, revision := range revisions {
		if revision.Current {
			currentCount++
		}
		if index > 0 {
			previous := revisions[index-1]
			if revision.Revision != previous.Revision+1 {
				return fmt.Errorf("Go BTC activation registry %s has non-contiguous revisions %d and %d", networkID, previous.Revision, revision.Revision)
			}
			if revision.StableLagBlocks != previous.StableLagBlocks {
				return fmt.Errorf("Go BTC activation registry %s revision %d changes stable_lag_blocks", networkID, revision.Revision)
			}
			if len(revision.Activations) < len(previous.Activations) {
				return fmt.Errorf("Go BTC activation registry %s revision %d removes activation history", networkID, revision.Revision)
			}
			for activationIndex := range previous.Activations {
				if !reflect.DeepEqual(previous.Activations[activationIndex], revision.Activations[activationIndex]) {
					return fmt.Errorf("Go BTC activation registry %s revision %d rewrites activation index %d", networkID, revision.Revision, activationIndex)
				}
			}
		}
	}
	if currentCount != 1 {
		return fmt.Errorf("Go BTC activation registry %s must mark exactly one revision current", networkID)
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("invalid trailing Go BTC activation golden data: %w", err)
		}
		return fmt.Errorf("unexpected trailing Go BTC activation golden value")
	}
	return nil
}

func (registry *btcActivationRegistry) lookup(btcHeight uint32) (*btcActivationPoint, error) {
	if registry == nil {
		return nil, fmt.Errorf("nil BTC activation registry")
	}
	for index := len(registry.Activations) - 1; index >= 0; index-- {
		activation := &registry.Activations[index]
		if activation.BTCHeight <= btcHeight {
			return activation, nil
		}
	}
	return nil, fmt.Errorf("BTC activation record not found for registry %s at height %d", registry.ActivationRegistryID, btcHeight)
}

func (registry *btcActivationRegistry) validateIdentity(
	btcHeight uint32,
	actualRegistryID string,
	actualSet ActiveVersionSet,
	actualSetID string,
) (*btcActivationPoint, error) {
	if actualRegistryID != registry.ActivationRegistryID {
		return nil, fmt.Errorf("%w: have %q want %q", ErrBTCActivationRegistryMismatch, actualRegistryID, registry.ActivationRegistryID)
	}
	if _, err := parseCanonicalHex32("activation_registry_id", actualRegistryID); err != nil {
		return nil, err
	}
	if _, err := parseCanonicalHex32("active_version_set_id", actualSetID); err != nil {
		return nil, err
	}
	expected, err := registry.lookup(btcHeight)
	if err != nil {
		return nil, err
	}
	computedID, err := actualSet.ID()
	if err != nil {
		return nil, fmt.Errorf("invalid active_version_set: %w", err)
	}
	if computedID != actualSetID {
		return nil, fmt.Errorf("%w: declared %q recomputed %q", ErrBTCActiveVersionSetMismatch, actualSetID, computedID)
	}
	if actualSetID != expected.ActiveVersionSetID {
		return nil, fmt.Errorf("%w at BTC height %d: have %q want %q", ErrBTCActiveVersionSetMismatch, btcHeight, actualSetID, expected.ActiveVersionSetID)
	}
	if err := actualSet.ValidateBTCProfileSurface(); err != nil {
		return nil, err
	}
	return expected, nil
}

// ensureSameHistory compares every activation interval in the indexed prefix, including
// intermediate divergences that later return to the same active set. The origin comes
// from chain config; this check does not authorize adopting a different dataset binding.
func (registry *btcActivationRegistry) ensureSameHistory(other *btcActivationRegistry, origin, through uint32) error {
	if registry == nil || other == nil {
		return fmt.Errorf("%w: missing registry", ErrBTCActivationRegistryMismatch)
	}
	if registry.NetworkID != other.NetworkID || registry.RulesScope != other.RulesScope || registry.StableLagBlocks != other.StableLagBlocks {
		return fmt.Errorf("%w: incompatible history domains: actual_registry=%s actual_network=%s actual_scope=%s actual_lag=%d expected_registry=%s expected_network=%s expected_scope=%s expected_lag=%d", ErrBTCActivationRegistryMismatch, registry.ActivationRegistryID, registry.NetworkID, registry.RulesScope, registry.StableLagBlocks, other.ActivationRegistryID, other.NetworkID, other.RulesScope, other.StableLagBlocks)
	}
	if through < origin {
		origin = through
	}
	// Advance the two interval cursors together; work scales with activation count, not height.
	left, right := 0, 0
	for left+1 < len(registry.Activations) && registry.Activations[left+1].BTCHeight <= origin {
		left++
	}
	for right+1 < len(other.Activations) && other.Activations[right+1].BTCHeight <= origin {
		right++
	}
	height := origin
	for {
		if left >= len(registry.Activations) || right >= len(other.Activations) || registry.Activations[left].BTCHeight > height || other.Activations[right].BTCHeight > height {
			return fmt.Errorf("%w: missing prefix at BTC height %d", ErrBTCActivationRegistryMismatch, height)
		}
		if registry.Activations[left].ActiveVersionSetID != other.Activations[right].ActiveVersionSetID {
			return fmt.Errorf("%w: execution histories differ: actual=%s expected=%s origin=%d through=%d first_difference_height=%d", ErrBTCActivationRegistryMismatch, registry.ActivationRegistryID, other.ActivationRegistryID, origin, through, height)
		}
		next := uint64(through) + 1
		if left+1 < len(registry.Activations) && uint64(registry.Activations[left+1].BTCHeight) < next {
			next = uint64(registry.Activations[left+1].BTCHeight)
		}
		if right+1 < len(other.Activations) && uint64(other.Activations[right+1].BTCHeight) < next {
			next = uint64(other.Activations[right+1].BTCHeight)
		}
		if next > uint64(through) {
			return nil
		}
		height = uint32(next)
		if left+1 < len(registry.Activations) && registry.Activations[left+1].BTCHeight == height {
			left++
		}
		if right+1 < len(other.Activations) && other.Activations[right+1].BTCHeight == height {
			right++
		}
	}
}
