package usdb

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	goActivationGoldenSchemaVersion = "uip-0008-go-btc-activation-golden:v1"
	btcActivationRegistrySchemaV1   = "uip-0008-btc-activation-registry:v1"

	// BTCMainnetActivationRegistryIDV1 is generated from btc-mainnet.json.
	BTCMainnetActivationRegistryIDV1 = "bb751626eb1415bbc349e77f58cb412908584842cbf7d786262b7bd1f6a7d39e"
	// BTCRegtestActivationRegistryIDV1 is generated from btc-regtest.json.
	BTCRegtestActivationRegistryIDV1 = "22d820e6ec242b61f63473f279c41a4103af5cff13206b1925fd415cceaaf83d"
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
	ActivationRegistryID string               `json:"activation_registry_id"`
	Activations          []btcActivationPoint `json:"activations"`
}

type btcActivationPoint struct {
	BTCHeight          uint32           `json:"btc_height"`
	ActiveVersionSet   ActiveVersionSet `json:"active_version_set"`
	ActiveVersionSetID string           `json:"active_version_set_id"`
}

func loadBTCActivationRegistry(registryID string) (*btcActivationRegistry, error) {
	btcActivationGoldenOnce.Do(func() {
		btcActivationGoldenRegistries, btcActivationGoldenErr = parseBTCActivationGolden(btcActivationGoldenJSON)
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
	if artifact.SchemaVersion != goActivationGoldenSchemaVersion {
		return nil, fmt.Errorf("unsupported Go BTC activation golden schema %q", artifact.SchemaVersion)
	}
	if artifact.SourceRegistrySchemaVersion != btcActivationRegistrySchemaV1 {
		return nil, fmt.Errorf("unsupported source BTC activation registry schema %q", artifact.SourceRegistrySchemaVersion)
	}
	if len(artifact.Registries) == 0 {
		return nil, fmt.Errorf("Go BTC activation golden artifact has no registries")
	}

	registries := make(map[string]*btcActivationRegistry, len(artifact.Registries))
	networks := make(map[string]struct{}, len(artifact.Registries))
	for index := range artifact.Registries {
		registry := &artifact.Registries[index]
		if registry.NetworkID == "" {
			return nil, fmt.Errorf("Go BTC activation registry has an empty network_id")
		}
		if _, exists := networks[registry.NetworkID]; exists {
			return nil, fmt.Errorf("duplicate Go BTC activation network %q", registry.NetworkID)
		}
		networks[registry.NetworkID] = struct{}{}
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
			computedID, err := activation.ActiveVersionSet.ID()
			if err != nil {
				return nil, fmt.Errorf("invalid golden active_version_set for %s at %d: %w", registry.NetworkID, activation.BTCHeight, err)
			}
			if computedID != activation.ActiveVersionSetID {
				return nil, fmt.Errorf("golden active_version_set_id mismatch for %s at %d: have %s recomputed %s", registry.NetworkID, activation.BTCHeight, activation.ActiveVersionSetID, computedID)
			}
			if err := activation.ActiveVersionSet.ValidateBTCProfileSurface(); err != nil {
				return nil, fmt.Errorf("unsupported golden active_version_set for %s at %d: %w", registry.NetworkID, activation.BTCHeight, err)
			}
		}
		registries[registry.ActivationRegistryID] = registry
	}
	return registries, nil
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
