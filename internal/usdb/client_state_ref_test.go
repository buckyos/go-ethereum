package usdb

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestRPCClientHistoricalStateRefContract(t *testing.T) {
	selector := newTestSelector(t, 100)
	want := testHistoricalStateRef(*newTestSystemStateInfo(t, selector))
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	transport := &stubJSONRPCClient{response: raw}
	client := &RPCClient{client: transport}
	query := QueryContext{RequestedHeight: 100, ExpectedState: QueryExpectedState{
		SnapshotID: selector.SnapshotIDHex(), SystemStateID: selector.SystemStateIDHex(),
		ActivationRegistryID: BTCRegtestMinerPassV2RegistryID,
	}}
	got, err := client.GetStateRefAtHeight(context.Background(), query)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if transport.method != "get_state_ref_at_height" || len(transport.args) != 1 {
		t.Fatal("wrong method/arity")
	}
	encoded, err := json.Marshal(transport.args[0])
	if err != nil {
		t.Fatal(err)
	}
	var params struct {
		BlockHeight uint32       `json:"block_height"`
		Context     QueryContext `json:"context"`
	}
	if err := json.Unmarshal(encoded, &params); err != nil {
		t.Fatal(err)
	}
	if params.BlockHeight != 100 || params.Context != query {
		t.Fatalf("wrong wire selectors: %s", encoded)
	}
	for _, code := range []int{rpcErrSnapshotNotReady, rpcErrHeightNotSynced, rpcErrStateNotRetained, rpcErrSystemStateIDMismatch} {
		transport.err = &stubRPCError{code: code}
		_, err := client.GetStateRefAtHeight(context.Background(), query)
		var rpcErr *RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != code {
			t.Fatalf("lost structured error: %v", err)
		}
	}
	transport.err = nil
	transport.response = json.RawMessage("null")
	if got, err := client.GetStateRefAtHeight(context.Background(), query); err != nil || got != nil {
		t.Fatalf("null: %+v %v", got, err)
	}
	for _, invalid := range []string{`{`, `[]`, `{"block_height":"bad"}`} {
		transport.response = json.RawMessage(invalid)
		_, err := client.GetStateRefAtHeight(context.Background(), query)
		if local, retry := ClassifyQueryFailure(err); !local || retry {
			t.Fatalf("invalid response classification: %v", err)
		}
	}
}
