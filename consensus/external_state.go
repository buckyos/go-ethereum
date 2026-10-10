package consensus

import "errors"

// ErrExternalStateUnavailable means validation must wait for local external state.
// It is neither a timestamp error nor evidence that the remote block is invalid.
var ErrExternalStateUnavailable = errors.New("external consensus state temporarily unavailable")

// ErrExternalStateBlocked requires operator intervention instead of queued retries.
var ErrExternalStateBlocked = errors.New("external consensus state requires intervention")

// ExternalStateError preserves the original query failure and its retry policy.
type ExternalStateError struct {
	Cause error
	Retry bool
}

func (e *ExternalStateError) Error() string { return e.Cause.Error() }
func (e *ExternalStateError) Unwrap() error { return e.Cause }
func (e *ExternalStateError) Is(target error) bool {
	if e.Retry {
		return target == ErrExternalStateUnavailable
	}
	return target == ErrExternalStateBlocked
}

// IsExternalStateError identifies local failures that cannot establish peer guilt.
func IsExternalStateError(err error) bool {
	return errors.Is(err, ErrExternalStateUnavailable) || errors.Is(err, ErrExternalStateBlocked)
}
