package usdb

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type stubJSONRPCClient struct {
	response json.RawMessage
	err      error
	method   string
	args     []interface{}
	closed   bool
}

func (s *stubJSONRPCClient) CallContext(_ context.Context, result interface{}, method string, args ...interface{}) error {
	s.method = method
	s.args = args
	if s.err != nil {
		return s.err
	}
	raw, ok := result.(*json.RawMessage)
	if !ok {
		return errors.New("unexpected result type")
	}
	*raw = append((*raw)[:0], s.response...)
	return nil
}

func (s *stubJSONRPCClient) Close() {
	s.closed = true
}

func TestPassEconomicProfileViewDecodesCurrentRPCContract(t *testing.T) {
	selector := newTestSelector(t, 123)
	raw := `{
		"view_version":"uip-0006-usdb-economic-state-view:v1",
		"external_state":{
			"btc_height":123,
			"snapshot_id":"` + selector.SnapshotIDHex() + `",
			"stable_block_hash":"` + repeatHex("44", 32) + `",
			"local_state_commit":"` + repeatHex("55", 32) + `",
			"system_state_id":"` + selector.SystemStateIDHex() + `",
			"balance_history_api_version":"1.0.0",
			"balance_history_semantics_version":"balance-snapshot-at-or-before:v1",
			"usdb_index_protocol_version":"1.0.0",
			"usdb_index_formula_version":"pass-energy-formula:v1"
		},
		"pass":{
			"pass_id":"` + selector.PassID.String() + `",
			"owner_script_hash":"` + repeatHex("66", 32) + `",
			"owner_btc_addr":null,
			"state":"active",
			"pass_kind":"standard",
			"raw_energy":"340282366920938463463374607431768211455",
			"collab_contribution":"0",
			"effective_energy":"340282366920938463463374607431768211455",
			"level":50,
			"difficulty_factor_bps":5000,
			"collab_breakdown_count":2
		}
	}`
	var view PassEconomicProfileView
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatalf("failed to decode profile contract: %v", err)
	}
	if view.ViewVersion != EconomicStateViewVersionV1 || view.ExternalState.BTCHeight != 123 {
		t.Fatalf("unexpected profile identity: %+v", view)
	}
	if view.Pass.RawEnergy != maximumEnergyValue.String() || view.Pass.Level != MaximumLevel || view.Pass.DifficultyFactorBps != 5_000 {
		t.Fatalf("unexpected profile values: %+v", view.Pass)
	}
}

func TestQueryContextEncodesUIP0007ExpectedState(t *testing.T) {
	query := QueryContext{
		RequestedHeight: 123,
		ExpectedState: QueryExpectedState{
			SnapshotID:    repeatHex("11", 32),
			SystemStateID: repeatHex("22", 32),
		},
	}
	raw, err := json.Marshal(query)
	if err != nil {
		t.Fatalf("failed to encode query context: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed to inspect query context: %v", err)
	}
	if decoded["requested_height"] != float64(123) {
		t.Fatalf("unexpected requested height: %v", decoded["requested_height"])
	}
	expectedState := decoded["expected_state"].(map[string]interface{})
	if expectedState["snapshot_id"] != query.ExpectedState.SnapshotID || expectedState["system_state_id"] != query.ExpectedState.SystemStateID {
		t.Fatalf("unexpected expected state: %+v", expectedState)
	}
}

func TestPassEconomicProfileParamsMatchUIP0006Contract(t *testing.T) {
	selector := newTestSelector(t, 123)
	query := QueryContext{
		RequestedHeight: selector.BTCHeight,
		ExpectedState: QueryExpectedState{
			SnapshotID:    selector.SnapshotIDHex(),
			SystemStateID: selector.SystemStateIDHex(),
		},
	}
	params := passEconomicProfileParams{
		ViewVersion: EconomicStateViewVersionV1,
		PassID:      selector.PassID.String(),
		BlockHeight: selector.BTCHeight,
		Context:     query,
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("failed to encode profile params: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed to inspect profile params: %v", err)
	}
	for _, field := range []string{"view_version", "pass_id", "block_height", "context"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("missing profile param %q in %s", field, raw)
		}
	}
	if len(decoded) != 4 {
		t.Fatalf("unexpected profile params: %s", raw)
	}
}

func TestRPCClientCallsPassEconomicProfileContract(t *testing.T) {
	selector := newTestSelector(t, 123)
	want := newTestProfileView(t, selector, "1000000", "500000")
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("failed to encode fake response: %v", err)
	}
	transport := &stubJSONRPCClient{response: raw}
	client := &RPCClient{client: transport}
	query := QueryContext{
		RequestedHeight: selector.BTCHeight,
		ExpectedState: QueryExpectedState{
			SnapshotID:    selector.SnapshotIDHex(),
			SystemStateID: selector.SystemStateIDHex(),
		},
	}
	got, err := client.GetPassEconomicProfile(context.Background(), selector.PassID, query)
	if err != nil {
		t.Fatalf("profile RPC failed: %v", err)
	}
	if transport.method != "get_pass_economic_profile" || len(transport.args) != 1 {
		t.Fatalf("unexpected RPC call: method=%q args=%+v", transport.method, transport.args)
	}
	params, ok := transport.args[0].(passEconomicProfileParams)
	if !ok {
		t.Fatalf("unexpected params type: %T", transport.args[0])
	}
	if params.ViewVersion != EconomicStateViewVersionV1 || params.PassID != selector.PassID.String() || params.BlockHeight != selector.BTCHeight {
		t.Fatalf("unexpected profile params: %+v", params)
	}
	if got.Pass.EffectiveEnergy != want.Pass.EffectiveEnergy || got.ExternalState != want.ExternalState {
		t.Fatalf("unexpected decoded profile: have %+v want %+v", got, want)
	}
	client.Close()
	if !transport.closed {
		t.Fatal("expected RPC transport to be closed")
	}
}

func TestRPCClientPropagatesProfileCallErrorsAndNull(t *testing.T) {
	selector := newTestSelector(t, 123)
	query := QueryContext{RequestedHeight: selector.BTCHeight}
	wantErr := errors.New("rpc unavailable")
	client := &RPCClient{client: &stubJSONRPCClient{err: wantErr}}
	if _, err := client.GetPassEconomicProfile(context.Background(), selector.PassID, query); !errors.Is(err, wantErr) {
		t.Fatalf("expected transport error, got %v", err)
	}
	client = &RPCClient{client: &stubJSONRPCClient{response: json.RawMessage("null")}}
	profile, err := client.GetPassEconomicProfile(context.Background(), selector.PassID, query)
	if err != nil || profile != nil {
		t.Fatalf("expected null profile, got profile=%+v err=%v", profile, err)
	}
}
