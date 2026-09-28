package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func retryResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestNotionClientHasBoundedSharedHTTP(t *testing.T) {
	client := NewNotionClient("test-token")
	if client.NotionHTTP.Client.Timeout <= 0 {
		t.Fatal("direct requests have no timeout")
	}
	if _, ok := client.NotionHTTP.Client.Transport.(*retryTransport); !ok {
		t.Fatal("direct requests do not use retry transport")
	}
}

func TestRetryRespectsRetryAfterAndReplaysWriteBody(t *testing.T) {
	calls := 0
	var delays []time.Duration
	transport := &retryTransport{
		base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			body, _ := io.ReadAll(request.Body)
			if string(body) != `{"children":[]}` {
				t.Errorf("attempt %d body = %q", calls, body)
			}
			if calls == 1 {
				response := retryResponse(429, `{"code":"rate_limited"}`)
				response.Header.Set("Retry-After", "2")
				return response, nil
			}
			return retryResponse(200, "ok"), nil
		}),
		wait: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
		jitter: func() time.Duration { return 0 },
	}
	request, _ := http.NewRequest(http.MethodPatch, "https://api.notion.com/v1/blocks/id/children", strings.NewReader(`{"children":[]}`))
	response, err := transport.RoundTrip(request)
	if err != nil || response.StatusCode != 200 || calls != 2 || len(delays) != 1 || delays[0] != 2*time.Second {
		t.Fatalf("response=%v err=%v calls=%d delays=%v", response, err, calls, delays)
	}
	response.Body.Close()
}

func TestRetryAvoidsUncertainWriteReplay(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		netErr bool
	}{
		{"server error", 503, false},
		{"network error", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			transport := &retryTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if tc.netErr {
					return nil, errors.New("connection lost")
				}
				return retryResponse(tc.status, "temporary"), nil
			})}
			request, _ := http.NewRequest(http.MethodPost, "https://api.notion.com/v1/pages", strings.NewReader("{}"))
			response, err := transport.RoundTrip(request)
			if calls != 1 || (tc.netErr && err == nil) || (!tc.netErr && response.StatusCode != tc.status) {
				t.Fatalf("response=%v err=%v calls=%d", response, err, calls)
			}
			if response != nil {
				response.Body.Close()
			}
		})
	}
}

func TestRetryReadsAndDeletesOnTransientFailure(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		calls := 0
		transport := &retryTransport{
			base: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return retryResponse(503, "temporary"), nil
				}
				return retryResponse(200, "ok"), nil
			}),
			wait:   func(context.Context, time.Duration) error { return nil },
			jitter: func() time.Duration { return 0 },
		}
		request, _ := http.NewRequest(method, "https://api.notion.com/v1/blocks/id", nil)
		response, err := transport.RoundTrip(request)
		if err != nil || calls != 2 || response.StatusCode != 200 {
			t.Fatalf("%s response=%v err=%v calls=%d", method, response, err, calls)
		}
		response.Body.Close()
	}
}

func TestRetryReadAfterNetworkError(t *testing.T) {
	calls := 0
	transport := &retryTransport{
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("connection reset")
			}
			return retryResponse(200, "ok"), nil
		}),
		wait:   func(context.Context, time.Duration) error { return nil },
		jitter: func() time.Duration { return 0 },
	}
	request, _ := http.NewRequest(http.MethodGet, "https://api.notion.com/v1/pages/id", nil)
	response, err := transport.RoundTrip(request)
	if err != nil || calls != 2 || response.StatusCode != 200 {
		t.Fatalf("response=%v err=%v calls=%d", response, err, calls)
	}
	response.Body.Close()
}

func TestHTTPTimeoutStopsStalledTransport(t *testing.T) {
	client := &http.Client{
		Timeout: 50 * time.Millisecond,
		Transport: &retryTransport{base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})},
	}
	_, err := client.Get("https://api.notion.com/v1/pages/id")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled request error = %v, want deadline exceeded", err)
	}
}

func TestBlockedConnectionIsNotRetried(t *testing.T) {
	calls := 0
	transport := &retryTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return retryResponse(429, `{"additional_data":{"rate_limit_reason":"public_api_request_blocked"}}`), nil
	})}
	request, _ := http.NewRequest(http.MethodGet, "https://api.notion.com/v1/pages/id", nil)
	response, err := transport.RoundTrip(request)
	if err != nil || calls != 1 || response.StatusCode != 429 {
		t.Fatalf("response=%v err=%v calls=%d", response, err, calls)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(body), "public_api_request_blocked") {
		t.Fatalf("caller lost response body: %q", body)
	}
}
