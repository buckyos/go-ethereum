package usdb

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"

	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

// ClassifyQueryFailure distinguishes missing local data from invalid pinned views.
// Snapshot readiness has several causes, so callers must bound retries and keep
// the existing deep-reorg halt guard. A readiness error never authorizes fallback.
func ClassifyQueryFailure(err error) (local, retry bool) {
	if err == nil {
		return false, false
	}
	if errors.Is(err, ErrHeightNotSynced) || errors.Is(err, ErrSnapshotNotReady) {
		return true, true
	}
	for _, kind := range []error{ErrInternalInvariantBroken, ErrStateNotRetained,
		ErrHistoryNotAvailable, ErrVersionNotSupported, ErrViewVersionMismatch,
		ErrFormulaVersionMismatch, ErrActivationRecordNotFound, ErrActivationRecordConflict} {
		if errors.Is(err, kind) {
			return true, false
		}
	}
	var response *rpcResponseError
	if errors.As(err, &response) {
		return true, false
	}
	var transport *rpcTransportError
	if errors.As(err, &transport) {
		if errors.Is(err, context.Canceled) {
			return true, false
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) ||
			errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNREFUSED) ||
			errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
			return true, true
		}
		var netErr net.Error
		if errors.As(err, &netErr) {
			return true, netErr.Timeout() || netErr.Temporary()
		}
		var httpErr gethrpc.HTTPError
		if errors.As(err, &httpErr) {
			switch httpErr.StatusCode {
			case 429, 502, 503, 504:
				return true, true
			}
		}
		// Authentication, malformed responses and unknown transport failures must be
		// diagnosed locally; blindly retrying them can hide configuration errors.
		return true, false
	}
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) && rpcErr.Kind == nil {
		return true, false
	}
	return false, false
}
