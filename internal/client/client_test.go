package client

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// disableRetryBackoff shrinks the retry waits so tests that exercise the
// retry loop don't pay the production 100ms/200ms delays. Restored at
// end of test.
func disableRetryBackoff(t *testing.T) {
	t.Helper()
	origMin, origMax := retryWaitMin, retryWaitMax
	retryWaitMin = 1 * time.Millisecond
	retryWaitMax = 1 * time.Millisecond
	t.Cleanup(func() {
		retryWaitMin = origMin
		retryWaitMax = origMax
	})
}

// newTestClient wires a *Client to an httptest.Server. The test server is
// closed automatically when the test ends.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{
		BaseURL:    srv.URL,
		APIKey:     "test-key",
		OrgID:      "test-org",
		HTTPClient: srv.Client(),
	}
}

// writeEnvelope renders a payload using the Pangolin API envelope. Status
// codes >= 400 are written with an "error" message so the client treats
// them as failures.
func writeEnvelope(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	env := APIResponse{
		Status:  status,
		Success: status < 400,
		Error:   status >= 400,
	}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode payload: %v", err)
		}
		env.Data = raw
	}
	if status >= 400 {
		env.Message = "test failure"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(env); err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
}

func TestDoRequest_SetsAuthHeaderAndPathPrefix(t *testing.T) {
	var (
		gotAuth   string
		gotPath   string
		gotMethod string
	)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotMethod = r.Method
		writeEnvelope(t, w, http.StatusOK, nil)
	})

	if _, err := c.doRequest(context.Background(), "GET", "/site/42", nil); err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotPath != "/v1/site/42" {
		t.Errorf("path = %q, want %q", gotPath, "/v1/site/42")
	}
	if gotMethod != "GET" {
		t.Errorf("method = %q, want GET", gotMethod)
	}
}

func TestDoRequest_StatusClassification(t *testing.T) {
	disableRetryBackoff(t)
	tests := []struct {
		name         string
		status       int
		wantSentinel error
	}{
		{"unauthorized", http.StatusUnauthorized, ErrUnauthorized},
		{"forbidden", http.StatusForbidden, ErrForbidden},
		{"not found", http.StatusNotFound, ErrNotFound},
		{"rate limited", http.StatusTooManyRequests, ErrRateLimited},
		{"internal server", http.StatusInternalServerError, ErrServer},
		{"bad gateway", http.StatusBadGateway, ErrServer},
		{"teapot is not classified", http.StatusTeapot, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeEnvelope(t, w, tc.status, nil)
			})
			_, err := c.doRequest(context.Background(), "GET", "/whatever", nil)
			if err == nil {
				t.Fatalf("expected error for status %d", tc.status)
			}
			if tc.wantSentinel == nil {
				for _, sentinel := range []error{ErrNotFound, ErrUnauthorized, ErrForbidden, ErrRateLimited, ErrServer, ErrTransport} {
					if errors.Is(err, sentinel) {
						t.Errorf("status %d should not match %v, got %v", tc.status, sentinel, err)
					}
				}
				return
			}
			if !errors.Is(err, tc.wantSentinel) {
				t.Errorf("status %d: errors.Is(%v) = false, want true", tc.status, tc.wantSentinel)
			}
		})
	}
}

func TestDoRequest_SuccessReturnsData(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]string{"hello": "world"})
	})
	resp, err := c.doRequest(context.Background(), "GET", "/x", nil)
	if err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["hello"] != "world" {
		t.Errorf("data = %v, want hello=world", got)
	}
}

func TestDoRequest_ParseErrorIncludesStatus(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "not json at all")
	})
	_, err := c.doRequest(context.Background(), "GET", "/x", nil)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "failed to parse response") {
		t.Errorf("error %q does not mention parse failure", err)
	}
}

func TestDoRequest_TransportError(t *testing.T) {
	disableRetryBackoff(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	c := &Client{
		BaseURL:    srv.URL,
		APIKey:     "test-key",
		HTTPClient: srv.Client(),
	}
	srv.Close() // force the next request to fail at the transport layer

	_, err := c.doRequest(context.Background(), "GET", "/x", nil)
	if err == nil {
		t.Fatal("expected transport error after server close")
	}
	if !errors.Is(err, ErrTransport) {
		t.Errorf("err = %v, want errors.Is(ErrTransport) = true", err)
	}
}

func TestDoRequest_ContextCancellation(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, nil)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so the request never goes out

	_, err := c.doRequest(ctx, "GET", "/x", nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestDoRequest_SendsJSONBody(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		writeEnvelope(t, w, http.StatusOK, nil)
	})

	body := map[string]any{"name": "homelab", "count": 3}
	if _, err := c.doRequest(context.Background(), "POST", "/x", body); err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	if got["name"] != "homelab" {
		t.Errorf("server saw name = %v, want homelab", got["name"])
	}
	if got["count"].(float64) != 3 {
		t.Errorf("server saw count = %v, want 3", got["count"])
	}
}

func TestGetSite_HappyPath(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/site/42" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, Site{
			SiteID: 42,
			NiceID: "nice-42",
			Name:   "homelab",
			Type:   "newt",
			Online: true,
		})
	})

	got, err := c.GetSite(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetSite: %v", err)
	}
	if got.SiteID != 42 || got.Name != "homelab" {
		t.Errorf("got %+v, want SiteID=42 Name=homelab", got)
	}
}

func TestGetSite_404IsErrNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusNotFound, nil)
	})

	_, err := c.GetSite(context.Background(), 42)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestGetSiteResource_NotInListReturnsErrNotFound(t *testing.T) {
	// The Pangolin API has no working per-id GET for site resources, so the
	// client lists and filters. An empty list must surface as ErrNotFound so
	// callers (and Read methods) can drop state instead of erroring.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, struct {
			SiteResources []SiteResource `json:"siteResources"`
		}{SiteResources: []SiteResource{{SiteResourceID: 1}, {SiteResourceID: 2}}})
	})

	_, err := c.GetSiteResource(context.Background(), 999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// startTLSServer spins up an httptest TLS server with a self-signed cert
// and returns it together with the cert encoded as PEM (for use with
// WithCAPool).
func startTLSServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, []byte) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	cert := srv.Certificate()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	return srv, pemBytes
}

func TestNewClient_TLSWithCAPool(t *testing.T) {
	srv, pemBytes := startTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, nil)
	})

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		t.Fatal("failed to append test cert to pool")
	}
	c := NewClient(srv.URL, "test-key", "test-org", WithCAPool(pool))

	if _, err := c.doRequest(context.Background(), "GET", "/x", nil); err != nil {
		t.Errorf("doRequest with CA pool: %v", err)
	}
}

func TestNewClient_TLSWithoutCAPoolFails(t *testing.T) {
	srv, _ := startTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, nil)
	})

	c := NewClient(srv.URL, "test-key", "test-org")
	_, err := c.doRequest(context.Background(), "GET", "/x", nil)
	if err == nil {
		t.Fatal("expected TLS verification failure without trusted CA")
	}
	// Avoid asserting on the exact error string — Go's TLS error wording
	// differs across versions. Anything that mentions certificate or x509
	// is good enough to confirm we tripped on verification.
	msg := err.Error()
	if !strings.Contains(msg, "certificate") && !strings.Contains(msg, "x509") {
		t.Errorf("error %q does not look like a TLS verification failure", msg)
	}
}

func TestNewClient_WithInsecureTLS(t *testing.T) {
	srv, _ := startTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, nil)
	})

	c := NewClient(srv.URL, "test-key", "test-org", WithInsecureTLS())
	if _, err := c.doRequest(context.Background(), "GET", "/x", nil); err != nil {
		t.Errorf("doRequest with InsecureTLS: %v", err)
	}
}

func TestGetSiteResource_ListHit(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, struct {
			SiteResources []SiteResource `json:"siteResources"`
		}{SiteResources: []SiteResource{
			{SiteResourceID: 1, Name: "a"},
			{SiteResourceID: 2, Name: "b"},
		}})
	})

	got, err := c.GetSiteResource(context.Background(), 2)
	if err != nil {
		t.Fatalf("GetSiteResource: %v", err)
	}
	if got.SiteResourceID != 2 || got.Name != "b" {
		t.Errorf("got %+v, want SiteResourceID=2 Name=b", got)
	}
}

// --- Retry policy tests ---

func TestDoRequest_RetriesOnServerError(t *testing.T) {
	disableRetryBackoff(t)
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			writeEnvelope(t, w, http.StatusServiceUnavailable, nil)
			return
		}
		writeEnvelope(t, w, http.StatusOK, map[string]string{"ok": "yes"})
	})

	resp, err := c.doRequest(context.Background(), "GET", "/x", nil)
	if err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("server hit %d times, want 3 (2 retries after first 503)", got)
	}
	var data map[string]string
	if err := json.Unmarshal(resp.Data, &data); err != nil || data["ok"] != "yes" {
		t.Errorf("final response = %v, want ok=yes", data)
	}
}

func TestDoRequest_RetriesOnRateLimit(t *testing.T) {
	disableRetryBackoff(t)
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeEnvelope(t, w, http.StatusTooManyRequests, nil)
			return
		}
		writeEnvelope(t, w, http.StatusOK, nil)
	})

	if _, err := c.doRequest(context.Background(), "GET", "/x", nil); err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server hit %d times, want 2 (1 retry after 429)", got)
	}
}

func TestDoRequest_RetriesOnTransportError(t *testing.T) {
	// First attempt: server is closed → transport error.
	// Subsequent attempts: server is up → 200.
	// Simulated by swapping the BaseURL between attempts.
	disableRetryBackoff(t)

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, nil)
	}))
	t.Cleanup(good.Close)

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close() // immediate: any request fails at the transport layer
	deadURL := dead.URL

	c := &Client{
		BaseURL:    deadURL,
		APIKey:     "test-key",
		HTTPClient: good.Client(),
	}
	// Swap to the good URL after the first failed attempt by hooking the
	// transport: simplest is to swap BaseURL right before retry by using
	// a roundtripper. Here we just rotate URLs across attempts manually.
	var calls atomic.Int32
	c.HTTPClient = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		n := calls.Add(1)
		if n == 1 {
			// pretend the connection died
			return nil, errors.New("connection reset by peer")
		}
		req.URL.Host = mustHost(t, good.URL)
		req.URL.Scheme = "http"
		return good.Client().Do(req)
	})}

	if _, err := c.doRequest(context.Background(), "GET", "/x", nil); err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("transport called %d times, want 2 (1 retry after transport failure)", got)
	}
}

func TestDoRequest_NoRetryOnPOST(t *testing.T) {
	disableRetryBackoff(t)
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeEnvelope(t, w, http.StatusServiceUnavailable, nil)
	})

	_, err := c.doRequest(context.Background(), http.MethodPost, "/x", nil)
	if !errors.Is(err, ErrServer) {
		t.Errorf("err = %v, want ErrServer", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("POST hit server %d times, want 1 (no retry on POST)", got)
	}
}

func TestDoRequest_NoRetryOn404(t *testing.T) {
	disableRetryBackoff(t)
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeEnvelope(t, w, http.StatusNotFound, nil)
	})

	_, err := c.doRequest(context.Background(), "GET", "/x", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("GET hit server %d times, want 1 (no retry on 404)", got)
	}
}

func TestDoRequest_GivesUpAfterMaxAttempts(t *testing.T) {
	disableRetryBackoff(t)
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeEnvelope(t, w, http.StatusServiceUnavailable, nil)
	})

	_, err := c.doRequest(context.Background(), "GET", "/x", nil)
	if !errors.Is(err, ErrServer) {
		t.Errorf("err = %v, want ErrServer after max attempts", err)
	}
	if got := calls.Load(); got != int32(maxAttempts) {
		t.Errorf("server hit %d times, want %d (maxAttempts)", got, maxAttempts)
	}
}

func TestDoRequest_RetryRespectsContextCancellation(t *testing.T) {
	// Keep the production backoff (~100ms) so the test can race a cancel
	// against the wait between attempts.
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeEnvelope(t, w, http.StatusServiceUnavailable, nil)
	})

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay — long enough for the first attempt to
	// complete but well within the first backoff window (100ms).
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := c.doRequest(ctx, "GET", "/x", nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if got := calls.Load(); got >= int32(maxAttempts) {
		t.Errorf("server hit %d times, expected cancellation to short-circuit before maxAttempts (%d)", got, maxAttempts)
	}
}

func TestShouldRetry(t *testing.T) {
	cases := []struct {
		name   string
		method string
		err    error
		want   bool
	}{
		{"nil err", "GET", nil, false},
		{"GET 5xx", "GET", ErrServer, true},
		{"GET 429", "GET", ErrRateLimited, true},
		{"GET transport", "GET", ErrTransport, true},
		{"GET 404", "GET", ErrNotFound, false},
		{"GET 401", "GET", ErrUnauthorized, false},
		{"PUT 5xx", "PUT", ErrServer, true},
		{"DELETE 5xx", "DELETE", ErrServer, true},
		{"POST 5xx never retries", "POST", ErrServer, false},
		{"POST 429 never retries", "POST", ErrRateLimited, false},
		{"POST transport never retries", "POST", ErrTransport, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRetry(tc.method, tc.err); got != tc.want {
				t.Errorf("shouldRetry(%q, %v) = %v, want %v", tc.method, tc.err, got, tc.want)
			}
		})
	}
}

func TestBackoffDelay(t *testing.T) {
	// Use the production values explicitly so the test doesn't pick up a
	// disableRetryBackoff leak from another test.
	origMin, origMax := retryWaitMin, retryWaitMax
	retryWaitMin = 100 * time.Millisecond
	retryWaitMax = 2 * time.Second
	t.Cleanup(func() {
		retryWaitMin = origMin
		retryWaitMax = origMax
	})

	cases := []struct {
		retryNum int
		want     time.Duration
	}{
		{0, 100 * time.Millisecond},  // defensive: <1 yields min
		{1, 100 * time.Millisecond},  // 100 * 2^0
		{2, 200 * time.Millisecond},  // 100 * 2^1
		{3, 400 * time.Millisecond},  // 100 * 2^2
		{4, 800 * time.Millisecond},  // 100 * 2^3
		{5, 1600 * time.Millisecond}, // 100 * 2^4
		{6, 2 * time.Second},         // would be 3.2s, capped
		{100, 2 * time.Second},       // overflow defense
	}
	for _, tc := range cases {
		if got := backoffDelay(tc.retryNum); got != tc.want {
			t.Errorf("backoffDelay(%d) = %v, want %v", tc.retryNum, got, tc.want)
		}
	}
}

// roundTripperFunc adapts a plain function to http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// mustHost extracts the host from an httptest server URL (e.g.
// "http://127.0.0.1:42613" → "127.0.0.1:42613").
func mustHost(t *testing.T, rawURL string) string {
	t.Helper()
	const prefix = "http://"
	if !strings.HasPrefix(rawURL, prefix) {
		t.Fatalf("unexpected test URL %q", rawURL)
	}
	return strings.TrimPrefix(rawURL, prefix)
}

// --- Request audit log tests ---

func TestListRequestLogs_EmptyResult(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/org/test-org/logs/request" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"log":              []any{},
			"pagination":       map[string]any{"total": 0, "limit": 1000, "offset": 0},
			"filterAttributes": map[string]any{"actors": []string{}, "resources": []string{}, "locations": []string{}, "hosts": []string{}, "paths": []string{}},
		})
	})

	res, err := c.ListRequestLogs(context.Background(), "test-org", RequestLogQuery{})
	if err != nil {
		t.Fatalf("ListRequestLogs: %v", err)
	}
	if len(res.Log) != 0 {
		t.Errorf("Log = %v, want empty", res.Log)
	}
	if res.Pagination.Total != 0 || res.Pagination.Limit != 1000 {
		t.Errorf("pagination = %+v, want total=0 limit=1000", res.Pagination)
	}
}

func TestListRequestLogs_QueryParamsEncoded(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"log":              []any{},
			"pagination":       map[string]any{"total": 0, "limit": 0, "offset": 0},
			"filterAttributes": map[string]any{},
		})
	})

	_, err := c.ListRequestLogs(context.Background(), "test-org", RequestLogQuery{
		TimeStart:  "2026-05-01T00:00:00Z",
		TimeEnd:    "2026-05-25T23:59:59Z",
		Method:     "GET",
		ResourceID: "42",
		Path:       "/api/secret",
		Actor:      "alice@example.com",
		Limit:      "50",
	})
	if err != nil {
		t.Fatalf("ListRequestLogs: %v", err)
	}

	want := map[string]string{
		"timeStart":  "2026-05-01T00:00:00Z",
		"timeEnd":    "2026-05-25T23:59:59Z",
		"method":     "GET",
		"resourceId": "42",
		"path":       "/api/secret",
		"actor":      "alice@example.com",
		"limit":      "50",
	}
	for k, v := range want {
		// url.Values.Encode escapes special chars; decode to compare
		got, err := url.QueryUnescape(extractParam(gotQuery, k))
		if err != nil {
			t.Errorf("decode %s: %v", k, err)
			continue
		}
		if got != v {
			t.Errorf("query[%q] = %q, want %q", k, got, v)
		}
	}
	// Unset fields must not appear
	for _, k := range []string{"action", "reason", "location", "host", "offset"} {
		if extractParam(gotQuery, k) != "" {
			t.Errorf("query[%q] should be absent, got it set", k)
		}
	}
}

func TestListRequestLogs_EntriesAndFilterAttributes(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"log": []map[string]any{
				{
					"timestamp":  "2026-05-25T10:00:00Z",
					"actor":      "alice@example.com",
					"method":     "GET",
					"reason":     "accept",
					"resourceId": "42",
					"location":   "Paris, FR",
					"host":       "app.example.com",
					"path":       "/dashboard",
					// extra field not modeled — should land in Raw
					"statusCode": 200,
					"userAgent":  "curl/8.4",
				},
				{
					"timestamp": "2026-05-25T10:01:00Z",
					"actor":     "anonymous",
					"method":    "POST",
					"reason":    "deny:no_credentials",
					"path":      "/admin",
				},
			},
			"pagination": map[string]any{"total": 2, "limit": 1000, "offset": 0},
			"filterAttributes": map[string]any{
				"actors":    []string{"alice@example.com", "anonymous"},
				"resources": []string{"42"},
				"locations": []string{"Paris, FR"},
				"hosts":     []string{"app.example.com"},
				"paths":     []string{"/dashboard", "/admin"},
			},
		})
	})

	res, err := c.ListRequestLogs(context.Background(), "test-org", RequestLogQuery{})
	if err != nil {
		t.Fatalf("ListRequestLogs: %v", err)
	}
	if len(res.Log) != 2 {
		t.Fatalf("Log len = %d, want 2", len(res.Log))
	}

	e0 := res.Log[0]
	if e0.Actor != "alice@example.com" || e0.Method != "GET" || e0.Reason != "accept" {
		t.Errorf("e0 modeled fields = %+v", e0)
	}
	// Raw must include the unmodeled fields
	if !strings.Contains(string(e0.Raw), `"statusCode":200`) {
		t.Errorf("e0.Raw missing statusCode: %s", e0.Raw)
	}
	if !strings.Contains(string(e0.Raw), `"userAgent":"curl/8.4"`) {
		t.Errorf("e0.Raw missing userAgent: %s", e0.Raw)
	}

	if res.Log[1].Actor != "anonymous" || res.Log[1].Reason != "deny:no_credentials" {
		t.Errorf("e1 = %+v", res.Log[1])
	}

	if len(res.FilterAttributes.Actors) != 2 || res.FilterAttributes.Actors[0] != "alice@example.com" {
		t.Errorf("FilterAttributes.Actors = %v", res.FilterAttributes.Actors)
	}
	if res.Pagination.Total != 2 {
		t.Errorf("Pagination.Total = %d, want 2", res.Pagination.Total)
	}
}

func TestListRequestLogs_ErrorPropagates(t *testing.T) {
	disableRetryBackoff(t)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusForbidden, nil)
	})

	_, err := c.ListRequestLogs(context.Background(), "test-org", RequestLogQuery{})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

// --- Enterprise Org fields tests ---

func TestGetOrg_FullEnterpriseShape(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/org/stackopshq" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"org": map[string]any{
				"orgId":                              "stackopshq",
				"name":                               "StackOps HQ",
				"subnet":                             "100.90.137.0/18",
				"utilitySubnet":                      "100.96.128.0/18",
				"createdAt":                          "2026-05-06T17:06:19.939Z",
				"requireTwoFactor":                   true,
				"maxSessionLengthHours":              12,
				"passwordExpiryDays":                 90,
				"settingsLogRetentionDaysRequest":    7,
				"settingsLogRetentionDaysAccess":     30,
				"settingsLogRetentionDaysAction":     30,
				"settingsLogRetentionDaysConnection": 14,
				"sshCaPrivateKey":                    "encrypted-blob-here",
				"sshCaPublicKey":                     "ssh-ed25519 AAAA...",
				"isBillingOrg":                       true,
				"billingOrgId":                       "stackopshq",
			},
		})
	})

	org, err := c.GetOrg(context.Background(), "stackopshq")
	if err != nil {
		t.Fatalf("GetOrg: %v", err)
	}
	if org.OrgID != "stackopshq" || org.Name != "StackOps HQ" {
		t.Errorf("identity wrong: %+v", org)
	}
	if org.Subnet != "100.90.137.0/18" || org.UtilitySubnet != "100.96.128.0/18" {
		t.Errorf("subnets wrong: %s / %s", org.Subnet, org.UtilitySubnet)
	}
	if org.RequireTwoFactor == nil || *org.RequireTwoFactor != true {
		t.Errorf("RequireTwoFactor = %v, want *true", org.RequireTwoFactor)
	}
	if org.MaxSessionLengthHours == nil || *org.MaxSessionLengthHours != 12 {
		t.Errorf("MaxSessionLengthHours = %v, want *12", org.MaxSessionLengthHours)
	}
	if org.PasswordExpiryDays == nil || *org.PasswordExpiryDays != 90 {
		t.Errorf("PasswordExpiryDays = %v, want *90", org.PasswordExpiryDays)
	}
	if org.SettingsLogRetentionDaysAccess != 30 || org.SettingsLogRetentionDaysConnection != 14 {
		t.Errorf("retention wrong: %+v", org)
	}
	if org.SSHCaPublicKey == "" || org.SSHCaPrivateKey == "" {
		t.Errorf("ssh CA missing: pub=%q priv=%q", org.SSHCaPublicKey, org.SSHCaPrivateKey)
	}
	if !org.IsBillingOrg || org.BillingOrgID != "stackopshq" {
		t.Errorf("billing wrong: %+v", org)
	}
}

func TestGetOrg_NullableFieldsStayNil(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"org": map[string]any{
				"orgId":                              "stackopshq",
				"name":                               "StackOps HQ",
				"subnet":                             "100.90.137.0/18",
				"utilitySubnet":                      "100.96.128.0/18",
				"requireTwoFactor":                   nil,
				"maxSessionLengthHours":              nil,
				"passwordExpiryDays":                 nil,
				"settingsLogRetentionDaysRequest":    7,
				"settingsLogRetentionDaysAccess":     0,
				"settingsLogRetentionDaysAction":     0,
				"settingsLogRetentionDaysConnection": 0,
			},
		})
	})

	org, err := c.GetOrg(context.Background(), "stackopshq")
	if err != nil {
		t.Fatalf("GetOrg: %v", err)
	}
	if org.RequireTwoFactor != nil {
		t.Errorf("RequireTwoFactor = %v, want nil", org.RequireTwoFactor)
	}
	if org.MaxSessionLengthHours != nil {
		t.Errorf("MaxSessionLengthHours = %v, want nil", org.MaxSessionLengthHours)
	}
	if org.PasswordExpiryDays != nil {
		t.Errorf("PasswordExpiryDays = %v, want nil", org.PasswordExpiryDays)
	}
}

func TestUpdateOrg_PartialBodyOmitsUnsetFields(t *testing.T) {
	var gotBody map[string]json.RawMessage
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/org/stackopshq" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"orgId": "stackopshq", "name": "updated",
		})
	})

	two := true
	twelve := 12
	if _, err := c.UpdateOrg(context.Background(), "stackopshq", &UpdateOrgRequest{
		Name:                  "updated",
		RequireTwoFactor:      &two,
		MaxSessionLengthHours: &twelve,
		// Others left nil — must NOT appear in body
	}); err != nil {
		t.Fatalf("UpdateOrg: %v", err)
	}

	wantKeys := []string{"name", "requireTwoFactor", "maxSessionLengthHours"}
	unwantedKeys := []string{
		"passwordExpiryDays",
		"settingsLogRetentionDaysRequest",
		"settingsLogRetentionDaysAccess",
		"settingsLogRetentionDaysAction",
		"settingsLogRetentionDaysConnection",
	}
	for _, k := range wantKeys {
		if _, ok := gotBody[k]; !ok {
			t.Errorf("body missing %q", k)
		}
	}
	for _, k := range unwantedKeys {
		if _, ok := gotBody[k]; ok {
			t.Errorf("body should not contain %q, got %s", k, gotBody[k])
		}
	}
}

func TestUpdateOrg_ZeroValueRetentionIsSent(t *testing.T) {
	// Sending settingsLogRetentionDaysAccess = 0 must reach the wire as
	// `"settingsLogRetentionDaysAccess":0`, not get dropped by omitempty.
	// The client uses *int + omitempty: a nil pointer is omitted, a
	// pointer to 0 stays.
	var gotBody map[string]json.RawMessage
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"orgId": "stackopshq", "name": "x",
		})
	})

	zero := 0
	if _, err := c.UpdateOrg(context.Background(), "stackopshq", &UpdateOrgRequest{
		SettingsLogRetentionDaysAccess: &zero,
	}); err != nil {
		t.Fatalf("UpdateOrg: %v", err)
	}
	if string(gotBody["settingsLogRetentionDaysAccess"]) != "0" {
		t.Errorf("settingsLogRetentionDaysAccess in body = %s, want 0", gotBody["settingsLogRetentionDaysAccess"])
	}
}

// extractParam grabs the value of a single URL query parameter from a raw
// query string. Returns "" if absent. Does not unescape.
func extractParam(rawQuery, key string) string {
	for _, kv := range strings.Split(rawQuery, "&") {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		if kv[:eq] == key {
			return kv[eq+1:]
		}
	}
	return ""
}
