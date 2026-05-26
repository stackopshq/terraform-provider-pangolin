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
	// Payload mirrors a real /logs/request entry observed against the
	// enterprise self-host. All numeric / boolean fields land on the
	// wire as their JSON-native types, not strings.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"log": []map[string]any{
				{
					"id":                 965105,
					"timestamp":          1779694741,
					"orgId":              "test-org",
					"action":             true,
					"reason":             101,
					"actorType":          "user",
					"actor":              "alice@example.com",
					"actorId":            "u-42",
					"resourceId":         14,
					"siteResourceId":     nil,
					"ip":                 "82.65.56.201",
					"location":           "FR",
					"userAgent":          "curl/8.4",
					"metadata":           map[string]any{"trace": "abc"},
					"headers":            nil,
					"query":              nil,
					"originalRequestURL": "https://app.example.com/dashboard",
					"scheme":             "",
					"host":               "app.example.com",
					"path":               "/dashboard",
					"method":             "GET",
					"tls":                true,
					"resourceName":       "Dashboard",
					"resourceNiceId":     "absolute-blue-fox",
				},
				{
					"id":         965106,
					"timestamp":  1779694800,
					"orgId":      "test-org",
					"action":     false,
					"reason":     403,
					"actorType":  nil,
					"actor":      nil,
					"actorId":    nil,
					"resourceId": 14,
					"path":       "/admin",
					"method":     "POST",
					"tls":        true,
				},
			},
			"pagination": map[string]any{"total": 2, "limit": 1000, "offset": 0},
			"filterAttributes": map[string]any{
				"actors":    []string{"alice@example.com"},
				"resources": []map[string]any{{"id": 14, "name": "Dashboard"}},
				"locations": []string{"FR"},
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
	if e0.ID != 965105 || e0.Timestamp != 1779694741 {
		t.Errorf("e0 numeric fields = id=%d ts=%d", e0.ID, e0.Timestamp)
	}
	if !e0.Action || e0.Reason != 101 {
		t.Errorf("e0 action/reason = %v / %d, want true / 101", e0.Action, e0.Reason)
	}
	if e0.ResourceID != 14 {
		t.Errorf("e0 ResourceID = %d, want 14", e0.ResourceID)
	}
	if e0.Actor == nil || *e0.Actor != "alice@example.com" {
		t.Errorf("e0 Actor = %v", e0.Actor)
	}
	if e0.IP != "82.65.56.201" || !e0.TLS || e0.Method != "GET" {
		t.Errorf("e0 transport fields = %+v", e0)
	}
	if string(e0.Metadata) != `{"trace":"abc"}` {
		t.Errorf("e0.Metadata = %s, want object", e0.Metadata)
	}
	// Raw must include every original field
	if !strings.Contains(string(e0.Raw), `"resourceNiceId":"absolute-blue-fox"`) {
		t.Errorf("e0.Raw missing resourceNiceId: %s", e0.Raw)
	}

	e1 := res.Log[1]
	if e1.Action {
		t.Errorf("e1.Action = true, want false")
	}
	if e1.Reason != 403 {
		t.Errorf("e1.Reason = %d, want 403", e1.Reason)
	}
	if e1.Actor != nil || e1.ActorType != nil || e1.ActorID != nil {
		t.Errorf("e1 nullable actor fields = %v / %v / %v, want all nil", e1.Actor, e1.ActorType, e1.ActorID)
	}

	if len(res.FilterAttributes.Actors) != 1 || res.FilterAttributes.Actors[0] != "alice@example.com" {
		t.Errorf("FilterAttributes.Actors = %v", res.FilterAttributes.Actors)
	}
	if len(res.FilterAttributes.Resources) != 1 || res.FilterAttributes.Resources[0].ID != 14 || res.FilterAttributes.Resources[0].Name != "Dashboard" {
		t.Errorf("FilterAttributes.Resources = %+v", res.FilterAttributes.Resources)
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

// --- Resource set-replace endpoints tests ---

func TestSetResourceRoles_BodyAndPath(t *testing.T) {
	var gotBody map[string]json.RawMessage
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/resource/7/roles" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode: %v", err)
		}
		writeEnvelope(t, w, http.StatusCreated, map[string]any{})
	})
	if err := c.SetResourceRoles(context.Background(), 7, []int{2, 3}); err != nil {
		t.Fatalf("SetResourceRoles: %v", err)
	}
	if string(gotBody["roleIds"]) != "[2,3]" {
		t.Errorf("roleIds = %s, want [2,3]", gotBody["roleIds"])
	}
}

func TestSetResourceRoles_NilTreatedAsEmptyArray(t *testing.T) {
	var gotBody map[string]json.RawMessage
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode: %v", err)
		}
		writeEnvelope(t, w, http.StatusCreated, map[string]any{})
	})
	if err := c.SetResourceRoles(context.Background(), 7, nil); err != nil {
		t.Fatalf("SetResourceRoles: %v", err)
	}
	if string(gotBody["roleIds"]) != "[]" {
		t.Errorf("roleIds = %s, want []", gotBody["roleIds"])
	}
}

func TestSetResourceUsers_BodyAndPath(t *testing.T) {
	var gotBody map[string]json.RawMessage
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/resource/7/users" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode: %v", err)
		}
		writeEnvelope(t, w, http.StatusCreated, map[string]any{})
	})
	if err := c.SetResourceUsers(context.Background(), 7, []string{"u-1", "u-2"}); err != nil {
		t.Fatalf("SetResourceUsers: %v", err)
	}
	if string(gotBody["userIds"]) != `["u-1","u-2"]` {
		t.Errorf("userIds = %s", gotBody["userIds"])
	}
}

func TestSetResourceWhitelist_BodyAndPath(t *testing.T) {
	var gotBody map[string]json.RawMessage
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/resource/7/whitelist" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode: %v", err)
		}
		writeEnvelope(t, w, http.StatusCreated, map[string]any{})
	})
	if err := c.SetResourceWhitelist(context.Background(), 7, []string{"alice@example.com", "bob@example.com"}); err != nil {
		t.Fatalf("SetResourceWhitelist: %v", err)
	}
	if string(gotBody["emails"]) != `["alice@example.com","bob@example.com"]` {
		t.Errorf("emails = %s", gotBody["emails"])
	}
}

func TestSetResourceWhitelist_ErrorWhenDisabled(t *testing.T) {
	disableRetryBackoff(t)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Simulate the 400 the API returns when emailWhitelistEnabled = false.
		writeEnvelope(t, w, http.StatusBadRequest, nil)
	})
	err := c.SetResourceWhitelist(context.Background(), 7, []string{"x@example.com"})
	if err == nil {
		t.Fatal("expected error when whitelist disabled")
	}
}

// --- User-centric role binding tests ---

func TestAddRoleToUser_PathAndMethod(t *testing.T) {
	var hits int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Method != "POST" || r.URL.Path != "/v1/user/u-123/add-role/7" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"userId": "u-123", "orgId": "org", "roleId": 7,
		})
	})
	if err := c.AddRoleToUser(context.Background(), "u-123", 7); err != nil {
		t.Fatalf("AddRoleToUser: %v", err)
	}
	if hits != 1 {
		t.Errorf("server hits = %d, want 1", hits)
	}
}

func TestRemoveRoleFromUser_PathAndMethod(t *testing.T) {
	var hits int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Method != "DELETE" || r.URL.Path != "/v1/user/u-123/remove-role/7" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, nil)
	})
	if err := c.RemoveRoleFromUser(context.Background(), "u-123", 7); err != nil {
		t.Fatalf("RemoveRoleFromUser: %v", err)
	}
	if hits != 1 {
		t.Errorf("server hits = %d, want 1", hits)
	}
}

func TestUserHasRole_HitAndMiss(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"users": []map[string]any{
				{
					"id": "u-1", "username": "alice", "orgId": "org",
					"roles": []map[string]any{{"roleId": 1, "roleName": "Admin"}, {"roleId": 2, "roleName": "Member"}},
				},
				{
					"id": "u-2", "username": "bob", "orgId": "org",
					"roles": []map[string]any{{"roleId": 2, "roleName": "Member"}},
				},
			},
		})
	})

	if has, err := c.UserHasRole(context.Background(), "u-1", 1); err != nil || !has {
		t.Errorf("alice/1: has=%v err=%v, want true/nil", has, err)
	}
	if has, err := c.UserHasRole(context.Background(), "u-1", 2); err != nil || !has {
		t.Errorf("alice/2: has=%v err=%v, want true/nil", has, err)
	}
	if has, err := c.UserHasRole(context.Background(), "u-2", 1); err != nil || has {
		t.Errorf("bob/1: has=%v err=%v, want false/nil", has, err)
	}
	_, err := c.UserHasRole(context.Background(), "u-999", 1)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("missing user err = %v, want ErrNotFound", err)
	}
}

// --- Resource whitelist GET tests ---

func TestListResourceWhitelist_EmptyList(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/resource/7/whitelist" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"whitelist": []any{}})
	})

	emails, err := c.ListResourceWhitelist(context.Background(), 7)
	if err != nil {
		t.Fatalf("ListResourceWhitelist: %v", err)
	}
	if len(emails) != 0 {
		t.Errorf("emails = %v, want empty", emails)
	}
}

func TestListResourceWhitelist_StringItems(t *testing.T) {
	// Shape A: items are plain email strings.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"whitelist": []string{"alice@example.com", "bob@example.com"},
		})
	})

	emails, err := c.ListResourceWhitelist(context.Background(), 7)
	if err != nil {
		t.Fatalf("ListResourceWhitelist: %v", err)
	}
	if len(emails) != 2 || emails[0] != "alice@example.com" || emails[1] != "bob@example.com" {
		t.Errorf("emails = %v", emails)
	}
}

func TestListResourceWhitelist_ObjectItems(t *testing.T) {
	// Shape B: items are {email} objects — the other form the server may emit.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"whitelist": []map[string]any{
				{"email": "alice@example.com"},
				{"email": "bob@example.com"},
			},
		})
	})

	emails, err := c.ListResourceWhitelist(context.Background(), 7)
	if err != nil {
		t.Fatalf("ListResourceWhitelist: %v", err)
	}
	if len(emails) != 2 || emails[0] != "alice@example.com" {
		t.Errorf("emails = %v", emails)
	}
}

func TestListResourceWhitelist_NotFound(t *testing.T) {
	disableRetryBackoff(t)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusNotFound, nil)
	})
	_, err := c.ListResourceWhitelist(context.Background(), 999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- Target hcHeaders wire-shape normalization tests ---

func TestTarget_UnmarshalJSON_StringEncodedHCHeaders(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"targetId":   23,
			"resourceId": 7,
			"siteId":     4,
			"ip":         "127.0.0.1",
			"method":     "http",
			"port":       9090,
			"enabled":    true,
			"hcEnabled":  true,
			"hcStatus":   200,
			"hcHeaders":  `[{"name":"X-Probe","value":"yes"}]`,
		})
	})

	target, err := c.GetTarget(context.Background(), 23)
	if err != nil {
		t.Fatalf("GetTarget: %v", err)
	}
	if target.HCHeadersRaw == nil || *target.HCHeadersRaw != `[{"name":"X-Probe","value":"yes"}]` {
		t.Errorf("HCHeadersRaw = %v, want the inner JSON-string", target.HCHeadersRaw)
	}
	headers, err := ParseTargetHCHeaders(target.HCHeadersRaw)
	if err != nil {
		t.Fatalf("ParseTargetHCHeaders: %v", err)
	}
	if len(headers) != 1 || headers[0].Name != "X-Probe" || headers[0].Value != "yes" {
		t.Errorf("Parsed headers = %+v", headers)
	}
}

func TestTarget_UnmarshalJSON_NativeArrayHCHeaders(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"targetId":   24,
			"resourceId": 7,
			"siteId":     4,
			"ip":         "127.0.0.1",
			"method":     "http",
			"port":       9090,
			"enabled":    true,
			"hcEnabled":  true,
			"hcHeaders":  []map[string]any{{"name": "X-Probe", "value": "yes"}, {"name": "X-Suite", "value": "smoke"}},
		})
	})

	target, err := c.GetTarget(context.Background(), 24)
	if err != nil {
		t.Fatalf("GetTarget: %v", err)
	}
	if target.HCHeadersRaw == nil {
		t.Fatal("HCHeadersRaw should not be nil for a native array payload")
	}
	headers, err := ParseTargetHCHeaders(target.HCHeadersRaw)
	if err != nil {
		t.Fatalf("ParseTargetHCHeaders: %v", err)
	}
	if len(headers) != 2 || headers[0].Name != "X-Probe" || headers[1].Value != "smoke" {
		t.Errorf("Parsed headers = %+v", headers)
	}
}

func TestTarget_UnmarshalJSON_NullHCHeaders(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"targetId":   25,
			"resourceId": 7,
			"siteId":     4,
			"ip":         "127.0.0.1",
			"method":     "http",
			"port":       9090,
			"enabled":    true,
			"hcHeaders":  nil,
		})
	})

	target, err := c.GetTarget(context.Background(), 25)
	if err != nil {
		t.Fatalf("GetTarget: %v", err)
	}
	if target.HCHeadersRaw != nil {
		t.Errorf("HCHeadersRaw = %v, want nil", target.HCHeadersRaw)
	}
	headers, err := ParseTargetHCHeaders(target.HCHeadersRaw)
	if err != nil || len(headers) != 0 {
		t.Errorf("Parsed headers from nil = %+v, %v", headers, err)
	}
}

func TestCreateTarget_HCFieldsOnWire(t *testing.T) {
	var gotBody map[string]json.RawMessage
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/v1/resource/7/target" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"targetId": 1, "resourceId": 7, "siteId": 4,
			"ip": "127.0.0.1", "method": "http", "port": 9090, "enabled": true,
		})
	})

	hcEnabled := true
	hcStatus := 200
	priority := 50
	if _, err := c.CreateTarget(context.Background(), 7, &CreateTargetRequest{
		IP: "127.0.0.1", Port: 9090, Method: "http", SiteID: 4,
		HCEnabled: &hcEnabled, HCStatus: &hcStatus,
		HCHeaders: []TargetHCHeader{{Name: "X-Probe", Value: "yes"}},
		Priority:  &priority,
	}); err != nil {
		t.Fatalf("CreateTarget: %v", err)
	}
	if string(gotBody["hcEnabled"]) != "true" {
		t.Errorf("hcEnabled wire = %s, want true", gotBody["hcEnabled"])
	}
	if string(gotBody["hcStatus"]) != "200" {
		t.Errorf("hcStatus wire = %s, want 200", gotBody["hcStatus"])
	}
	if string(gotBody["hcHeaders"]) != `[{"name":"X-Probe","value":"yes"}]` {
		t.Errorf("hcHeaders wire = %s, want native array", gotBody["hcHeaders"])
	}
	if string(gotBody["priority"]) != "50" {
		t.Errorf("priority wire = %s, want 50", gotBody["priority"])
	}
	for _, k := range []string{"hcPath", "hcMethod", "hcInterval", "hcHealthyThreshold", "rewritePath"} {
		if _, ok := gotBody[k]; ok {
			t.Errorf("body should not contain %q (left unset)", k)
		}
	}
}

// --- User extended shape + GetUserByUsername tests ---

func TestListUsers_ExtendedFields(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"users": []map[string]any{
				{
					"id":               "nb625aydwn9l4e3",
					"username":         "kallioli",
					"email":            nil,
					"emailVerified":    true,
					"dateCreated":      "2026-04-02T21:17:17.146Z",
					"orgId":            "stackops",
					"name":             nil,
					"type":             "oidc",
					"isOwner":          false,
					"idpName":          "Authentik",
					"idpId":            1,
					"idpType":          "oidc",
					"idpVariant":       "oidc",
					"twoFactorEnabled": false,
					"roles": []map[string]any{
						{"roleId": 2, "roleName": "Member"},
					},
				},
			},
		})
	})

	users, err := c.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("len = %d, want 1", len(users))
	}
	u := users[0]
	if u.IdpID != 1 || u.IdpName != "Authentik" || u.IdpVariant != "oidc" {
		t.Errorf("idp fields = %+v", u)
	}
	if u.Name != nil {
		t.Errorf("Name = %v, want nil pointer", u.Name)
	}
	if u.TwoFactorEnabled || u.IsOwner {
		t.Errorf("flags should be false: 2fa=%v owner=%v", u.TwoFactorEnabled, u.IsOwner)
	}
	if len(u.Roles) != 1 || u.Roles[0].RoleID != 2 || u.Roles[0].RoleName != "Member" {
		t.Errorf("Roles = %+v", u.Roles)
	}
}

func TestGetUserByUsername_BothQueryParamsSent(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.URL.Path != "/v1/org/stackops/user-by-username" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"userId":           "u-42",
			"orgId":            "stackops",
			"username":         "alice",
			"email":            "alice@example.com",
			"name":             "Alice Example",
			"type":             "oidc",
			"isOwner":          true,
			"twoFactorEnabled": true,
			"roles": []map[string]any{
				{"roleId": 1, "roleName": "Admin"},
			},
		})
	})

	out, err := c.GetUserByUsername(context.Background(), "stackops", "alice", 7)
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if out.UserID != "u-42" || out.IsOwner != true || !out.TwoFactorEnabled {
		t.Errorf("user = %+v", out)
	}
	if out.Email == nil || *out.Email != "alice@example.com" {
		t.Errorf("Email = %v", out.Email)
	}
	for k, want := range map[string]string{"username": "alice", "idpId": "7"} {
		got := extractParam(gotQuery, k)
		if got != want {
			t.Errorf("query[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestGetUserByUsername_NotFound(t *testing.T) {
	disableRetryBackoff(t)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusNotFound, nil)
	})
	_, err := c.GetUserByUsername(context.Background(), "stackops", "ghost", 1)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- Site extended shape + GetSiteByNiceID tests ---

func TestGetSiteByNiceID_FullShape(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/org/stackops/site/smart-marbled-salamander" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"siteId":              4,
			"orgId":               "stackops",
			"niceId":              "smart-marbled-salamander",
			"exitNodeId":          1,
			"name":                "Kwatch",
			"pubKey":              "KWRyJnnUBxd0adPAYu7FQegMXapahlAKaeGzIhtZin8=",
			"subnet":              "100.89.128.8/30",
			"megabytesIn":         29.69217,
			"megabytesOut":        102.718094,
			"lastBandwidthUpdate": "2026-05-25T20:27:11.840Z",
			"type":                "newt",
			"online":              true,
			"lastPing":            1779741077,
			"address":             "100.90.128.0/24",
			"endpoint":            "82.65.56.201:63328",
			"publicKey":           "XY4jYKb60ITzUXyVVyUTdK5Kae1CYM6sxQ3lHc2kbFg=",
			"lastHolePunch":       1779741075,
			"listenPort":          63328,
			"dockerSocketEnabled": true,
			"status":              "approved",
			"newtId":              "q37zyhmys02nttl",
		})
	})

	site, err := c.GetSiteByNiceID(context.Background(), "stackops", "smart-marbled-salamander")
	if err != nil {
		t.Fatalf("GetSiteByNiceID: %v", err)
	}
	if site.SiteID != 4 || site.Name != "Kwatch" || site.Type != "newt" {
		t.Errorf("base fields = %+v", site)
	}
	if site.ExitNodeID == nil || *site.ExitNodeID != 1 {
		t.Errorf("ExitNodeID = %v, want *1", site.ExitNodeID)
	}
	if site.MegabytesIn < 29 || site.MegabytesIn > 30 {
		t.Errorf("MegabytesIn = %v, want ~29.7", site.MegabytesIn)
	}
	if site.Status != "approved" || site.NewtID != "q37zyhmys02nttl" {
		t.Errorf("status/newtId wrong: status=%q newtId=%q", site.Status, site.NewtID)
	}
	if site.ListenPort != 63328 {
		t.Errorf("ListenPort = %d, want 63328", site.ListenPort)
	}
}

func TestGetSiteByNiceID_NullExitNode(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"siteId":     5,
			"orgId":      "stackops",
			"niceId":     "lone-site",
			"exitNodeId": nil,
			"name":       "Detached",
			"type":       "newt",
			"online":     false,
		})
	})

	site, err := c.GetSiteByNiceID(context.Background(), "stackops", "lone-site")
	if err != nil {
		t.Fatalf("GetSiteByNiceID: %v", err)
	}
	if site.ExitNodeID != nil {
		t.Errorf("ExitNodeID = %v, want nil", site.ExitNodeID)
	}
}

func TestGetSiteByNiceID_NotFound(t *testing.T) {
	disableRetryBackoff(t)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusNotFound, nil)
	})
	_, err := c.GetSiteByNiceID(context.Background(), "stackops", "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- Domain extended shape + DNS records tests ---

func TestGetDomain_FullShape(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/org/stackops/domain/egcq4bwo41tak9o" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"domainId":           "egcq4bwo41tak9o",
			"baseDomain":         "domokit.fr",
			"verified":           true,
			"type":               "wildcard",
			"failed":             false,
			"tries":              0,
			"configManaged":      false,
			"certResolver":       nil,
			"customCertResolver": nil,
			"preferWildcardCert": false,
			"errorMessage":       nil,
		})
	})

	d, err := c.GetDomain(context.Background(), "stackops", "egcq4bwo41tak9o")
	if err != nil {
		t.Fatalf("GetDomain: %v", err)
	}
	if d.BaseDomain != "domokit.fr" || d.Type != "wildcard" || !d.Verified {
		t.Errorf("base fields wrong: %+v", d)
	}
	if d.CertResolver != nil || d.CustomCertResolver != nil || d.ErrorMessage != nil {
		t.Errorf("nullable string fields should be nil: cert=%v custom=%v err=%v",
			d.CertResolver, d.CustomCertResolver, d.ErrorMessage)
	}
}

func TestGetDomain_PopulatedNullables(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"domainId":           "x",
			"baseDomain":         "fail.example.com",
			"verified":           false,
			"type":               "cname",
			"failed":             true,
			"tries":              3,
			"certResolver":       "letsencrypt-staging",
			"customCertResolver": "internal-ca",
			"errorMessage":       "verification timed out",
		})
	})

	d, err := c.GetDomain(context.Background(), "stackops", "x")
	if err != nil {
		t.Fatalf("GetDomain: %v", err)
	}
	if !d.Failed || d.Tries != 3 {
		t.Errorf("retry fields = failed=%v tries=%d", d.Failed, d.Tries)
	}
	if d.CertResolver == nil || *d.CertResolver != "letsencrypt-staging" {
		t.Errorf("CertResolver = %v", d.CertResolver)
	}
	if d.ErrorMessage == nil || *d.ErrorMessage != "verification timed out" {
		t.Errorf("ErrorMessage = %v", d.ErrorMessage)
	}
}

func TestListDomainDNSRecords_BareArrayResponse(t *testing.T) {
	// The endpoint returns a JSON array directly under `data`, not
	// wrapped in a `{records: [...]}` object. Confirmed live.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/org/stackops/domain/x/dns-records" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, []map[string]any{
			{"id": 5, "domainId": "x", "recordType": "A", "baseDomain": "*.example.com", "value": "1.2.3.4", "verified": true},
			{"id": 6, "domainId": "x", "recordType": "TXT", "baseDomain": "example.com", "value": "v=spf1 -all", "verified": false},
		})
	})

	records, err := c.ListDomainDNSRecords(context.Background(), "stackops", "x")
	if err != nil {
		t.Fatalf("ListDomainDNSRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("len = %d, want 2", len(records))
	}
	if records[0].RecordType != "A" || records[0].Value != "1.2.3.4" || !records[0].Verified {
		t.Errorf("records[0] = %+v", records[0])
	}
	if records[1].RecordType != "TXT" || records[1].Verified {
		t.Errorf("records[1] = %+v", records[1])
	}
}

func TestGetDomain_NotFound(t *testing.T) {
	disableRetryBackoff(t)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusNotFound, nil)
	})
	_, err := c.GetDomain(context.Background(), "stackops", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- Resource sub-listings tests ---

func TestListResourceTargets_FullShape(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/resource/7/targets" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"targets": []map[string]any{
				{
					"targetId":   8,
					"resourceId": 7,
					"siteId":     4,
					"ip":         "kwatch_admin",
					"method":     "http",
					"port":       8001,
					"enabled":    true,
					"siteType":   "newt",
					"siteName":   "Kwatch",
					"hcEnabled":  false,
					"hcHealth":   "unknown",
					"hcPath":     nil,
					"priority":   100,
				},
				{
					"targetId":   9,
					"resourceId": 7,
					"siteId":     4,
					"ip":         "10.0.0.5",
					"method":     "https",
					"port":       8443,
					"enabled":    true,
					"hcEnabled":  true,
					"hcHealth":   "healthy",
					"hcPath":     "/_health",
					"hcInterval": 30,
				},
			},
		})
	})

	targets, err := c.ListResourceTargets(context.Background(), 7)
	if err != nil {
		t.Fatalf("ListResourceTargets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("len = %d, want 2", len(targets))
	}
	t0 := targets[0]
	if t0.TargetID != 8 || t0.IP != "kwatch_admin" || t0.SiteName != "Kwatch" {
		t.Errorf("t0 base = %+v", t0)
	}
	if t0.HCPath != nil || t0.HCInterval != nil {
		t.Errorf("t0 nullable hc fields should stay nil: hcPath=%v hcInterval=%v", t0.HCPath, t0.HCInterval)
	}
	if t0.Priority == nil || *t0.Priority != 100 {
		t.Errorf("t0.Priority = %v, want *100", t0.Priority)
	}
	t1 := targets[1]
	if !t1.HCEnabled || t1.HCHealth != "healthy" {
		t.Errorf("t1 hc fields wrong: %+v", t1)
	}
	if t1.HCPath == nil || *t1.HCPath != "/_health" {
		t.Errorf("t1.HCPath = %v, want *\"/_health\"", t1.HCPath)
	}
	if t1.HCInterval == nil || *t1.HCInterval != 30 {
		t.Errorf("t1.HCInterval = %v, want *30", t1.HCInterval)
	}
}

func TestListResourceRoles_FullShape(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/resource/42/roles" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"roles": []map[string]any{
				{"roleId": 1, "name": "Admin", "description": "Admin role with the most permissions", "isAdmin": true},
				{"roleId": 2, "name": "Member", "description": "Members can only view resources", "isAdmin": false},
			},
		})
	})

	roles, err := c.ListResourceRoles(context.Background(), 42)
	if err != nil {
		t.Fatalf("ListResourceRoles: %v", err)
	}
	if len(roles) != 2 {
		t.Fatalf("len = %d, want 2", len(roles))
	}
	if roles[0].RoleID != 1 || !roles[0].IsAdmin || roles[0].Name != "Admin" {
		t.Errorf("roles[0] = %+v", roles[0])
	}
	if roles[1].IsAdmin {
		t.Errorf("roles[1].IsAdmin = true, want false")
	}
}

func TestListResourceTargets_ErrorPropagates(t *testing.T) {
	disableRetryBackoff(t)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusNotFound, nil)
	})
	_, err := c.ListResourceTargets(context.Background(), 999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- Logs analytics tests ---

func TestGetLogsAnalytics_FullShape(t *testing.T) {
	// Payload mirrors the real /logs/analytics response observed
	// against the enterprise self-host. Day counts come as JSON
	// strings, totals come as ints — the client must handle both.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/org/test-org/logs/analytics" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"requestsPerCountry": []map[string]any{
				{"code": "FR", "count": 359592},
				{"code": "CH", "count": 20462},
			},
			"requestsPerDay": []map[string]any{
				{
					"day":          "2026-05-25 00:00:00+00",
					"allowedCount": "44908",
					"blockedCount": "6",
					"totalCount":   "44914",
				},
				{
					"day":          "2026-05-26 00:00:00+00",
					"allowedCount": "12345",
					"blockedCount": "0",
					"totalCount":   "12345",
				},
			},
			"totalBlocked":  587,
			"totalRequests": 391167,
		})
	})

	out, err := c.GetLogsAnalytics(context.Background(), "test-org", LogsAnalyticsQuery{})
	if err != nil {
		t.Fatalf("GetLogsAnalytics: %v", err)
	}
	if out.TotalBlocked != 587 || out.TotalRequests != 391167 {
		t.Errorf("totals = %+v", out)
	}
	if len(out.RequestsPerCountry) != 2 || out.RequestsPerCountry[0].Code != "FR" || out.RequestsPerCountry[0].Count != 359592 {
		t.Errorf("per-country = %+v", out.RequestsPerCountry)
	}
	if len(out.RequestsPerDay) != 2 {
		t.Fatalf("per-day len = %d, want 2", len(out.RequestsPerDay))
	}
	d0 := out.RequestsPerDay[0]
	if d0.Day != "2026-05-25 00:00:00+00" {
		t.Errorf("day = %q", d0.Day)
	}
	// The string-encoded counts must decode to int64s
	if d0.AllowedCount != 44908 || d0.BlockedCount != 6 || d0.TotalCount != 44914 {
		t.Errorf("day[0] counts = allowed=%d blocked=%d total=%d", d0.AllowedCount, d0.BlockedCount, d0.TotalCount)
	}
}

func TestGetLogsAnalytics_AcceptsNumericCounts(t *testing.T) {
	// Defensive: if the upstream ever tightens the contract to emit
	// numeric counts instead of strings, the decoder still works.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"requestsPerCountry": []any{},
			"requestsPerDay": []map[string]any{
				{
					"day":          "2026-05-25",
					"allowedCount": 100,
					"blockedCount": 0,
					"totalCount":   100,
				},
			},
			"totalBlocked":  0,
			"totalRequests": 100,
		})
	})

	out, err := c.GetLogsAnalytics(context.Background(), "test-org", LogsAnalyticsQuery{})
	if err != nil {
		t.Fatalf("GetLogsAnalytics: %v", err)
	}
	if out.RequestsPerDay[0].AllowedCount != 100 || out.RequestsPerDay[0].TotalCount != 100 {
		t.Errorf("numeric counts decoded wrong: %+v", out.RequestsPerDay[0])
	}
}

func TestGetLogsAnalytics_QueryParamsEncoded(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"requestsPerCountry": []any{}, "requestsPerDay": []any{},
			"totalBlocked": 0, "totalRequests": 0,
		})
	})

	_, err := c.GetLogsAnalytics(context.Background(), "test-org", LogsAnalyticsQuery{
		TimeStart:  "2026-05-01T00:00:00Z",
		TimeEnd:    "2026-05-31T23:59:59Z",
		ResourceID: "42",
	})
	if err != nil {
		t.Fatalf("GetLogsAnalytics: %v", err)
	}
	for k, want := range map[string]string{
		"timeStart":  "2026-05-01T00:00:00Z",
		"timeEnd":    "2026-05-31T23:59:59Z",
		"resourceId": "42",
	} {
		got, _ := url.QueryUnescape(extractParam(gotQuery, k))
		if got != want {
			t.Errorf("query[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestGetLogsAnalytics_StringMalformedFails(t *testing.T) {
	// A count that is neither numeric nor a numeric string should
	// surface as a parse error, not silently coerce to zero.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"requestsPerCountry": []any{},
			"requestsPerDay": []map[string]any{
				{"day": "2026-05-25", "allowedCount": "not-a-number", "blockedCount": "0", "totalCount": "0"},
			},
			"totalBlocked": 0, "totalRequests": 0,
		})
	})

	_, err := c.GetLogsAnalytics(context.Background(), "test-org", LogsAnalyticsQuery{})
	if err == nil {
		t.Fatal("expected parse error on malformed count")
	}
	if !strings.Contains(err.Error(), "allowedCount") {
		t.Errorf("error %q does not mention the bad field", err)
	}
}

// --- IDP variant tests ---

func TestListIDPs_VariantField(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/idp" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"idps": []map[string]any{
				{"idpId": 1, "name": "Authentik", "type": "oidc", "variant": "oidc", "autoProvision": true, "orgCount": "1"},
				{"idpId": 2, "name": "Google Workspace", "type": "oidc", "variant": "google", "autoProvision": true, "orgCount": "3"},
				{"idpId": 3, "name": "Entra ID", "type": "oidc", "variant": "azure", "autoProvision": false, "orgCount": "1"},
			},
		})
	})

	idps, err := c.ListIDPs(context.Background())
	if err != nil {
		t.Fatalf("ListIDPs: %v", err)
	}
	if len(idps) != 3 {
		t.Fatalf("len = %d, want 3", len(idps))
	}
	want := map[int]string{1: "oidc", 2: "google", 3: "azure"}
	for _, idp := range idps {
		if idp.Variant != want[idp.IDPId] {
			t.Errorf("idp %d Variant = %q, want %q", idp.IDPId, idp.Variant, want[idp.IDPId])
		}
	}
	// OrgCount is the string-encoded JSON quirk — must round-trip as-is
	if idps[1].OrgCount != "3" {
		t.Errorf("OrgCount = %q, want %q", idps[1].OrgCount, "3")
	}
}

func TestCreateIDP_SendsVariant(t *testing.T) {
	var gotBody map[string]json.RawMessage
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/idp/oidc" || r.Method != "PUT" {
			t.Errorf("path/method = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode: %v", err)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"idpId": 42, "redirectUrl": "https://app.example.com/callback",
		})
	})

	_, err := c.CreateIDP(context.Background(), &CreateIDPRequest{
		Name:           "Google Workspace",
		ClientID:       "cid",
		ClientSecret:   "csec",
		AuthURL:        "https://accounts.google.com/o/oauth2/auth",
		TokenURL:       "https://oauth2.googleapis.com/token",
		IdentifierPath: "sub",
		Scopes:         "openid email profile",
		Variant:        "google",
	})
	if err != nil {
		t.Fatalf("CreateIDP: %v", err)
	}
	if string(gotBody["variant"]) != `"google"` {
		t.Errorf("variant on the wire = %s, want \"google\"", gotBody["variant"])
	}
}

func TestCreateIDP_OmitsVariantWhenEmpty(t *testing.T) {
	var gotBody map[string]json.RawMessage
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode: %v", err)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"idpId": 1, "redirectUrl": "x",
		})
	})

	_, err := c.CreateIDP(context.Background(), &CreateIDPRequest{
		Name:           "Generic OIDC",
		ClientID:       "cid",
		ClientSecret:   "csec",
		AuthURL:        "https://idp.example.com/authorize",
		TokenURL:       "https://idp.example.com/token",
		IdentifierPath: "sub",
		Scopes:         "openid",
		// Variant intentionally unset — server should pick the default
	})
	if err != nil {
		t.Fatalf("CreateIDP: %v", err)
	}
	if _, ok := gotBody["variant"]; ok {
		t.Errorf("variant should be omitted from body when empty, got %s", gotBody["variant"])
	}
}

// --- Invitations tests ---

func TestCreateInvite_BodyAndResponse(t *testing.T) {
	var gotBody map[string]json.RawMessage
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/org/test-org/create-invite" || r.Method != "POST" {
			t.Errorf("path/method = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"inviteLink": "https://app.example.com/invite?token=abc123-deadbeef",
			"expiresAt":  1234567890123,
		})
	})

	out, err := c.CreateInvite(context.Background(), "test-org", &CreateInviteRequest{
		Email:      "alice@example.com",
		RoleID:     7,
		ValidHours: 24,
		SendEmail:  false,
	})
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if out.InviteLink == "" || out.ExpiresAt != 1234567890123 {
		t.Errorf("CreateInvite result = %+v", out)
	}
	if string(gotBody["email"]) != `"alice@example.com"` {
		t.Errorf("email = %s", gotBody["email"])
	}
	if string(gotBody["roleId"]) != "7" {
		t.Errorf("roleId = %s", gotBody["roleId"])
	}
	if string(gotBody["validHours"]) != "24" {
		t.Errorf("validHours = %s", gotBody["validHours"])
	}
	// sendEmail is not omitempty: must appear, with the explicit value
	if string(gotBody["sendEmail"]) != "false" {
		t.Errorf("sendEmail = %s, want false", gotBody["sendEmail"])
	}
	// Unset fields omitted
	for _, k := range []string{"roleIds", "regenerate"} {
		if _, ok := gotBody[k]; ok {
			t.Errorf("body should not contain %q", k)
		}
	}
}

func TestListInvitations_StringTotalQuirk(t *testing.T) {
	// Real upstream observation: pagination.total is a *string* ("0")
	// rather than a number. The client must not choke on this — we
	// achieve that by ignoring the pagination block entirely.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"invitations": []map[string]any{
				{
					"inviteId":  "AbCdEfGhIj",
					"email":     "alice@example.com",
					"expiresAt": 1779694244895,
					"roles":     []map[string]any{{"roleId": 7, "roleName": "Member"}},
				},
			},
			"pagination": map[string]any{
				"total":  "1", // <-- string, not number
				"limit":  1000,
				"offset": 0,
			},
		})
	})

	invites, err := c.ListInvitations(context.Background(), "test-org")
	if err != nil {
		t.Fatalf("ListInvitations: %v", err)
	}
	if len(invites) != 1 || invites[0].InviteID != "AbCdEfGhIj" {
		t.Errorf("invites = %+v", invites)
	}
	if len(invites[0].Roles) != 1 || invites[0].Roles[0].RoleID != 7 {
		t.Errorf("nested roles = %+v", invites[0].Roles)
	}
}

func TestGetInvitation_HitAndMiss(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"invitations": []map[string]any{
				{"inviteId": "id1", "email": "a@example.com", "expiresAt": 1, "roles": []any{}},
				{"inviteId": "id2", "email": "b@example.com", "expiresAt": 2, "roles": []any{}},
			},
		})
	})

	got, err := c.GetInvitation(context.Background(), "test-org", "id2")
	if err != nil || got.Email != "b@example.com" {
		t.Errorf("hit = %+v, err = %v", got, err)
	}

	_, err = c.GetInvitation(context.Background(), "test-org", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("miss err = %v, want ErrNotFound", err)
	}
}

func TestFindInvitationByEmail(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"invitations": []map[string]any{
				{"inviteId": "id1", "email": "alice@example.com", "expiresAt": 1, "roles": []any{}},
			},
		})
	})

	got, err := c.FindInvitationByEmail(context.Background(), "test-org", "alice@example.com")
	if err != nil || got.InviteID != "id1" {
		t.Errorf("hit = %+v, err = %v", got, err)
	}
	_, err = c.FindInvitationByEmail(context.Background(), "test-org", "nope@example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("miss err = %v, want ErrNotFound", err)
	}
}

func TestDeleteInvitation(t *testing.T) {
	var hits int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Method != "DELETE" || r.URL.Path != "/v1/org/test-org/invitations/id42" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, nil)
	})
	if err := c.DeleteInvitation(context.Background(), "test-org", "id42"); err != nil {
		t.Fatalf("DeleteInvitation: %v", err)
	}
	if hits != 1 {
		t.Errorf("server hits = %d, want 1", hits)
	}
}

// --- Role SSH bastion tests ---

func TestParseSSHList(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{"empty string", "", []string{}, false},
		{"empty JSON array", "[]", []string{}, false},
		{"single item", `["sudo"]`, []string{"sudo"}, false},
		{"multiple items", `["sudo","wheel","docker"]`, []string{"sudo", "wheel", "docker"}, false},
		{"invalid JSON", `not-json`, nil, true},
		{"non-array JSON", `{"a":1}`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSSHList(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for %q, got %v", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Errorf("len = %d, want %d (%v)", len(got), len(tc.want), got)
			}
			for i, v := range got {
				if v != tc.want[i] {
					t.Errorf("[%d] = %q, want %q", i, v, tc.want[i])
				}
			}
		})
	}
}

func TestGetRoleByID_SSHBastionFields(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/org/test-org/roles" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"roles": []map[string]any{
				{
					"roleId":                40632,
					"orgId":                 "test-org",
					"orgName":               "Test Org",
					"isAdmin":               true,
					"name":                  "Admin",
					"description":           "Admin role",
					"requireDeviceApproval": true,
					"allowSsh":              true,
					"sshSudoMode":           "full",
					"sshSudoCommands":       `["sudo","wheel"]`,
					"sshCreateHomeDir":      true,
					"sshUnixGroups":         `["docker","kvm"]`,
				},
				{
					"roleId":                40633,
					"isAdmin":               nil,
					"name":                  "Member",
					"description":           "View only",
					"requireDeviceApproval": false,
					"allowSsh":              false,
					"sshSudoMode":           "none",
					"sshSudoCommands":       "[]",
					"sshCreateHomeDir":      true,
					"sshUnixGroups":         "[]",
				},
			},
		})
	})

	role, err := c.GetRoleByID(context.Background(), 40632)
	if err != nil {
		t.Fatalf("GetRoleByID: %v", err)
	}
	if role.IsAdmin == nil || !*role.IsAdmin {
		t.Errorf("IsAdmin = %v, want *true", role.IsAdmin)
	}
	if !role.AllowSSH || role.SSHSudoMode != "full" {
		t.Errorf("ssh top-level wrong: %+v", role)
	}
	if role.SSHSudoCommandsRaw != `["sudo","wheel"]` {
		t.Errorf("SSHSudoCommandsRaw = %q", role.SSHSudoCommandsRaw)
	}
	parsed, err := ParseSSHList(role.SSHSudoCommandsRaw)
	if err != nil || len(parsed) != 2 || parsed[0] != "sudo" {
		t.Errorf("ParseSSHList round-trip = %v, %v", parsed, err)
	}

	// Member role: nullable IsAdmin stays nil
	member, err := c.GetRoleByID(context.Background(), 40633)
	if err != nil {
		t.Fatalf("GetRoleByID member: %v", err)
	}
	if member.IsAdmin != nil {
		t.Errorf("IsAdmin = %v, want nil", member.IsAdmin)
	}
}

func TestCreateRole_SSHBodyEncoded(t *testing.T) {
	var gotBody map[string]json.RawMessage
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"roleId": 99, "name": "new-role", "description": "from test",
		})
	})

	allow := true
	mode := "restricted"
	if _, err := c.CreateRole(context.Background(), &CreateRoleRequest{
		Name:            "new-role",
		Description:     "from test",
		AllowSSH:        &allow,
		SSHSudoMode:     &mode,
		SSHSudoCommands: []string{"sudo", "wheel"},
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	// SSH list fields go on the wire as a native JSON array, not a string
	if string(gotBody["sshSudoCommands"]) != `["sudo","wheel"]` {
		t.Errorf("sshSudoCommands wire = %s, want [\"sudo\",\"wheel\"]", gotBody["sshSudoCommands"])
	}
	if string(gotBody["allowSsh"]) != "true" {
		t.Errorf("allowSsh = %s", gotBody["allowSsh"])
	}
	if string(gotBody["sshSudoMode"]) != `"restricted"` {
		t.Errorf("sshSudoMode = %s", gotBody["sshSudoMode"])
	}
	// Unset fields must be absent
	for _, k := range []string{"requireDeviceApproval", "sshCreateHomeDir", "sshUnixGroups"} {
		if _, present := gotBody[k]; present {
			t.Errorf("body should not contain %q", k)
		}
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
