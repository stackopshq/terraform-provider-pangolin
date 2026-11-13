package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestDoRequestNoRetry_RetriesOnlyUnsentFailures(t *testing.T) {
	disableRetryBackoff(t)
	for _, tc := range []struct {
		name   string
		status int
		want   int32
	}{
		{"429 is retried", http.StatusTooManyRequests, int32(maxAttempts)},
		{"502 is not retried", http.StatusBadGateway, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				writeEnvelope(t, w, tc.status, nil)
			})
			_, _ = c.doRequestNoRetry(context.Background(), http.MethodPut, "/x", nil)
			if got := atomic.LoadInt32(&calls); got != tc.want {
				t.Errorf("calls = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDoRequestNoRetry_RetriesDialErrors(t *testing.T) {
	disableRetryBackoff(t)
	var dials int32
	c := NewClient("http://pangolin.invalid", "k", "o")
	c.HTTPClient.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			atomic.AddInt32(&dials, 1)
			return nil, &net.OpError{Op: "dial", Net: network, Err: errors.New("connection refused")}
		},
	}
	_, err := c.doRequestNoRetry(context.Background(), http.MethodPut, "/x", nil)
	if err == nil || !shouldRetryUnsent(err) {
		t.Fatalf("expected a retryable dial error, got %v", err)
	}
	if got := atomic.LoadInt32(&dials); got != int32(maxAttempts) {
		t.Errorf("dials = %d, want %d", got, maxAttempts)
	}
}

func TestShouldRetryUnsent(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"rate limited", fmt.Errorf("api: %w", ErrRateLimited), true},
		{"dial", fmt.Errorf("request failed: %w: %w", dial, ErrTransport), true},
		{"proxy dial", fmt.Errorf("request failed: %w: %w", &net.OpError{Op: "proxyconnect", Net: "tcp", Err: dial}, ErrTransport), true},
		{"read after write", fmt.Errorf("request failed: %w: %w", &net.OpError{Op: "read", Net: "tcp", Err: errors.New("reset")}, ErrTransport), false},
		{"server error", fmt.Errorf("api: %w", ErrServer), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRetryUnsent(tc.err); got != tc.want {
				t.Errorf("shouldRetryUnsent(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
