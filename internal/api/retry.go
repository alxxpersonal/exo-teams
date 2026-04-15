package api

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// --- Retry ---

const (
	maxRetryAttempts = 3
	retryBaseDelay   = 500 * time.Millisecond
	retryMaxJitter   = 200 * time.Millisecond
)

// retryableStatus reports whether an HTTP status code warrants a retry.
// Covers rate limiting (429), OneDrive file lock (423), and transient server errors.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusLocked,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// retryableError reports whether a transport-level error is transient.
// Includes timeouts and errors implementing Temporary().
func retryableError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		type temporary interface{ Temporary() bool }
		var tmp temporary
		if errors.As(err, &tmp) && tmp.Temporary() {
			return true
		}
	}
	return false
}

// parseRetryAfter returns the Retry-After delay from a response.
// Accepts both integer seconds and HTTP-date formats. Returns zero if absent or invalid.
func parseRetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	hdr := resp.Header.Get("Retry-After")
	if hdr == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(hdr)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(hdr); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// backoffDelay computes exponential backoff with jitter for a given attempt.
// attempt is zero-indexed: 0 -> 500ms, 1 -> 1s, 2 -> 2s, plus up to 200ms jitter.
// The jitter is scheduling noise, not a security boundary, so math/rand is fine.
func backoffDelay(attempt int) time.Duration {
	base := retryBaseDelay << attempt
	jitter := time.Duration(rand.Int64N(int64(retryMaxJitter) + 1)) // #nosec G404
	return base + jitter
}

// sleepCtx sleeps for d or until ctx is cancelled, whichever comes first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryDoer describes what retryDo needs to build and send a single attempt.
// buildReq must return a fresh request for each call; this lets callers rebuild
// the body and swap in a refreshed token before the retried send.
type retryDoer struct {
	ctx      context.Context
	client   *http.Client
	buildReq func() (*http.Request, error)
}

// retryDo executes the request with exponential backoff on retryable failures.
// Returns the final response. Callers are responsible for closing Body.
func retryDo(d retryDoer) (*http.Response, error) {
	var (
		resp    *http.Response
		lastErr error
	)
	for attempt := 0; attempt < maxRetryAttempts; attempt++ {
		if err := d.ctx.Err(); err != nil {
			return nil, err
		}

		req, err := d.buildReq()
		if err != nil {
			return nil, err
		}

		resp, lastErr = d.client.Do(req)
		if lastErr == nil && !retryableStatus(resp.StatusCode) {
			return resp, nil
		}

		isLastAttempt := attempt == maxRetryAttempts-1
		if isLastAttempt {
			if lastErr != nil {
				return nil, lastErr
			}
			return resp, nil
		}

		// Decide whether to retry.
		retry := false
		var wait time.Duration
		if lastErr != nil {
			retry = retryableError(lastErr)
		} else {
			retry = retryableStatus(resp.StatusCode)
			if retry {
				wait = parseRetryAfter(resp)
			}
		}
		if !retry {
			return resp, lastErr
		}

		// Drain and close the body before retrying so the connection can be reused.
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			resp = nil
		}

		if wait <= 0 {
			wait = backoffDelay(attempt)
		}
		if err := sleepCtx(d.ctx, wait); err != nil {
			return nil, err
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return resp, nil
}
