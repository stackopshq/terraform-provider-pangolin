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
	"strings"
	"testing"
)

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
	tests := []struct {
		name         string
		status       int
		wantSentinel error
	}{
		{"unauthorized", http.StatusUnauthorized, ErrUnauthorized},
		{"forbidden", http.StatusForbidden, ErrForbidden},
		{"not found", http.StatusNotFound, ErrNotFound},
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
				for _, sentinel := range []error{ErrNotFound, ErrUnauthorized, ErrForbidden, ErrServer} {
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
	if !strings.Contains(err.Error(), "request failed") {
		t.Errorf("error %q does not wrap transport failure", err)
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
