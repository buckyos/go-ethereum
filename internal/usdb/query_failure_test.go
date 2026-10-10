package usdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

func TestClassifyQueryFailure(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		local, retry bool
	}{
		{"height", ErrHeightNotSynced, true, true},
		{"readiness", &RPCError{Kind: ErrSnapshotNotReady}, true, true},
		{"pruned", ErrStateNotRetained, true, false},
		{"history", ErrHistoryNotAvailable, true, false},
		{"unsupported", ErrVersionNotSupported, true, false},
		{"snapshot identity", ErrSnapshotIDMismatch, false, false},
		{"system identity", ErrSystemStateIDMismatch, false, false},
		{"pass missing", ErrPassNotFound, false, false},
		{"bad profile", errors.New("invalid profile"), false, false},
		{"timeout", &rpcTransportError{cause: context.DeadlineExceeded}, true, true},
		{"closed", &rpcTransportError{cause: io.EOF}, true, true},
		{"gateway", &rpcTransportError{cause: gethrpc.HTTPError{StatusCode: 503}}, true, true},
		{"credentials", &rpcTransportError{cause: gethrpc.HTTPError{StatusCode: 401}}, true, false},
		{"malformed response", &rpcTransportError{cause: errors.New("invalid JSON")}, true, false},
		{"canceled", &rpcTransportError{cause: context.Canceled}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local, retry := ClassifyQueryFailure(fmt.Errorf("profile query: %w", tc.err))
			if local != tc.local || retry != tc.retry {
				t.Fatalf("local=%v retry=%v", local, retry)
			}
		})
	}
}
