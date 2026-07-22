package usdb

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

const (
	activeVersionSetHashDomain = "usdb-active-version-set:v1"

	InscriptionSchemaVersionV1       = "uip-0001-miner-pass-inscription:v1"
	PassStateMachineVersionV1        = "uip-0002-pass-state-machine:v1"
	EnergyFormulaVersionV1           = "uip-0003-pass-energy-formula:v1"
	EffectiveEnergyFormulaVersionV1  = "uip-0004-collab-leader-effective-energy:v1"
	LevelFormulaVersionV1            = "uip-0005-level-and-real-difficulty:v1"
	QuerySemanticsVersionV1          = "uip-0006-economic-query-semantics:v1"
	CommitProtocolVersionV1          = "uip-0008-usdb-local-state-commit:v1"
	BalanceHistorySemanticsVersionV1 = "balance-snapshot-at-or-before:v1"
)

var activeVersionFamilies = []string{
	"inscription_schema_version",
	"pass_state_machine_version",
	"energy_formula_version",
	"effective_energy_formula_version",
	"level_formula_version",
	"query_semantics_version",
	"state_view_version",
	"payload_version",
	"difficulty_policy_version",
	"reward_rule_version",
	"coinbase_emission_policy_version",
	"fee_split_policy_version",
	"collaboration_efficiency_policy_version",
	"price_policy_version",
	"quote_policy_version",
	"aux_pool_policy_version",
	"commit_protocol_version",
	"balance_history_semantics_version",
}

var activeVersionFamilySet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(activeVersionFamilies))
	for _, family := range activeVersionFamilies {
		set[family] = struct{}{}
	}
	return set
}()

// ActiveVersionSet is the UIP-0008 version map selected for one exact chain context.
// Its custom decoder rejects unknown and duplicate version families.
type ActiveVersionSet map[string]json.RawMessage

func (set *ActiveVersionSet) UnmarshalJSON(input []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	start, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("invalid active_version_set: %w", err)
	}
	if delimiter, ok := start.(json.Delim); !ok || delimiter != '{' {
		return fmt.Errorf("active_version_set must be an object")
	}

	decoded := make(ActiveVersionSet)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("invalid active_version_set family: %w", err)
		}
		family, ok := token.(string)
		if !ok {
			return fmt.Errorf("active_version_set family must be a string")
		}
		if _, ok := activeVersionFamilySet[family]; !ok {
			return fmt.Errorf("unknown active_version_set family %q", family)
		}
		if _, exists := decoded[family]; exists {
			return fmt.Errorf("duplicate active_version_set family %q", family)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("invalid active_version_set value for %q: %w", family, err)
		}
		if _, _, err := decodeActiveVersionValue(value); err != nil {
			return fmt.Errorf("invalid active_version_set value for %q: %w", family, err)
		}
		decoded[family] = append(json.RawMessage(nil), value...)
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("invalid active_version_set terminator: %w", err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return fmt.Errorf("invalid trailing active_version_set data: %w", err)
		}
		return fmt.Errorf("unexpected trailing active_version_set token %v", token)
	}
	*set = decoded
	return nil
}

// ID returns the canonical SHA-256 identity defined by the Rust activation registry.
func (set ActiveVersionSet) ID() (string, error) {
	hasher := sha256.New()
	writeLengthPrefixedString(hasher, activeVersionSetHashDomain)
	for _, family := range activeVersionFamilies {
		writeLengthPrefixedString(hasher, family)
		raw, present := set[family]
		if !present {
			hasher.Write([]byte{0})
			continue
		}
		hasher.Write([]byte{1})
		stringValue, integerValue, err := decodeActiveVersionValue(raw)
		if err != nil {
			return "", fmt.Errorf("invalid %s: %w", family, err)
		}
		if stringValue != nil {
			hasher.Write([]byte{0})
			writeLengthPrefixedString(hasher, *stringValue)
		} else {
			hasher.Write([]byte{1})
			var encoded [8]byte
			binary.BigEndian.PutUint64(encoded[:], *integerValue)
			hasher.Write(encoded[:])
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// ValidateBTCProfileSurface rejects version sets whose non-formula contracts cannot
// be decoded by this validator. Formula families are dispatched separately so a
// future generated registry can carry multiple supported formula versions.
func (set ActiveVersionSet) ValidateBTCProfileSurface() error {
	required := map[string]string{
		"inscription_schema_version":        InscriptionSchemaVersionV1,
		"pass_state_machine_version":        PassStateMachineVersionV1,
		"energy_formula_version":            "",
		"effective_energy_formula_version":  "",
		"level_formula_version":             "",
		"query_semantics_version":           QuerySemanticsVersionV1,
		"state_view_version":                EconomicStateViewVersionV1,
		"commit_protocol_version":           CommitProtocolVersionV1,
		"balance_history_semantics_version": BalanceHistorySemanticsVersionV1,
	}
	if len(set) != len(required) {
		return fmt.Errorf("BTC active_version_set has %d families, want %d", len(set), len(required))
	}
	for family, expected := range required {
		value, err := set.requireStringVersion(family)
		if err != nil {
			return err
		}
		if expected != "" && value != expected {
			return fmt.Errorf("unsupported %s, have %q want %q", family, value, expected)
		}
	}
	return nil
}

func (set ActiveVersionSet) requireStringVersion(family string) (string, error) {
	raw, ok := set[family]
	if !ok {
		return "", fmt.Errorf("BTC active_version_set is missing %s", family)
	}
	value, integer, err := decodeActiveVersionValue(raw)
	if err != nil {
		return "", fmt.Errorf("invalid %s: %w", family, err)
	}
	if value == nil || integer != nil || *value == "" {
		return "", fmt.Errorf("%s must be a non-empty string, have %s", family, string(raw))
	}
	return *value, nil
}

func decodeActiveVersionValue(raw json.RawMessage) (*string, *uint64, error) {
	var stringValue string
	if err := json.Unmarshal(raw, &stringValue); err == nil {
		return &stringValue, nil, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, err
	}
	number, ok := value.(json.Number)
	if !ok {
		return nil, nil, fmt.Errorf("version value must be a string or unsigned integer")
	}
	integer, err := strconv.ParseUint(number.String(), 10, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("version integer must be uint64: %w", err)
	}
	return nil, &integer, nil
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeLengthPrefixedString(writer byteWriter, value string) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	writer.Write(length[:])
	writer.Write([]byte(value))
}
