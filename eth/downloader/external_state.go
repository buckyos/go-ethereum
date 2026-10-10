package downloader

import (
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/log"
)

// retryExternalState keeps only the downloader's already bounded current batch.
// Every attempt re-enters normal validation without sleeping under a chain lock.
// Session cancellation releases the batch; the sync coordinator can fetch it again.
func (d *Downloader) retryExternalState(operation string, insert func() (int, error)) (int, error) {
	return d.retryExternalStateFor(operation, insert, time.Second, 2*time.Minute)
}

func (d *Downloader) retryExternalStateFor(operation string, insert func() (int, error), initial, budget time.Duration) (int, error) {
	start := time.Now()
	delay := initial
	for attempt := 0; ; attempt++ {
		select {
		case <-d.quitCh:
			return 0, errCanceled
		case <-d.cancelCh:
			return 0, errCanceled
		default:
		}
		index, err := insert()
		if !errors.Is(err, consensus.ErrExternalStateUnavailable) {
			if attempt > 0 && err == nil {
				log.Info("External state available, resumed chain validation", "operation", operation, "attempts", attempt+1, "elapsed", time.Since(start))
			}
			return index, err
		}
		remaining := budget - time.Since(start)
		if remaining <= 0 {
			log.Warn("External state wait budget exhausted", "operation", operation, "index", index, "attempts", attempt+1, "elapsed", time.Since(start), "err", err)
			return index, err
		}
		if attempt == 0 {
			log.Info("Chain validation waiting for external state", "operation", operation, "index", index, "budget", budget, "err", err)
		}
		wait := delay
		if wait > remaining {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-d.quitCh:
			timer.Stop()
			return index, errCanceled
		case <-d.cancelCh:
			timer.Stop()
			return index, errCanceled
		case <-timer.C:
		}
		delay *= 2
		if delay > 8*time.Second {
			delay = 8 * time.Second
		}
	}
}
