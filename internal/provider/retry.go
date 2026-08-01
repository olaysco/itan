package provider

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// HTTPError is a typed provider failure carrying enough signal for retry
// classification and Retry-After honoring.
type HTTPError struct {
	Provider   string
	Status     int
	Body       string
	RetryAfter time.Duration // 0 when the server sent no hint
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.Status, e.Body)
}

// retryAfter parses Retry-After as delta-seconds or an HTTP date.
func retryAfter(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// RetryInfo is published to the UI while waiting — retries are visible state,
// not hidden sleeps.
type RetryInfo struct {
	Attempt int
	Max     int
	Wait    time.Duration
	Err     error
}

// Retrying wraps a Provider with classification-aware exponential backoff.
type Retrying struct {
	Inner   Provider
	Max     int             // total attempts (default 5)
	OnRetry func(RetryInfo) // optional UI callback
	sleep   func(context.Context, time.Duration) error
}

func WithRetry(p Provider, onRetry func(RetryInfo)) *Retrying {
	return &Retrying{Inner: p, Max: 5, OnRetry: onRetry, sleep: ctxSleep}
}

func (r *Retrying) Name() string { return r.Inner.Name() }

func (r *Retrying) Complete(ctx context.Context, req Request) (*Response, error) {
	max := r.Max
	if max <= 0 {
		max = 5
	}
	var lastErr error
	for attempt := 1; attempt <= max; attempt++ {
		resp, err := r.Inner.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt == max || !Retryable(err) {
			return nil, err
		}
		wait := backoff(attempt, err)
		if r.OnRetry != nil {
			r.OnRetry(RetryInfo{Attempt: attempt, Max: max, Wait: wait, Err: err})
		}
		if serr := r.sleep(ctx, wait); serr != nil {
			return nil, serr // context canceled while waiting
		}
	}
	return nil, lastErr
}

// Retryable classifies errors:
//   - network-level failures: retry
//   - 408 / 429 / all 5xx (incl. 529 overloaded): retry
//   - other 4xx (bad request, auth, context overflow): never retry
func Retryable(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		switch {
		case he.Status == 408, he.Status == 429:
			return true
		case he.Status >= 500:
			return true
		default:
			return false
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true // transport-level error (conn reset, DNS, ...)
}

// backoff: server hint first, else 2s·2^(n-1) capped at 30s, ±25% jitter.
func backoff(attempt int, err error) time.Duration {
	var he *HTTPError
	if errors.As(err, &he) && he.RetryAfter > 0 {
		return he.RetryAfter
	}
	base := 2 * time.Second << (attempt - 1)
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	jitter := time.Duration(rand.Int63n(int64(base) / 2))
	return base*3/4 + jitter
}

func ctxSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
