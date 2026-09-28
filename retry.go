package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxNotionAttempts = 6

func newNotionHTTPClient() *http.Client {
	return &http.Client{
		// Includes rate-limit waits and body reads, so even a stalled API call
		// cannot hold an entire manifest run indefinitely.
		Timeout:   10 * time.Minute,
		Transport: &retryTransport{base: http.DefaultTransport},
	}
}

// retryTransport is shared by the SDK and direct upload calls. It follows
// Notion's retry guidance: 429/529 for replayable requests, and transient
// server/network failures only for reads and deletes. An uncertain write must
// be inspected before it is repeated.
type retryTransport struct {
	base   http.RoundTripper
	wait   func(context.Context, time.Duration) error
	jitter func() time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	idempotent := req.Method == http.MethodGet || req.Method == http.MethodDelete
	for attempt := 0; attempt < maxNotionAttempts; attempt++ {
		copy := req.Clone(req.Context())
		if attempt > 0 && req.Body != nil {
			if req.GetBody == nil {
				return nil, fmt.Errorf("cannot retry %s %s: request body cannot be replayed", req.Method, req.URL)
			}
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("cannot replay request body: %w", err)
			}
			copy.Body = body
		}
		response, err := base.RoundTrip(copy)
		if err != nil {
			if !idempotent || attempt == maxNotionAttempts-1 || req.Context().Err() != nil {
				return nil, err
			}
			if err := t.pause(req.Context(), t.delay(nil, attempt)); err != nil {
				return nil, err
			}
			continue
		}
		retry := response.StatusCode == http.StatusTooManyRequests || response.StatusCode == 529 ||
			(idempotent && (response.StatusCode == 500 || response.StatusCode == 502 ||
				response.StatusCode == 503 || response.StatusCode == 504))
		if response.StatusCode == http.StatusTooManyRequests && blockedByNotion(response) {
			retry = false
		}
		if !retry || attempt == maxNotionAttempts-1 {
			return response, nil
		}
		if req.Body != nil && req.GetBody == nil {
			return response, nil
		}
		delay := t.delay(response, attempt)
		response.Body.Close()
		if err := t.pause(req.Context(), delay); err != nil {
			return nil, err
		}
	}
	panic("unreachable")
}

type originalCloser struct {
	io.Reader
	io.Closer
}

func blockedByNotion(response *http.Response) bool {
	// Error bodies are small. Preserve every byte for the caller when this is
	// a non-retryable restricted connection or the final attempt.
	original := response.Body
	prefix, _ := io.ReadAll(io.LimitReader(original, 64<<10))
	response.Body = originalCloser{Reader: io.MultiReader(bytes.NewReader(prefix), original), Closer: original}
	return bytes.Contains(prefix, []byte(`"public_api_request_blocked"`))
}

func (t *retryTransport) delay(response *http.Response, attempt int) time.Duration {
	base := time.Duration(1<<attempt) * time.Second
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	if response != nil {
		if seconds, err := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After"))); err == nil && seconds >= 0 {
			base = time.Duration(seconds) * time.Second
		}
	}
	if t.jitter != nil {
		return base + t.jitter()
	}
	return base + time.Duration(rand.Intn(250))*time.Millisecond
}

func (t *retryTransport) pause(ctx context.Context, delay time.Duration) error {
	if t.wait != nil {
		return t.wait(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
