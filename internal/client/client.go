package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Sentinel errors returned by the client. Callers should match with
// errors.Is to drive control flow (e.g. retries, state removal).
var (
	// ErrUnauthorized is returned for HTTP 401 — usually means the API
	// key is missing, malformed, expired, or has been revoked.
	ErrUnauthorized = errors.New("pangolin: unauthorized")
	// ErrForbidden is returned for HTTP 403 — usually means the API key
	// is valid but does not have access to the requested organization
	// or resource.
	ErrForbidden = errors.New("pangolin: forbidden")
	// ErrServer is returned for HTTP 5xx — the upstream is unhealthy.
	// The client retries idempotent methods on this error.
	ErrServer = errors.New("pangolin: server error")
	// ErrRateLimited is returned for HTTP 429. The client retries
	// idempotent methods on this error.
	ErrRateLimited = errors.New("pangolin: rate limited")
	// ErrTransport wraps network-layer failures (connection refused,
	// reset, TLS handshake errors, etc). The client retries idempotent
	// methods on this error.
	ErrTransport = errors.New("pangolin: transport error")
)

// Retry policy for transient failures. Values are package-level vars
// (not const) so tests can shrink the wait times.
var (
	maxAttempts  = 3
	retryWaitMin = 100 * time.Millisecond
	retryWaitMax = 2 * time.Second
)

// ErrNotFound is returned by client methods when the requested resource does
// not exist on the Pangolin API (HTTP 404 or list-and-filter miss). Callers
// should test with errors.Is(err, client.ErrNotFound) to drive Terraform state
// removal in Read methods.
var ErrNotFound = errors.New("pangolin: resource not found")

// Client is the Pangolin API client.
type Client struct {
	BaseURL    string
	APIKey     string
	OrgID      string
	HTTPClient *http.Client
}

// APIResponse is the standard Pangolin API response wrapper.
type APIResponse struct {
	Data    json.RawMessage `json:"data"`
	Success bool            `json:"success"`
	Error   bool            `json:"error"`
	Message string          `json:"message"`
	Status  int             `json:"status"`
}

// Option configures optional Client behaviors at construction time.
type Option func(*Client)

// WithCAPool installs a custom certificate pool used by the HTTP transport
// to verify the Pangolin server's TLS certificate. Use this when the
// Pangolin instance is served by a private or self-signed CA.
func WithCAPool(pool *x509.CertPool) Option {
	return func(c *Client) {
		c.tlsConfig().RootCAs = pool
	}
}

// WithInsecureTLS disables TLS certificate verification. Intended for
// local debugging only — never use against a production Pangolin.
func WithInsecureTLS() Option {
	return func(c *Client) {
		c.tlsConfig().InsecureSkipVerify = true
	}
}

// NewClient creates a new Pangolin API client.
func NewClient(baseURL, apiKey, orgID string, opts ...Option) *Client {
	c := &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		OrgID:   orgID,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// tlsConfig returns the *tls.Config of the Client's HTTP transport,
// lazily cloning the default transport on first use so we don't share
// mutable state with http.DefaultTransport.
func (c *Client) tlsConfig() *tls.Config {
	if c.HTTPClient.Transport == nil {
		c.HTTPClient.Transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	tr := c.HTTPClient.Transport.(*http.Transport)
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return tr.TLSClientConfig
}

// doRequest performs an HTTP request with retries on transient failures
// (5xx, 429, transport errors) for idempotent methods, and returns the
// parsed API response. POST requests are never retried because they are
// assumed to be resource creations.
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) (*APIResponse, error) {
	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyBytes = b
	}

	var (
		lastResp *APIResponse
		lastErr  error
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			wait := backoffDelay(attempt - 1)
			tflog.Debug(ctx, "pangolin: retrying HTTP request", map[string]any{
				"method":  method,
				"path":    path,
				"attempt": attempt,
				"wait_ms": wait.Milliseconds(),
			})
			select {
			case <-ctx.Done():
				return lastResp, ctx.Err()
			case <-time.After(wait):
			}
		}

		lastResp, lastErr = c.doRequestOnce(ctx, method, path, bodyBytes)
		if !shouldRetry(method, lastErr) {
			return lastResp, lastErr
		}
	}
	return lastResp, lastErr
}

// doRequestOnce performs a single HTTP attempt. bodyBytes may be nil.
func (c *Client) doRequestOnce(ctx context.Context, method, path string, bodyBytes []byte) (*APIResponse, error) {
	url := fmt.Sprintf("%s/v1%s", c.BaseURL, path)

	var reqBody io.Reader
	if bodyBytes != nil {
		reqBody = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	tflog.Debug(ctx, "pangolin: HTTP request", map[string]any{
		"method": method,
		"path":   path,
	})

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w: %w", err, ErrTransport)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w: %w", err, ErrTransport)
	}

	tflog.Debug(ctx, "pangolin: HTTP response", map[string]any{
		"method": method,
		"path":   path,
		"status": resp.StatusCode,
	})

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response (status %d): %s", resp.StatusCode, string(respBody))
	}

	if apiResp.Error || resp.StatusCode >= 400 {
		return &apiResp, classifyError(resp.StatusCode, apiResp.Message)
	}

	return &apiResp, nil
}

// shouldRetry decides whether a failed attempt is worth retrying. POST is
// never retried because it is assumed to create a resource server-side
// and a duplicate could leak. GET/PUT/DELETE/PATCH retry on transient
// errors (5xx, 429, transport).
func shouldRetry(method string, err error) bool {
	if err == nil {
		return false
	}
	if method == http.MethodPost {
		return false
	}
	return errors.Is(err, ErrServer) ||
		errors.Is(err, ErrRateLimited) ||
		errors.Is(err, ErrTransport)
}

// backoffDelay returns the wait before retry number retryNum (1-indexed).
// Exponential growth: 100ms, 200ms, 400ms... capped at retryWaitMax.
func backoffDelay(retryNum int) time.Duration {
	if retryNum < 1 {
		return retryWaitMin
	}
	wait := retryWaitMin << (retryNum - 1)
	if wait <= 0 || wait > retryWaitMax {
		return retryWaitMax
	}
	return wait
}

// classifyError maps an HTTP status code to a wrapped sentinel error so
// callers can distinguish missing resources, auth failures, permission
// failures, rate limits and retryable server errors with errors.Is.
// Unknown status codes fall through to a plain error.
func classifyError(status int, message string) error {
	switch {
	case status == http.StatusNotFound:
		return fmt.Errorf("API error (status 404): %s: %w", message, ErrNotFound)
	case status == http.StatusUnauthorized:
		return fmt.Errorf("API error (status 401): %s: %w", message, ErrUnauthorized)
	case status == http.StatusForbidden:
		return fmt.Errorf("API error (status 403): %s: %w", message, ErrForbidden)
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("API error (status 429): %s: %w", message, ErrRateLimited)
	case status >= 500:
		return fmt.Errorf("API error (status %d): %s: %w", status, message, ErrServer)
	default:
		return fmt.Errorf("API error (status %d): %s", status, message)
	}
}

// --- Site Defaults ---

// SiteDefaults represents the response from pick-site-defaults.
type SiteDefaults struct {
	NewtID     string `json:"newtId"`
	NewtSecret string `json:"newtSecret"`
	Address    string `json:"clientAddress"`
}

// GetSiteDefaults picks site defaults for creating a new site.
func (c *Client) GetSiteDefaults(ctx context.Context) (*SiteDefaults, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/pick-site-defaults", c.OrgID), nil)
	if err != nil {
		return nil, err
	}

	var defaults SiteDefaults
	if err := json.Unmarshal(resp.Data, &defaults); err != nil {
		return nil, fmt.Errorf("failed to parse site defaults: %w", err)
	}
	return &defaults, nil
}

// --- Sites ---

// Site represents a Pangolin site (tunnel connector).
//
// The per-id GET (used by GetSite / GetSiteByNiceID) returns a much
// richer payload than the create / list responses — exit-node assoc,
// WireGuard keys, traffic counters, public endpoint, status / newt
// metadata. All these read-only fields are omitempty so older
// CRUD code paths that fill only the first seven keep working.
type Site struct {
	SiteID              int    `json:"siteId"`
	NiceID              string `json:"niceId"`
	Name                string `json:"name"`
	Type                string `json:"type"`
	Online              bool   `json:"online"`
	Address             string `json:"address"`
	DockerSocketEnabled bool   `json:"dockerSocketEnabled"`

	// Extended fields surfaced by /org/{org}/site/{niceId}
	OrgID               string  `json:"orgId,omitempty"`
	ExitNodeID          *int    `json:"exitNodeId,omitempty"`
	PubKey              string  `json:"pubKey,omitempty"`
	Subnet              string  `json:"subnet,omitempty"`
	MegabytesIn         float64 `json:"megabytesIn,omitempty"`
	MegabytesOut        float64 `json:"megabytesOut,omitempty"`
	LastBandwidthUpdate string  `json:"lastBandwidthUpdate,omitempty"`
	LastPing            int64   `json:"lastPing,omitempty"`
	Endpoint            string  `json:"endpoint,omitempty"`
	PublicKey           string  `json:"publicKey,omitempty"`
	LastHolePunch       int64   `json:"lastHolePunch,omitempty"`
	ListenPort          int     `json:"listenPort,omitempty"`
	Status              string  `json:"status,omitempty"`
	NewtID              string  `json:"newtId,omitempty"`
}

// CreateSiteRequest is the payload for creating a site.
type CreateSiteRequest struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	NewtID  string `json:"newtId,omitempty"`
	Secret  string `json:"secret,omitempty"`
	Address string `json:"address,omitempty"`
}

// CreateSite creates a new site in the organization.
func (c *Client) CreateSite(ctx context.Context, req *CreateSiteRequest) (*Site, error) {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/org/%s/site", c.OrgID), req)
	if err != nil {
		return nil, err
	}

	var site Site
	if err := json.Unmarshal(resp.Data, &site); err != nil {
		return nil, fmt.Errorf("failed to parse site: %w", err)
	}
	return &site, nil
}

// GetSite retrieves a site by ID.
func (c *Client) GetSite(ctx context.Context, siteID int) (*Site, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/site/%d", siteID), nil)
	if err != nil {
		return nil, err
	}

	var site Site
	if err := json.Unmarshal(resp.Data, &site); err != nil {
		return nil, fmt.Errorf("failed to parse site: %w", err)
	}
	return &site, nil
}

// GetSiteByNiceID retrieves a site by its human-readable nice ID via
// GET /org/{org}/site/{niceId}. Useful when callers know the nice ID
// (the one shown in the Pangolin UI) but not the numeric site ID.
func (c *Client) GetSiteByNiceID(ctx context.Context, orgID, niceID string) (*Site, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/site/%s", orgID, niceID), nil)
	if err != nil {
		return nil, err
	}
	var site Site
	if err := json.Unmarshal(resp.Data, &site); err != nil {
		return nil, fmt.Errorf("failed to parse site: %w", err)
	}
	return &site, nil
}

// DeleteSite deletes a site by ID.
func (c *Client) DeleteSite(ctx context.Context, siteID int) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/site/%d", siteID), nil)
	return err
}

// --- Domains ---

// Domain represents a Pangolin domain. The list endpoint returns a
// richer shape than the older provider modeled — verification flags,
// retry counters, cert resolver configuration and the last error
// message are all surfaced now.
type Domain struct {
	DomainID           string  `json:"domainId"`
	BaseDomain         string  `json:"baseDomain"`
	Verified           bool    `json:"verified"`
	Type               string  `json:"type"`
	Failed             bool    `json:"failed,omitempty"`
	Tries              int     `json:"tries,omitempty"`
	ConfigManaged      bool    `json:"configManaged,omitempty"`
	CertResolver       *string `json:"certResolver,omitempty"`
	CustomCertResolver *string `json:"customCertResolver,omitempty"`
	PreferWildcardCert bool    `json:"preferWildcardCert,omitempty"`
	ErrorMessage       *string `json:"errorMessage,omitempty"`
}

// DomainDNSRecord is one row of the DNS records list returned by
// GET /org/{org}/domain/{id}/dns-records.
type DomainDNSRecord struct {
	ID         int    `json:"id"`
	DomainID   string `json:"domainId"`
	RecordType string `json:"recordType"`
	BaseDomain string `json:"baseDomain"`
	Value      string `json:"value"`
	Verified   bool   `json:"verified"`
}

// DomainsResponse wraps the domains list response.
type DomainsResponse struct {
	Domains []Domain `json:"domains"`
}

// ListDomains retrieves all domains for the organization.
func (c *Client) ListDomains(ctx context.Context) ([]Domain, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/domains", c.OrgID), nil)
	if err != nil {
		return nil, err
	}

	var domainsResp DomainsResponse
	if err := json.Unmarshal(resp.Data, &domainsResp); err != nil {
		return nil, fmt.Errorf("failed to parse domains: %w", err)
	}
	return domainsResp.Domains, nil
}

// --- Resources (HTTP public) ---

// Resource represents a Pangolin HTTP resource.
type Resource struct {
	ResourceID            int     `json:"resourceId"`
	NiceID                string  `json:"niceId"`
	Name                  string  `json:"name"`
	Subdomain             string  `json:"subdomain"`
	FullDomain            string  `json:"fullDomain"`
	DomainID              string  `json:"domainId"`
	SSO                   bool    `json:"sso"`
	SSL                   bool    `json:"ssl"`
	Enabled               bool    `json:"enabled"`
	BlockAccess           bool    `json:"blockAccess"`
	EmailWhitelistEnabled bool    `json:"emailWhitelistEnabled"`
	ApplyRules            bool    `json:"applyRules"`
	StickySession         bool    `json:"stickySession"`
	TLSServerName         *string `json:"tlsServerName"`
}

// CreateResourceRequest is the payload for creating an HTTP resource.
type CreateResourceRequest struct {
	Name      string  `json:"name"`
	HTTP      bool    `json:"http"`
	Subdomain *string `json:"subdomain"`
	DomainID  string  `json:"domainId"`
	Protocol  string  `json:"protocol"`
}

// CreateResource creates a new HTTP resource.
func (c *Client) CreateResource(ctx context.Context, req *CreateResourceRequest) (*Resource, error) {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/org/%s/resource", c.OrgID), req)
	if err != nil {
		return nil, err
	}

	var resource Resource
	if err := json.Unmarshal(resp.Data, &resource); err != nil {
		return nil, fmt.Errorf("failed to parse resource: %w", err)
	}
	return &resource, nil
}

// GetResource retrieves a resource by ID.
func (c *Client) GetResource(ctx context.Context, resourceID int) (*Resource, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/resource/%d", resourceID), nil)
	if err != nil {
		return nil, err
	}

	var resource Resource
	if err := json.Unmarshal(resp.Data, &resource); err != nil {
		return nil, fmt.Errorf("failed to parse resource: %w", err)
	}
	return &resource, nil
}

// DeleteResource deletes a resource by ID.
func (c *Client) DeleteResource(ctx context.Context, resourceID int) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/resource/%d", resourceID), nil)
	return err
}

// --- Targets ---

// TargetHCHeader is one entry of the health-check request headers
// list sent on Create / Update target bodies.
type TargetHCHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Target represents a backend target for a resource. The response
// payload now carries the full health-check + routing configuration
// returned by both the list endpoint and the per-id GET. Nullable
// upstream fields are modeled as pointer types so callers can
// distinguish "no value" from a zero / empty value.
//
// Wire quirk: hcHeaders is sent as a typed array of {name, value}
// on Create / Update bodies but comes back as a JSON-string in the
// response (cf. roles' sshSudoCommands quirk). HCHeadersRaw holds
// that string; ParseTargetHCHeaders decodes it into []TargetHCHeader.
type Target struct {
	TargetID            int    `json:"targetId"`
	ResourceID          int    `json:"resourceId"`
	SiteID              int    `json:"siteId"`
	IP                  string `json:"ip"`
	Method              string `json:"method"`
	Port                int    `json:"port"`
	Enabled             bool   `json:"enabled"`
	TargetHealthCheckID *int   `json:"targetHealthCheckId,omitempty"`
	OrgID               string `json:"orgId,omitempty"`
	Name                string `json:"name,omitempty"`
	InternalPort        *int   `json:"internalPort,omitempty"`

	// List-view extras (read-only; emitted by /resource/{id}/targets
	// and the create/update responses)
	SiteType             string  `json:"siteType,omitempty"`
	SiteName             string  `json:"siteName,omitempty"`
	HCEnabled            bool    `json:"hcEnabled,omitempty"`
	HCPath               *string `json:"hcPath,omitempty"`
	HCScheme             *string `json:"hcScheme,omitempty"`
	HCMode               *string `json:"hcMode,omitempty"`
	HCHostname           *string `json:"hcHostname,omitempty"`
	HCPort               *int    `json:"hcPort,omitempty"`
	HCInterval           *int    `json:"hcInterval,omitempty"`
	HCUnhealthyInterval  *int    `json:"hcUnhealthyInterval,omitempty"`
	HCTimeout            *int    `json:"hcTimeout,omitempty"`
	HCHeadersRaw         *string `json:"hcHeaders,omitempty"`
	HCFollowRedirects    *bool   `json:"hcFollowRedirects,omitempty"`
	HCMethod             *string `json:"hcMethod,omitempty"`
	HCStatus             *int    `json:"hcStatus,omitempty"`
	HCHealth             string  `json:"hcHealth,omitempty"`
	HCTLSServerName      *string `json:"hcTlsServerName,omitempty"`
	HCHealthyThreshold   *int    `json:"hcHealthyThreshold,omitempty"`
	HCUnhealthyThreshold *int    `json:"hcUnhealthyThreshold,omitempty"`
	Path                 *string `json:"path,omitempty"`
	PathMatchType        *string `json:"pathMatchType,omitempty"`
	RewritePath          *string `json:"rewritePath,omitempty"`
	RewritePathType      *string `json:"rewritePathType,omitempty"`
	Priority             *int    `json:"priority,omitempty"`
}

// ParseTargetHCHeaders decodes a target's hcHeaders response field —
// emitted by the server as a JSON-string (e.g. `"[]"` or `"[{\"name\":
// \"X-Probe\",\"value\":\"yes\"}]"`) — into a typed slice. Target's
// custom UnmarshalJSON already normalizes the wire-shape asymmetry
// (string vs native array), so callers only see the string form.
func ParseTargetHCHeaders(raw *string) ([]TargetHCHeader, error) {
	if raw == nil || *raw == "" || *raw == "[]" {
		return []TargetHCHeader{}, nil
	}
	var out []TargetHCHeader
	if err := json.Unmarshal([]byte(*raw), &out); err != nil {
		return nil, fmt.Errorf("parse target hcHeaders %q: %w", *raw, err)
	}
	return out, nil
}

// UnmarshalJSON normalizes the hcHeaders wire-shape asymmetry: the
// PUT /resource/{id}/target and POST /target/{id} responses emit
// hcHeaders as a JSON-encoded *string* (e.g. `"[{...}]"`), while
// GET /target/{id} returns the same data as a native JSON array.
// Both forms decode into HCHeadersRaw as the string form, so
// downstream consumers (and ParseTargetHCHeaders) work uniformly
// regardless of which endpoint produced the payload.
func (t *Target) UnmarshalJSON(data []byte) error {
	type targetAlias Target
	aux := &struct {
		HCHeaders json.RawMessage `json:"hcHeaders"`
		*targetAlias
	}{targetAlias: (*targetAlias)(t)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	switch {
	case len(aux.HCHeaders) == 0, string(aux.HCHeaders) == "null":
		t.HCHeadersRaw = nil
	case aux.HCHeaders[0] == '"':
		// JSON-encoded string form — decode the outer string layer
		var s string
		if err := json.Unmarshal(aux.HCHeaders, &s); err != nil {
			return fmt.Errorf("decode hcHeaders string: %w", err)
		}
		t.HCHeadersRaw = &s
	default:
		// Native array (or any other JSON value) — keep verbatim
		s := string(aux.HCHeaders)
		t.HCHeadersRaw = &s
	}
	return nil
}

// ListResourceTargets lists all targets that back a resource, with
// the full list-view detail (site info, health-check config, path
// routing). The per-id GetTarget call returns only the CRUD subset.
func (c *Client) ListResourceTargets(ctx context.Context, resourceID int) ([]Target, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/resource/%d/targets", resourceID), nil)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Targets []Target `json:"targets"`
	}
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse resource targets: %w", err)
	}
	return wrapper.Targets, nil
}

// ResourceRoleSummary is the slim shape returned by
// GET /resource/{id}/roles — only four fields per role, not the full
// Role struct.
type ResourceRoleSummary struct {
	RoleID      int    `json:"roleId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsAdmin     bool   `json:"isAdmin"`
}

// ListResourceRoles lists the roles assigned to a resource.
func (c *Client) ListResourceRoles(ctx context.Context, resourceID int) ([]ResourceRoleSummary, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/resource/%d/roles", resourceID), nil)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Roles []ResourceRoleSummary `json:"roles"`
	}
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse resource roles: %w", err)
	}
	return wrapper.Roles, nil
}

// CreateTargetRequest is the payload for creating a target.
//
// All hc* fields and routing extras are optional. Pointer types let
// callers send the zero value (e.g. priority=0) without colliding
// with "leave at server default" (nil → omitempty). HCHeaders is a
// typed slice; the server returns it back as a JSON-string in the
// response (cf. Target.HCHeadersRaw).
type CreateTargetRequest struct {
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Method  string `json:"method"`
	SiteID  int    `json:"siteId"`
	Enabled *bool  `json:"enabled,omitempty"`

	// Health-check configuration
	HCEnabled            *bool            `json:"hcEnabled,omitempty"`
	HCPath               *string          `json:"hcPath,omitempty"`
	HCScheme             *string          `json:"hcScheme,omitempty"`
	HCMode               *string          `json:"hcMode,omitempty"`
	HCHostname           *string          `json:"hcHostname,omitempty"`
	HCPort               *int             `json:"hcPort,omitempty"`
	HCInterval           *int             `json:"hcInterval,omitempty"`
	HCUnhealthyInterval  *int             `json:"hcUnhealthyInterval,omitempty"`
	HCTimeout            *int             `json:"hcTimeout,omitempty"`
	HCHeaders            []TargetHCHeader `json:"hcHeaders,omitempty"`
	HCFollowRedirects    *bool            `json:"hcFollowRedirects,omitempty"`
	HCMethod             *string          `json:"hcMethod,omitempty"`
	HCStatus             *int             `json:"hcStatus,omitempty"`
	HCTLSServerName      *string          `json:"hcTlsServerName,omitempty"`
	HCHealthyThreshold   *int             `json:"hcHealthyThreshold,omitempty"`
	HCUnhealthyThreshold *int             `json:"hcUnhealthyThreshold,omitempty"`

	// Routing extras
	Path            *string `json:"path,omitempty"`
	PathMatchType   *string `json:"pathMatchType,omitempty"`
	RewritePath     *string `json:"rewritePath,omitempty"`
	RewritePathType *string `json:"rewritePathType,omitempty"`
	Priority        *int    `json:"priority,omitempty"`
}

// CreateTarget creates a new target for a resource.
func (c *Client) CreateTarget(ctx context.Context, resourceID int, req *CreateTargetRequest) (*Target, error) {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/resource/%d/target", resourceID), req)
	if err != nil {
		return nil, err
	}

	var target Target
	if err := json.Unmarshal(resp.Data, &target); err != nil {
		return nil, fmt.Errorf("failed to parse target: %w", err)
	}
	return &target, nil
}

// GetTarget retrieves a target by ID.
func (c *Client) GetTarget(ctx context.Context, targetID int) (*Target, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/target/%d", targetID), nil)
	if err != nil {
		return nil, err
	}

	var target Target
	if err := json.Unmarshal(resp.Data, &target); err != nil {
		return nil, fmt.Errorf("failed to parse target: %w", err)
	}
	return &target, nil
}

// UpdateTargetRequest is the payload for updating a target. Mirrors
// the CreateTargetRequest shape — every hc* / routing field is
// optional, sent only when the pointer is non-nil.
type UpdateTargetRequest struct {
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Method  string `json:"method"`
	Enabled bool   `json:"enabled"`
	SiteID  int    `json:"siteId"`

	// Health-check configuration
	HCEnabled            *bool            `json:"hcEnabled,omitempty"`
	HCPath               *string          `json:"hcPath,omitempty"`
	HCScheme             *string          `json:"hcScheme,omitempty"`
	HCMode               *string          `json:"hcMode,omitempty"`
	HCHostname           *string          `json:"hcHostname,omitempty"`
	HCPort               *int             `json:"hcPort,omitempty"`
	HCInterval           *int             `json:"hcInterval,omitempty"`
	HCUnhealthyInterval  *int             `json:"hcUnhealthyInterval,omitempty"`
	HCTimeout            *int             `json:"hcTimeout,omitempty"`
	HCHeaders            []TargetHCHeader `json:"hcHeaders,omitempty"`
	HCFollowRedirects    *bool            `json:"hcFollowRedirects,omitempty"`
	HCMethod             *string          `json:"hcMethod,omitempty"`
	HCStatus             *int             `json:"hcStatus,omitempty"`
	HCTLSServerName      *string          `json:"hcTlsServerName,omitempty"`
	HCHealthyThreshold   *int             `json:"hcHealthyThreshold,omitempty"`
	HCUnhealthyThreshold *int             `json:"hcUnhealthyThreshold,omitempty"`

	// Routing extras
	Path            *string `json:"path,omitempty"`
	PathMatchType   *string `json:"pathMatchType,omitempty"`
	RewritePath     *string `json:"rewritePath,omitempty"`
	RewritePathType *string `json:"rewritePathType,omitempty"`
	Priority        *int    `json:"priority,omitempty"`
}

// UpdateTarget updates an existing target by ID.
func (c *Client) UpdateTarget(ctx context.Context, targetID int, req *UpdateTargetRequest) (*Target, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/target/%d", targetID), req)
	if err != nil {
		return nil, err
	}
	var target Target
	if err := json.Unmarshal(resp.Data, &target); err != nil {
		return nil, fmt.Errorf("failed to parse target: %w", err)
	}
	return &target, nil
}

// DeleteTarget deletes a target by ID.
func (c *Client) DeleteTarget(ctx context.Context, targetID int) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/target/%d", targetID), nil)
	return err
}

// --- Site Resources (private) ---

// SiteResource represents a private site resource.
type SiteResource struct {
	SiteResourceID int    `json:"siteResourceId"`
	SiteID         int    `json:"siteId"`
	NiceID         string `json:"niceId"`
	Name           string `json:"name"`
	Mode           string `json:"mode"`
	Destination    string `json:"destination"`
	Alias          string `json:"alias"`
	TCPPortRange   string `json:"tcpPortRangeString"`
	UDPPortRange   string `json:"udpPortRangeString"`
	DisableICMP    bool   `json:"disableIcmp"`
	AuthDaemonPort int    `json:"authDaemonPort"`
	AuthDaemonMode string `json:"authDaemonMode"`
}

// CreateSiteResourceRequest is the payload for creating a private site resource.
type CreateSiteResourceRequest struct {
	Name           string   `json:"name"`
	SiteID         int      `json:"siteId"`
	Mode           string   `json:"mode"`
	Destination    string   `json:"destination"`
	Alias          string   `json:"alias,omitempty"`
	TCPPortRange   string   `json:"tcpPortRangeString,omitempty"`
	UDPPortRange   string   `json:"udpPortRangeString,omitempty"`
	DisableICMP    bool     `json:"disableIcmp,omitempty"`
	AuthDaemonMode string   `json:"authDaemonMode,omitempty"`
	RoleIDs        []int    `json:"roleIds"`
	UserIDs        []string `json:"userIds"`
	ClientIDs      []int    `json:"clientIds"`
}

// CreateSiteResource creates a new private site resource.
func (c *Client) CreateSiteResource(ctx context.Context, req *CreateSiteResourceRequest) (*SiteResource, error) {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/org/%s/site-resource", c.OrgID), req)
	if err != nil {
		return nil, err
	}

	var siteResource SiteResource
	if err := json.Unmarshal(resp.Data, &siteResource); err != nil {
		return nil, fmt.Errorf("failed to parse site resource: %w", err)
	}
	return &siteResource, nil
}

// GetSiteResource retrieves a site resource by ID (via list + filter).
// Note: GET /site-resource/{id} has a bug in the Pangolin API requiring siteId/orgId,
// so we use list + filter instead.
func (c *Client) GetSiteResource(ctx context.Context, siteResourceID int) (*SiteResource, error) {
	siteResources, err := c.ListSiteResources(ctx)
	if err != nil {
		return nil, err
	}
	for _, sr := range siteResources {
		if sr.SiteResourceID == siteResourceID {
			s := sr
			return &s, nil
		}
	}
	return nil, fmt.Errorf("site resource %d: %w", siteResourceID, ErrNotFound)
}

// DeleteSiteResource deletes a site resource by ID.
func (c *Client) DeleteSiteResource(ctx context.Context, siteResourceID int) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/site-resource/%d", siteResourceID), nil)
	return err
}

// --- Roles ---

// Role represents a Pangolin role.
//
// SSH bastion fields are returned by the API. SSHSudoCommandsRaw and
// SSHUnixGroupsRaw arrive over the wire as JSON-serialized strings
// (e.g. `"[]"` or `"[\"sudo\",\"wheel\"]"`) even though the input
// schema accepts native arrays — kept as strings here so callers see
// what the API actually emits. Use ParseSSHList to materialize them
// into []string.
type Role struct {
	RoleID                int    `json:"roleId"`
	Name                  string `json:"name"`
	Description           string `json:"description"`
	IsAdmin               *bool  `json:"isAdmin,omitempty"`
	OrgID                 string `json:"orgId,omitempty"`
	OrgName               string `json:"orgName,omitempty"`
	RequireDeviceApproval bool   `json:"requireDeviceApproval"`
	AllowSSH              bool   `json:"allowSsh"`
	SSHSudoMode           string `json:"sshSudoMode,omitempty"`
	SSHSudoCommandsRaw    string `json:"sshSudoCommands,omitempty"`
	SSHCreateHomeDir      bool   `json:"sshCreateHomeDir"`
	SSHUnixGroupsRaw      string `json:"sshUnixGroups,omitempty"`
}

// ParseSSHList decodes one of the JSON-serialized list fields
// (sshSudoCommands, sshUnixGroups) into a Go slice. An empty input or
// the literal `"[]"` returns an empty slice. Any other parse error
// is returned for the caller to handle.
func ParseSSHList(raw string) ([]string, error) {
	if raw == "" || raw == "[]" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse SSH list %q: %w", raw, err)
	}
	return out, nil
}

// RolesResponse wraps the roles list response.
type RolesResponse struct {
	Roles []Role `json:"roles"`
}

// ListRoles retrieves all roles for the organization.
func (c *Client) ListRoles(ctx context.Context) ([]Role, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/roles", c.OrgID), nil)
	if err != nil {
		return nil, err
	}

	var rolesResp RolesResponse
	if err := json.Unmarshal(resp.Data, &rolesResp); err != nil {
		return nil, fmt.Errorf("failed to parse roles: %w", err)
	}
	return rolesResp.Roles, nil
}

// AddUserToRole assigns a user to a role at organization level.
func (c *Client) AddUserToRole(ctx context.Context, roleID int, userID string) error {
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/role/%d/add/%s", roleID, userID), nil)
	return err
}

// RemoveUserFromRole removes a user from a role at organization level.
func (c *Client) RemoveUserFromRole(ctx context.Context, roleID int, userID string) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/role/%d/remove/%s", roleID, userID), nil)
	return err
}

// AddRoleToUser binds an additional role to a user (cumulative).
// Distinct from AddUserToRole / POST /role/{id}/users/add, which the
// Pangolin server treats as a single-role assignment endpoint —
// calling it strips the user's other roles. Use this when you want a
// user to hold *multiple* roles simultaneously, e.g. Member + Admin.
func (c *Client) AddRoleToUser(ctx context.Context, userID string, roleID int) error {
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/user/%s/add-role/%d", userID, roleID), nil)
	return err
}

// RemoveRoleFromUser detaches one role from a user without touching
// the user's other roles. Pairs with AddRoleToUser.
func (c *Client) RemoveRoleFromUser(ctx context.Context, userID string, roleID int) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/user/%s/remove-role/%d", userID, roleID), nil)
	return err
}

// UserHasRole reports whether a given role is currently bound to a
// user. Uses the org-scoped list+filter on /users since the API does
// not expose a per-user roles endpoint.
func (c *Client) UserHasRole(ctx context.Context, userID string, roleID int) (bool, error) {
	users, err := c.ListUsers(ctx)
	if err != nil {
		return false, err
	}
	for _, u := range users {
		if u.ID != userID {
			continue
		}
		for _, r := range u.Roles {
			if r.RoleID == roleID {
				return true, nil
			}
		}
		return false, nil
	}
	return false, fmt.Errorf("user %s: %w", userID, ErrNotFound)
}

// ListRoleUsers retrieves all users assigned to a role.
func (c *Client) ListRoleUsers(ctx context.Context, roleID int) ([]string, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/role/%d/users", roleID), nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Users []struct {
			UserID string `json:"userId"`
		} `json:"users"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse role users: %w", err)
	}
	users := make([]string, len(result.Users))
	for i, u := range result.Users {
		users[i] = u.UserID
	}
	return users, nil
}

// AddRoleToResource assigns a role to an HTTP resource.
func (c *Client) AddRoleToResource(ctx context.Context, resourceID, roleID int) error {
	body := map[string]int{"roleId": roleID}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/roles/add", resourceID), body)
	return err
}

// RemoveRoleFromResource removes a role from an HTTP resource.
func (c *Client) RemoveRoleFromResource(ctx context.Context, resourceID, roleID int) error {
	body := map[string]int{"roleId": roleID}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/roles/remove", resourceID), body)
	return err
}

// AddUserToResource assigns a user to an HTTP resource.
func (c *Client) AddUserToResource(ctx context.Context, resourceID int, userID string) error {
	body := map[string]string{"userId": userID}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/users/add", resourceID), body)
	return err
}

// RemoveUserFromResource removes a user from an HTTP resource.
func (c *Client) RemoveUserFromResource(ctx context.Context, resourceID int, userID string) error {
	body := map[string]string{"userId": userID}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/users/remove", resourceID), body)
	return err
}

// AddRoleToSiteResource assigns a role to a private site resource.
func (c *Client) AddRoleToSiteResource(ctx context.Context, siteResourceID, roleID int) error {
	body := map[string]int{"roleId": roleID}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/site-resource/%d/roles/add", siteResourceID), body)
	return err
}

// RemoveRoleFromSiteResource removes a role from a private site resource.
func (c *Client) RemoveRoleFromSiteResource(ctx context.Context, siteResourceID, roleID int) error {
	body := map[string]int{"roleId": roleID}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/site-resource/%d/roles/remove", siteResourceID), body)
	return err
}

// AddUserToSiteResource assigns a user to a private site resource.
func (c *Client) AddUserToSiteResource(ctx context.Context, siteResourceID int, userID string) error {
	body := map[string]string{"userId": userID}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/site-resource/%d/users/add", siteResourceID), body)
	return err
}

// RemoveUserFromSiteResource removes a user from a private site resource.
func (c *Client) RemoveUserFromSiteResource(ctx context.Context, siteResourceID int, userID string) error {
	body := map[string]string{"userId": userID}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/site-resource/%d/users/remove", siteResourceID), body)
	return err
}

// --- Users ---

// UserRoleBinding is the slim role pair embedded in a User's roles
// list (returned by the org-scoped list endpoint).
type UserRoleBinding struct {
	RoleID   int    `json:"roleId"`
	RoleName string `json:"roleName"`
}

// User represents a Pangolin user.
//
// The list endpoint returns a richer payload than the older provider
// modeled — IDP linkage, 2FA flag, ownership marker, dateCreated,
// plus the embedded role bindings. New fields are all omitempty so
// older create/update responses (which return a subset) keep
// unmarshalling cleanly.
type User struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`

	// List-view extras (omitempty so legacy responses stay clean)
	EmailVerified    bool              `json:"emailVerified,omitempty"`
	DateCreated      string            `json:"dateCreated,omitempty"`
	OrgID            string            `json:"orgId,omitempty"`
	Name             *string           `json:"name,omitempty"`
	Type             string            `json:"type,omitempty"`
	IsOwner          bool              `json:"isOwner,omitempty"`
	IdpName          string            `json:"idpName,omitempty"`
	IdpID            int               `json:"idpId,omitempty"`
	IdpType          string            `json:"idpType,omitempty"`
	IdpVariant       string            `json:"idpVariant,omitempty"`
	TwoFactorEnabled bool              `json:"twoFactorEnabled,omitempty"`
	Roles            []UserRoleBinding `json:"roles,omitempty"`
}

// UserByUsernameResponse mirrors the user-by-username payload, which
// uses `userId` (not `id`) as the primary key field.
type UserByUsernameResponse struct {
	UserID           string            `json:"userId"`
	OrgID            string            `json:"orgId"`
	Email            *string           `json:"email"`
	Username         string            `json:"username"`
	Name             *string           `json:"name"`
	Type             string            `json:"type"`
	IsOwner          bool              `json:"isOwner"`
	TwoFactorEnabled bool              `json:"twoFactorEnabled"`
	Roles            []UserRoleBinding `json:"roles,omitempty"`
}

// GetUserByUsername looks up a user by their username within an
// organization. The Pangolin API requires the IDP ID alongside the
// username — usernames are unique only within an IDP, not globally.
func (c *Client) GetUserByUsername(ctx context.Context, orgID, username string, idpID int) (*UserByUsernameResponse, error) {
	values := url.Values{}
	values.Set("username", username)
	values.Set("idpId", fmt.Sprintf("%d", idpID))
	path := fmt.Sprintf("/org/%s/user-by-username?%s", orgID, values.Encode())
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var out UserByUsernameResponse
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse user-by-username: %w", err)
	}
	return &out, nil
}

// UsersResponse wraps the users list response.
type UsersResponse struct {
	Users []User `json:"users"`
}

// ListUsers retrieves all users for the organization.
func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/users", c.OrgID), nil)
	if err != nil {
		return nil, err
	}

	var usersResp UsersResponse
	if err := json.Unmarshal(resp.Data, &usersResp); err != nil {
		return nil, fmt.Errorf("failed to parse users: %w", err)
	}
	return usersResp.Users, nil
}

// --- Update operations ---

// UpdateSiteRequest is the payload for updating a site.
type UpdateSiteRequest struct {
	Name                string `json:"name"`
	DockerSocketEnabled bool   `json:"dockerSocketEnabled"`
}

// UpdateSite updates a site by ID.
func (c *Client) UpdateSite(ctx context.Context, siteID int, req *UpdateSiteRequest) (*Site, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/site/%d", siteID), req)
	if err != nil {
		return nil, err
	}
	var site Site
	if err := json.Unmarshal(resp.Data, &site); err != nil {
		return nil, fmt.Errorf("failed to parse site: %w", err)
	}
	return &site, nil
}

// UpdateResourceRequest is the payload for updating an HTTP resource.
type UpdateResourceRequest struct {
	Name                  string  `json:"name"`
	Subdomain             *string `json:"subdomain,omitempty"`
	SSO                   *bool   `json:"sso,omitempty"`
	SSL                   *bool   `json:"ssl,omitempty"`
	Enabled               *bool   `json:"enabled,omitempty"`
	BlockAccess           *bool   `json:"blockAccess,omitempty"`
	EmailWhitelistEnabled *bool   `json:"emailWhitelistEnabled,omitempty"`
	ApplyRules            *bool   `json:"applyRules,omitempty"`
	StickySession         *bool   `json:"stickySession,omitempty"`
	TLSServerName         *string `json:"tlsServerName,omitempty"`
}

// UpdateResource updates an HTTP resource by ID.
func (c *Client) UpdateResource(ctx context.Context, resourceID int, req *UpdateResourceRequest) (*Resource, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d", resourceID), req)
	if err != nil {
		return nil, err
	}
	var resource Resource
	if err := json.Unmarshal(resp.Data, &resource); err != nil {
		return nil, fmt.Errorf("failed to parse resource: %w", err)
	}
	return &resource, nil
}

// UpdateSiteResourceRequest is the payload for updating a private site resource.
type UpdateSiteResourceRequest struct {
	Name           string   `json:"name"`
	SiteID         int      `json:"siteId"`
	Destination    string   `json:"destination"`
	Alias          string   `json:"alias,omitempty"`
	TCPPortRange   string   `json:"tcpPortRangeString,omitempty"`
	UDPPortRange   string   `json:"udpPortRangeString,omitempty"`
	DisableICMP    bool     `json:"disableIcmp,omitempty"`
	AuthDaemonMode string   `json:"authDaemonMode,omitempty"`
	RoleIDs        []int    `json:"roleIds"`
	UserIDs        []string `json:"userIds"`
	ClientIDs      []int    `json:"clientIds"`
}

// UpdateSiteResource updates a private site resource by ID.
func (c *Client) UpdateSiteResource(ctx context.Context, siteResourceID int, req *UpdateSiteResourceRequest) (*SiteResource, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/site-resource/%d", siteResourceID), req)
	if err != nil {
		return nil, err
	}
	var siteResource SiteResource
	if err := json.Unmarshal(resp.Data, &siteResource); err != nil {
		return nil, fmt.Errorf("failed to parse site resource: %w", err)
	}
	return &siteResource, nil
}

// --- Roles CRUD ---

// CreateRoleRequest is the payload for creating a role. SSH bastion
// fields are optional — pointer types so that omitempty drops them when
// the caller does not set them (preserving the server default behavior).
type CreateRoleRequest struct {
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	RequireDeviceApproval *bool    `json:"requireDeviceApproval,omitempty"`
	AllowSSH              *bool    `json:"allowSsh,omitempty"`
	SSHSudoMode           *string  `json:"sshSudoMode,omitempty"`
	SSHSudoCommands       []string `json:"sshSudoCommands,omitempty"`
	SSHCreateHomeDir      *bool    `json:"sshCreateHomeDir,omitempty"`
	SSHUnixGroups         []string `json:"sshUnixGroups,omitempty"`
}

// CreateRole creates a new role in the organization.
func (c *Client) CreateRole(ctx context.Context, req *CreateRoleRequest) (*Role, error) {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/org/%s/role", c.OrgID), req)
	if err != nil {
		return nil, err
	}
	var role Role
	if err := json.Unmarshal(resp.Data, &role); err != nil {
		return nil, fmt.Errorf("failed to parse role: %w", err)
	}
	return &role, nil
}

// GetRoleByID retrieves a role by ID (via list + filter, no individual Get endpoint).
func (c *Client) GetRoleByID(ctx context.Context, roleID int) (*Role, error) {
	roles, err := c.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	for _, role := range roles {
		if role.RoleID == roleID {
			r := role
			return &r, nil
		}
	}
	return nil, fmt.Errorf("role %d: %w", roleID, ErrNotFound)
}

// UpdateRoleRequest is the payload for updating a role. All SSH bastion
// fields are optional; pointer types let callers send the zero value
// (e.g. allow_ssh=false to revoke) without confusing it with "leave
// unchanged". A nil pointer omits the field via omitempty.
type UpdateRoleRequest struct {
	Name                  string   `json:"name,omitempty"`
	Description           string   `json:"description,omitempty"`
	RequireDeviceApproval *bool    `json:"requireDeviceApproval,omitempty"`
	AllowSSH              *bool    `json:"allowSsh,omitempty"`
	SSHSudoMode           *string  `json:"sshSudoMode,omitempty"`
	SSHSudoCommands       []string `json:"sshSudoCommands,omitempty"`
	SSHCreateHomeDir      *bool    `json:"sshCreateHomeDir,omitempty"`
	SSHUnixGroups         []string `json:"sshUnixGroups,omitempty"`
}

// UpdateRole updates a role by ID.
func (c *Client) UpdateRole(ctx context.Context, roleID int, req *UpdateRoleRequest) (*Role, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/role/%d", roleID), req)
	if err != nil {
		return nil, err
	}
	var role Role
	if err := json.Unmarshal(resp.Data, &role); err != nil {
		return nil, fmt.Errorf("failed to parse role: %w", err)
	}
	return &role, nil
}

// DeleteRole deletes a role by ID. The replacementRoleID is assigned to any users
// currently holding the deleted role (required by the Pangolin API).
func (c *Client) DeleteRole(ctx context.Context, roleID int, replacementRoleID int) error {
	body := map[string]string{"roleId": fmt.Sprintf("%d", replacementRoleID)}
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/role/%d", roleID), body)
	return err
}

// --- API Keys ---

// APIKey represents a Pangolin API key.
type APIKey struct {
	APIKeyID string `json:"apiKeyId"`
	Name     string `json:"name"`
	APIKey   string `json:"apiKey"` // Only returned on creation.
}

// APIKeysResponse wraps the API keys list response.
type APIKeysResponse struct {
	APIKeys []APIKey `json:"apiKeys"`
}

// CreateAPIKeyRequest is the payload for creating an API key.
type CreateAPIKeyRequest struct {
	Name string `json:"name"`
}

// CreateAPIKey creates a new API key for the organization.
func (c *Client) CreateAPIKey(ctx context.Context, req *CreateAPIKeyRequest) (*APIKey, error) {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/org/%s/api-key", c.OrgID), req)
	if err != nil {
		return nil, err
	}
	var apiKey APIKey
	if err := json.Unmarshal(resp.Data, &apiKey); err != nil {
		return nil, fmt.Errorf("failed to parse API key: %w", err)
	}
	return &apiKey, nil
}

// ListAPIKeys retrieves all API keys for the organization.
func (c *Client) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/api-keys", c.OrgID), nil)
	if err != nil {
		return nil, err
	}
	var keysResp APIKeysResponse
	if err := json.Unmarshal(resp.Data, &keysResp); err != nil {
		return nil, fmt.Errorf("failed to parse API keys: %w", err)
	}
	return keysResp.APIKeys, nil
}

// GetAPIKeyByID retrieves an API key by ID (via list + filter).
func (c *Client) GetAPIKeyByID(ctx context.Context, apiKeyID string) (*APIKey, error) {
	keys, err := c.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if key.APIKeyID == apiKeyID {
			k := key
			return &k, nil
		}
	}
	return nil, fmt.Errorf("API key %s: %w", apiKeyID, ErrNotFound)
}

// DeleteAPIKey deletes an API key by ID.
func (c *Client) DeleteAPIKey(ctx context.Context, apiKeyID string) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/org/%s/api-key/%s", c.OrgID, apiKeyID), nil)
	return err
}

// --- OLM Clients ---

// ClientDefaults represents the response from pick-client-defaults.
type ClientDefaults struct {
	OlmID     string `json:"olmId"`
	OlmSecret string `json:"olmSecret"`
	Subnet    string `json:"subnet"`
}

// GetClientDefaults picks client defaults for creating a new OLM client.
func (c *Client) GetClientDefaults(ctx context.Context) (*ClientDefaults, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/pick-client-defaults", c.OrgID), nil)
	if err != nil {
		return nil, err
	}
	var defaults ClientDefaults
	if err := json.Unmarshal(resp.Data, &defaults); err != nil {
		return nil, fmt.Errorf("failed to parse client defaults: %w", err)
	}
	return &defaults, nil
}

// OLMClient represents a Pangolin OLM (Overlay LAN Manager) client device.
type OLMClient struct {
	ClientID int    `json:"clientId"`
	NiceID   string `json:"niceId"`
	Name     string `json:"name"`
	Online   bool   `json:"online"`
	Secret   string `json:"secret"` // Only returned on creation.
}

// OLMClientsResponse wraps the clients list response.
type OLMClientsResponse struct {
	Clients []OLMClient `json:"clients"`
}

// CreateOLMClientRequest is the payload for creating an OLM client.
type CreateOLMClientRequest struct {
	Name   string `json:"name"`
	OlmID  string `json:"olmId"`
	Secret string `json:"secret"`
	Subnet string `json:"subnet"`
	Type   string `json:"type"`
}

// UpdateOLMClientRequest is the payload for updating an OLM client.
type UpdateOLMClientRequest struct {
	Name string `json:"name"`
}

// CreateOLMClient creates a new OLM client device.
func (c *Client) CreateOLMClient(ctx context.Context, req *CreateOLMClientRequest) (*OLMClient, error) {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/org/%s/client", c.OrgID), req)
	if err != nil {
		return nil, err
	}
	var client OLMClient
	if err := json.Unmarshal(resp.Data, &client); err != nil {
		return nil, fmt.Errorf("failed to parse OLM client: %w", err)
	}
	return &client, nil
}

// ListOLMClients retrieves all OLM clients for the organization.
func (c *Client) ListOLMClients(ctx context.Context) ([]OLMClient, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/clients", c.OrgID), nil)
	if err != nil {
		return nil, err
	}
	var clientsResp OLMClientsResponse
	if err := json.Unmarshal(resp.Data, &clientsResp); err != nil {
		return nil, fmt.Errorf("failed to parse OLM clients: %w", err)
	}
	return clientsResp.Clients, nil
}

// GetOLMClient retrieves an OLM client by ID.
func (c *Client) GetOLMClient(ctx context.Context, clientID int) (*OLMClient, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/client/%d", clientID), nil)
	if err != nil {
		return nil, err
	}
	var client OLMClient
	if err := json.Unmarshal(resp.Data, &client); err != nil {
		return nil, fmt.Errorf("failed to parse OLM client: %w", err)
	}
	return &client, nil
}

// UpdateOLMClient updates an OLM client by ID.
func (c *Client) UpdateOLMClient(ctx context.Context, clientID int, req *UpdateOLMClientRequest) (*OLMClient, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/client/%d", clientID), req)
	if err != nil {
		return nil, err
	}
	var client OLMClient
	if err := json.Unmarshal(resp.Data, &client); err != nil {
		return nil, fmt.Errorf("failed to parse OLM client: %w", err)
	}
	return &client, nil
}

// DeleteOLMClient deletes an OLM client by ID.
func (c *Client) DeleteOLMClient(ctx context.Context, clientID int) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/client/%d", clientID), nil)
	return err
}

// --- Whitelist ---

// AddWhitelistToResource adds an email to the whitelist of an HTTP resource.
func (c *Client) AddWhitelistToResource(ctx context.Context, resourceID int, email string) error {
	body := map[string]string{"email": email}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/whitelist/add", resourceID), body)
	return err
}

// RemoveWhitelistFromResource removes an email from the whitelist of an HTTP resource.
func (c *Client) RemoveWhitelistFromResource(ctx context.Context, resourceID int, email string) error {
	body := map[string]string{"email": email}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/whitelist/remove", resourceID), body)
	return err
}

// ListResourceWhitelist returns the email whitelist currently
// configured on an HTTP resource. The response shape is `{whitelist:
// [...]}` — the items are either plain email strings or `{email}`
// objects depending on the server build, so the unmarshaler accepts
// both forms and normalizes to a `[]string` of emails.
//
// Returns an empty slice when the resource has no whitelist
// configured or when email_whitelist_enabled is off. The 400 error
// "Email whitelist is not enabled for this resource" only fires on
// add/remove, not on the GET — confirmed live.
func (c *Client) ListResourceWhitelist(ctx context.Context, resourceID int) ([]string, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/resource/%d/whitelist", resourceID), nil)
	if err != nil {
		return nil, err
	}
	// Accept both `{whitelist: ["a@x", "b@x"]}` and
	// `{whitelist: [{email: "a@x"}, {email: "b@x"}]}` shapes by
	// unmarshalling each item as a json.RawMessage and probing
	// whether it is a string or an object.
	var wrapper struct {
		Whitelist []json.RawMessage `json:"whitelist"`
	}
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse resource whitelist: %w", err)
	}
	out := make([]string, 0, len(wrapper.Whitelist))
	for i, raw := range wrapper.Whitelist {
		if len(raw) == 0 {
			continue
		}
		if raw[0] == '"' {
			// String form
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, fmt.Errorf("whitelist[%d]: %w", i, err)
			}
			out = append(out, s)
			continue
		}
		// Object form
		var obj struct {
			Email string `json:"email"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, fmt.Errorf("whitelist[%d]: %w", i, err)
		}
		out = append(out, obj.Email)
	}
	return out, nil
}

// SetResourceRoles replaces the non-admin role bindings of an HTTP
// resource with the given set. Quirk observed live: the built-in
// Admin role is mandatory on every resource — the API rejects any
// roleIds list that *includes* role 1 ("Admin role cannot be
// assigned to resources") and silently keeps Admin attached when
// the list does *not* mention it. In practice, callers should pass
// only the additional roles they want bound; Admin stays put.
func (c *Client) SetResourceRoles(ctx context.Context, resourceID int, roleIDs []int) error {
	if roleIDs == nil {
		roleIDs = []int{}
	}
	body := map[string]any{"roleIds": roleIDs}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/roles", resourceID), body)
	return err
}

// SetResourceUsers replaces the user bindings of an HTTP resource
// with the given set. Pass an empty slice to clear all per-user
// bindings (role-based access still applies).
func (c *Client) SetResourceUsers(ctx context.Context, resourceID int, userIDs []string) error {
	if userIDs == nil {
		userIDs = []string{}
	}
	body := map[string]any{"userIds": userIDs}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/users", resourceID), body)
	return err
}

// SetResourceWhitelist replaces the email whitelist of an HTTP
// resource with the given set. The resource must have
// email_whitelist_enabled = true for the call to succeed; otherwise
// the server responds with 400 "Email whitelist is not enabled for
// this resource".
func (c *Client) SetResourceWhitelist(ctx context.Context, resourceID int, emails []string) error {
	if emails == nil {
		emails = []string{}
	}
	body := map[string]any{"emails": emails}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/whitelist", resourceID), body)
	return err
}

// --- Client assignments for site resources ---

// AddClientToSiteResource assigns an OLM client to a private site resource.
func (c *Client) AddClientToSiteResource(ctx context.Context, siteResourceID, clientID int) error {
	body := map[string]int{"clientId": clientID}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/site-resource/%d/clients/add", siteResourceID), body)
	return err
}

// RemoveClientFromSiteResource removes an OLM client from a private site resource.
func (c *Client) RemoveClientFromSiteResource(ctx context.Context, siteResourceID, clientID int) error {
	body := map[string]int{"clientId": clientID}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/site-resource/%d/clients/remove", siteResourceID), body)
	return err
}

// --- List operations ---

// SitesResponse wraps the sites list response.
type SitesResponse struct {
	Sites []Site `json:"sites"`
}

// ListSites retrieves all sites for the organization.
func (c *Client) ListSites(ctx context.Context) ([]Site, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/sites", c.OrgID), nil)
	if err != nil {
		return nil, err
	}
	var sitesResp SitesResponse
	if err := json.Unmarshal(resp.Data, &sitesResp); err != nil {
		return nil, fmt.Errorf("failed to parse sites: %w", err)
	}
	return sitesResp.Sites, nil
}

// ResourcesResponse wraps the resources list response.
type ResourcesResponse struct {
	Resources []Resource `json:"resources"`
}

// ListResources retrieves all HTTP resources for the organization.
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/resources", c.OrgID), nil)
	if err != nil {
		return nil, err
	}
	var resourcesResp ResourcesResponse
	if err := json.Unmarshal(resp.Data, &resourcesResp); err != nil {
		return nil, fmt.Errorf("failed to parse resources: %w", err)
	}
	return resourcesResp.Resources, nil
}

// SiteResourcesListResponse wraps the site resources list response.
type SiteResourcesListResponse struct {
	SiteResources []SiteResource `json:"siteResources"`
}

// ListSiteResources retrieves all private site resources for the organization.
func (c *Client) ListSiteResources(ctx context.Context) ([]SiteResource, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/site-resources", c.OrgID), nil)
	if err != nil {
		return nil, err
	}
	var siteResourcesResp SiteResourcesListResponse
	if err := json.Unmarshal(resp.Data, &siteResourcesResp); err != nil {
		return nil, fmt.Errorf("failed to parse site resources: %w", err)
	}
	return siteResourcesResp.SiteResources, nil
}

// --- Organizations ---

// Org represents a Pangolin organization.
type Org struct {
	OrgID         string `json:"orgId"`
	Name          string `json:"name"`
	Subnet        string `json:"subnet"`
	UtilitySubnet string `json:"utilitySubnet"`
	CreatedAt     string `json:"createdAt,omitempty"`

	// Security policies — nullable upstream (null = unset / inherit).
	RequireTwoFactor      *bool `json:"requireTwoFactor"`
	MaxSessionLengthHours *int  `json:"maxSessionLengthHours"`
	PasswordExpiryDays    *int  `json:"passwordExpiryDays"`

	// Audit log retention (days). 0 means disabled for that log stream.
	SettingsLogRetentionDaysRequest    int `json:"settingsLogRetentionDaysRequest"`
	SettingsLogRetentionDaysAccess     int `json:"settingsLogRetentionDaysAccess"`
	SettingsLogRetentionDaysAction     int `json:"settingsLogRetentionDaysAction"`
	SettingsLogRetentionDaysConnection int `json:"settingsLogRetentionDaysConnection"`

	// SSH CA — used to sign certificates for SSH bastion access. The
	// private key is returned in clear by the API; treat as secret.
	SSHCaPrivateKey string `json:"sshCaPrivateKey,omitempty"`
	SSHCaPublicKey  string `json:"sshCaPublicKey,omitempty"`

	// Billing — IsBillingOrg=true means this org carries the billing
	// account. BillingOrgID points at the billing org (often itself).
	IsBillingOrg bool   `json:"isBillingOrg,omitempty"`
	BillingOrgID string `json:"billingOrgId,omitempty"`
}

// CreateOrgRequest is the payload for creating an organization.
type CreateOrgRequest struct {
	OrgID         string `json:"orgId"`
	Name          string `json:"name"`
	Subnet        string `json:"subnet"`
	UtilitySubnet string `json:"utilitySubnet"`
}

// CreateOrg creates a new organization.
func (c *Client) CreateOrg(ctx context.Context, req *CreateOrgRequest) (*Org, error) {
	resp, err := c.doRequest(ctx, "PUT", "/org", req)
	if err != nil {
		return nil, err
	}
	var org Org
	if err := json.Unmarshal(resp.Data, &org); err != nil {
		return nil, fmt.Errorf("failed to parse org: %w", err)
	}
	return &org, nil
}

// GetOrg retrieves an organization by ID.
func (c *Client) GetOrg(ctx context.Context, orgID string) (*Org, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s", orgID), nil)
	if err != nil {
		return nil, err
	}
	// Response is wrapped: {"org": {...}}
	var wrapper struct {
		Org Org `json:"org"`
	}
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse org: %w", err)
	}
	return &wrapper.Org, nil
}

// UpdateOrgRequest is the payload for updating an organization. All
// fields are optional; pointer types let callers send a zero value
// (e.g. log retention = 0 to disable) without confusing it with "leave
// unchanged". A nil pointer omits the field from the body via
// omitempty.
type UpdateOrgRequest struct {
	Name                               string `json:"name,omitempty"`
	RequireTwoFactor                   *bool  `json:"requireTwoFactor,omitempty"`
	MaxSessionLengthHours              *int   `json:"maxSessionLengthHours,omitempty"`
	PasswordExpiryDays                 *int   `json:"passwordExpiryDays,omitempty"`
	SettingsLogRetentionDaysRequest    *int   `json:"settingsLogRetentionDaysRequest,omitempty"`
	SettingsLogRetentionDaysAccess     *int   `json:"settingsLogRetentionDaysAccess,omitempty"`
	SettingsLogRetentionDaysAction     *int   `json:"settingsLogRetentionDaysAction,omitempty"`
	SettingsLogRetentionDaysConnection *int   `json:"settingsLogRetentionDaysConnection,omitempty"`
}

// UpdateOrg updates an organization by ID.
func (c *Client) UpdateOrg(ctx context.Context, orgID string, req *UpdateOrgRequest) (*Org, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/org/%s", orgID), req)
	if err != nil {
		return nil, err
	}
	var org Org
	if err := json.Unmarshal(resp.Data, &org); err != nil {
		return nil, fmt.Errorf("failed to parse org: %w", err)
	}
	return &org, nil
}

// DeleteOrg deletes an organization by ID.
func (c *Client) DeleteOrg(ctx context.Context, orgID string) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/org/%s", orgID), nil)
	return err
}

// --- User CRUD ---

// CreateUserRequest is the payload for creating a user in an organization.
type CreateUserRequest struct {
	Username string `json:"username"`
	RoleID   int    `json:"roleId"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
	IdpID    int    `json:"idpId,omitempty"`
}

// UpdateUserRequest is the payload for updating a user.
type UpdateUserRequest struct {
	AutoProvisioned bool `json:"autoProvisioned"`
}

// CreateUser creates a new user in the organization.
func (c *Client) CreateUser(ctx context.Context, req *CreateUserRequest) (*User, error) {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/org/%s/user", c.OrgID), req)
	if err != nil {
		return nil, err
	}
	var result struct {
		User User `json:"user"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		// Try direct unmarshal
		var user User
		if err2 := json.Unmarshal(resp.Data, &user); err2 != nil {
			return nil, fmt.Errorf("failed to parse user: %w", err)
		}
		return &user, nil
	}
	return &result.User, nil
}

// GetUser retrieves a user by ID.
func (c *Client) GetUser(ctx context.Context, userID string) (*User, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/user/%s", c.OrgID, userID), nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		User User `json:"user"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		var user User
		if err2 := json.Unmarshal(resp.Data, &user); err2 != nil {
			return nil, fmt.Errorf("failed to parse user: %w", err)
		}
		return &user, nil
	}
	return &result.User, nil
}

// UpdateUser updates a user's auto-provisioned status.
func (c *Client) UpdateUser(ctx context.Context, userID string, req *UpdateUserRequest) (*User, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/org/%s/user/%s", c.OrgID, userID), req)
	if err != nil {
		return nil, err
	}
	var result struct {
		User User `json:"user"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		var user User
		if err2 := json.Unmarshal(resp.Data, &user); err2 != nil {
			return nil, fmt.Errorf("failed to parse user: %w", err)
		}
		return &user, nil
	}
	return &result.User, nil
}

// DeleteUser removes a user from the organization.
func (c *Client) DeleteUser(ctx context.Context, userID string) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/org/%s/user/%s", c.OrgID, userID), nil)
	return err
}

// --- IDP ---

// IDP represents a Pangolin Identity Provider.
//
// Variant refines Type for OIDC providers — observed values are
// "oidc" (generic), "google", "azure". The Pangolin UI uses the
// variant to pre-fill provider-specific URLs and tweak the consent
// flow; downstream consumers can branch on it to render different
// help text.
//
// OrgCount comes from the API as a JSON-encoded string (e.g. "0")
// in the LIST response — kept as string here to match the wire.
type IDP struct {
	IDPId              int    `json:"idpId"`
	Name               string `json:"name"`
	Type               string `json:"type"`
	Variant            string `json:"variant,omitempty"`
	AutoProvision      bool   `json:"autoProvision"`
	Tags               string `json:"tags"`
	OrgCount           string `json:"orgCount,omitempty"`
	DefaultRoleMapping string `json:"defaultRoleMapping,omitempty"`
	DefaultOrgMapping  string `json:"defaultOrgMapping,omitempty"`
}

// IDPOidcConfig represents the OIDC configuration of an IDP.
type IDPOidcConfig struct {
	ClientID       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	AuthURL        string `json:"authUrl"`
	TokenURL       string `json:"tokenUrl"`
	IdentifierPath string `json:"identifierPath"`
	EmailPath      string `json:"emailPath"`
	NamePath       string `json:"namePath"`
	Scopes         string `json:"scopes"`
}

// CreateIDPRequest is the payload for creating an OIDC IDP.
// Variant is optional — defaults to "oidc" server-side when omitted.
type CreateIDPRequest struct {
	Name           string `json:"name"`
	ClientID       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	AuthURL        string `json:"authUrl"`
	TokenURL       string `json:"tokenUrl"`
	IdentifierPath string `json:"identifierPath"`
	EmailPath      string `json:"emailPath,omitempty"`
	NamePath       string `json:"namePath,omitempty"`
	Scopes         string `json:"scopes"`
	AutoProvision  bool   `json:"autoProvision,omitempty"`
	Tags           string `json:"tags,omitempty"`
	Variant        string `json:"variant,omitempty"`
}

// UpdateIDPRequest is the payload for updating an OIDC IDP.
type UpdateIDPRequest struct {
	Name           string `json:"name,omitempty"`
	ClientID       string `json:"clientId,omitempty"`
	ClientSecret   string `json:"clientSecret,omitempty"`
	AuthURL        string `json:"authUrl,omitempty"`
	TokenURL       string `json:"tokenUrl,omitempty"`
	IdentifierPath string `json:"identifierPath,omitempty"`
	EmailPath      string `json:"emailPath,omitempty"`
	NamePath       string `json:"namePath,omitempty"`
	Scopes         string `json:"scopes,omitempty"`
	AutoProvision  bool   `json:"autoProvision,omitempty"`
	Tags           string `json:"tags,omitempty"`
	Variant        string `json:"variant,omitempty"`
}

// CreateIDPResponse is the response from creating an IDP.
type CreateIDPResponse struct {
	IDPId       int    `json:"idpId"`
	RedirectURL string `json:"redirectUrl"`
}

// CreateIDP creates a new OIDC IDP.
func (c *Client) CreateIDP(ctx context.Context, req *CreateIDPRequest) (*CreateIDPResponse, error) {
	resp, err := c.doRequest(ctx, "PUT", "/idp/oidc", req)
	if err != nil {
		return nil, err
	}
	var result CreateIDPResponse
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse IDP response: %w", err)
	}
	return &result, nil
}

// GetIDP retrieves an IDP by ID.
func (c *Client) GetIDP(ctx context.Context, idpID int) (*IDP, *IDPOidcConfig, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/idp/%d", idpID), nil)
	if err != nil {
		return nil, nil, err
	}
	var result struct {
		IDP           IDP           `json:"idp"`
		IDPOidcConfig IDPOidcConfig `json:"idpOidcConfig"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, nil, fmt.Errorf("failed to parse IDP: %w", err)
	}
	return &result.IDP, &result.IDPOidcConfig, nil
}

// UpdateIDP updates an OIDC IDP.
func (c *Client) UpdateIDP(ctx context.Context, idpID int, req *UpdateIDPRequest) error {
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/idp/%d/oidc", idpID), req)
	return err
}

// DeleteIDP deletes an IDP.
func (c *Client) DeleteIDP(ctx context.Context, idpID int) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/idp/%d", idpID), nil)
	return err
}

// ListIDPs retrieves all IDPs in the system.
func (c *Client) ListIDPs(ctx context.Context) ([]IDP, error) {
	resp, err := c.doRequest(ctx, "GET", "/idp", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		IDPs []IDP `json:"idps"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse IDPs: %w", err)
	}
	return result.IDPs, nil
}

// IDPOrgPolicy represents an IDP org mapping policy.
type IDPOrgPolicy struct {
	IDPId       int    `json:"idpId"`
	OrgID       string `json:"orgId"`
	RoleMapping string `json:"roleMapping"`
	OrgMapping  string `json:"orgMapping"`
}

// SetIDPOrgPolicyRequest is the payload for creating/updating an IDP org policy.
type SetIDPOrgPolicyRequest struct {
	RoleMapping string `json:"roleMapping,omitempty"`
	OrgMapping  string `json:"orgMapping,omitempty"`
}

// CreateIDPOrgPolicy creates an IDP policy for an org.
func (c *Client) CreateIDPOrgPolicy(ctx context.Context, idpID int, orgID string, req *SetIDPOrgPolicyRequest) error {
	_, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/idp/%d/org/%s", idpID, orgID), req)
	return err
}

// UpdateIDPOrgPolicy updates an IDP policy for an org.
func (c *Client) UpdateIDPOrgPolicy(ctx context.Context, idpID int, orgID string, req *SetIDPOrgPolicyRequest) error {
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/idp/%d/org/%s", idpID, orgID), req)
	return err
}

// DeleteIDPOrgPolicy removes an IDP policy for an org.
func (c *Client) DeleteIDPOrgPolicy(ctx context.Context, idpID int, orgID string) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/idp/%d/org/%s", idpID, orgID), nil)
	return err
}

// GetIDPOrgPolicy retrieves the IDP policy for a specific org (via list + filter).
func (c *Client) GetIDPOrgPolicy(ctx context.Context, idpID int, orgID string) (*IDPOrgPolicy, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/idp/%d/org", idpID), nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Policies []IDPOrgPolicy `json:"policies"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse IDP org policies: %w", err)
	}
	for _, p := range result.Policies {
		if p.OrgID == orgID {
			policy := p
			return &policy, nil
		}
	}
	return nil, fmt.Errorf("IDP org policy for org %s: %w", orgID, ErrNotFound)
}

// --- Domain ---

// GetDomainByID retrieves a domain by ID (via list + filter). Kept
// for callers that still use the list-based lookup.
func (c *Client) GetDomainByID(ctx context.Context, domainID string) (*Domain, error) {
	domains, err := c.ListDomains(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range domains {
		if d.DomainID == domainID {
			domain := d
			return &domain, nil
		}
	}
	return nil, fmt.Errorf("domain %s: %w", domainID, ErrNotFound)
}

// GetDomain retrieves a domain by ID via the per-id endpoint
// (GET /org/{org}/domain/{id}) — preferred over GetDomainByID since
// it avoids fetching the full list.
func (c *Client) GetDomain(ctx context.Context, orgID, domainID string) (*Domain, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/domain/%s", orgID, domainID), nil)
	if err != nil {
		return nil, err
	}
	var domain Domain
	if err := json.Unmarshal(resp.Data, &domain); err != nil {
		return nil, fmt.Errorf("failed to parse domain: %w", err)
	}
	return &domain, nil
}

// ListDomainDNSRecords retrieves the DNS records configured for a
// domain. Returned in declaration order from the server.
func (c *Client) ListDomainDNSRecords(ctx context.Context, orgID, domainID string) ([]DomainDNSRecord, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/domain/%s/dns-records", orgID, domainID), nil)
	if err != nil {
		return nil, err
	}
	var records []DomainDNSRecord
	if err := json.Unmarshal(resp.Data, &records); err != nil {
		return nil, fmt.Errorf("failed to parse DNS records: %w", err)
	}
	return records, nil
}

// --- Resource Rules ---

// ResourceRule represents an access control rule for a resource.
type ResourceRule struct {
	RuleID     int    `json:"ruleId"`
	ResourceID int    `json:"resourceId"`
	Action     string `json:"action"`
	Match      string `json:"match"`
	Value      string `json:"value"`
	Priority   int    `json:"priority"`
	Enabled    bool   `json:"enabled"`
}

// SetResourceRuleRequest is the payload for creating or updating a resource rule.
type SetResourceRuleRequest struct {
	Action   string `json:"action"`
	Match    string `json:"match"`
	Value    string `json:"value"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
}

// CreateResourceRule creates a new rule for a resource.
func (c *Client) CreateResourceRule(ctx context.Context, resourceID int, req *SetResourceRuleRequest) (*ResourceRule, error) {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/resource/%d/rule", resourceID), req)
	if err != nil {
		return nil, err
	}
	var rule ResourceRule
	if err := json.Unmarshal(resp.Data, &rule); err != nil {
		return nil, fmt.Errorf("failed to parse resource rule: %w", err)
	}
	return &rule, nil
}

// GetResourceRule retrieves a resource rule by ID (via list + filter).
func (c *Client) GetResourceRule(ctx context.Context, resourceID, ruleID int) (*ResourceRule, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/resource/%d/rules", resourceID), nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Rules []ResourceRule `json:"rules"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse resource rules: %w", err)
	}
	for _, r := range result.Rules {
		if r.RuleID == ruleID {
			rule := r
			return &rule, nil
		}
	}
	return nil, fmt.Errorf("resource rule %d: %w", ruleID, ErrNotFound)
}

// UpdateResourceRule updates an existing resource rule.
func (c *Client) UpdateResourceRule(ctx context.Context, resourceID, ruleID int, req *SetResourceRuleRequest) (*ResourceRule, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/rule/%d", resourceID, ruleID), req)
	if err != nil {
		return nil, err
	}
	var rule ResourceRule
	if err := json.Unmarshal(resp.Data, &rule); err != nil {
		return nil, fmt.Errorf("failed to parse resource rule: %w", err)
	}
	return &rule, nil
}

// DeleteResourceRule deletes a resource rule.
func (c *Client) DeleteResourceRule(ctx context.Context, resourceID, ruleID int) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/resource/%d/rule/%d", resourceID, ruleID), nil)
	return err
}

// --- Resource Auth ---

// ResourceAuthState holds the auth IDs for a resource (from list endpoint).
type ResourceAuthState struct {
	PasswordID   *int `json:"passwordId"`
	PincodeID    *int `json:"pincodeId"`
	HeaderAuthID *int `json:"headerAuthId"`
}

// ResourceListItem is the minimal shape returned by the resources list endpoint.
type ResourceListItem struct {
	ResourceID   int  `json:"resourceId"`
	PasswordID   *int `json:"passwordId"`
	PincodeID    *int `json:"pincodeId"`
	HeaderAuthID *int `json:"headerAuthId"`
}

// GetResourceAuthState returns the auth IDs for a resource via list + filter.
func (c *Client) GetResourceAuthState(ctx context.Context, resourceID int) (*ResourceAuthState, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/resources", c.OrgID), nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Resources []ResourceListItem `json:"resources"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse resources: %w", err)
	}
	for _, r := range result.Resources {
		if r.ResourceID == resourceID {
			return &ResourceAuthState{
				PasswordID:   r.PasswordID,
				PincodeID:    r.PincodeID,
				HeaderAuthID: r.HeaderAuthID,
			}, nil
		}
	}
	return nil, fmt.Errorf("resource %d: %w", resourceID, ErrNotFound)
}

// SetResourcePassword sets or clears the password for a resource.
// Pass nil to remove the password.
func (c *Client) SetResourcePassword(ctx context.Context, resourceID int, password *string) error {
	body := map[string]interface{}{"password": password}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/password", resourceID), body)
	return err
}

// SetResourcePincode sets or clears the pincode for a resource.
// Pass nil to remove the pincode.
func (c *Client) SetResourcePincode(ctx context.Context, resourceID int, pincode *string) error {
	body := map[string]interface{}{"pincode": pincode}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/pincode", resourceID), body)
	return err
}

// SetResourceHeaderAuthRequest is the payload for setting header auth.
type SetResourceHeaderAuthRequest struct {
	Password              *string `json:"password"`
	User                  *string `json:"user"`
	ExtendedCompatibility bool    `json:"extendedCompatibility"`
}

// SetResourceHeaderAuth sets or clears the header authentication for a resource.
func (c *Client) SetResourceHeaderAuth(ctx context.Context, resourceID int, req *SetResourceHeaderAuthRequest) error {
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/header-auth", resourceID), req)
	return err
}

// --- Audit Logs ---

// RequestLogQuery filters a request audit log query. All fields are optional;
// zero values are not sent. Timestamps must be ISO 8601 / RFC 3339 strings.
type RequestLogQuery struct {
	TimeStart  string
	TimeEnd    string
	Action     string
	Method     string
	Reason     string
	ResourceID string
	Actor      string
	Location   string
	Host       string
	Path       string
	Limit      string
	Offset     string
}

// RequestLogPagination is the pagination wrapper returned by the logs API.
type RequestLogPagination struct {
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// RequestLogResourceRef is a tiny {id, name} object embedded in the
// FilterAttributes.Resources slice — the server returns resource
// references as objects, not bare IDs.
type RequestLogResourceRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// RequestLogFilterAttributes lists the distinct values seen in the result
// set for each filterable dimension. Useful to populate UIs with allowed
// values for refining a query.
type RequestLogFilterAttributes struct {
	Actors    []string                `json:"actors"`
	Resources []RequestLogResourceRef `json:"resources"`
	Locations []string                `json:"locations"`
	Hosts     []string                `json:"hosts"`
	Paths     []string                `json:"paths"`
}

// RequestLogEntry is one request audit log line as actually returned by
// GET /org/{org}/logs/request. The Pangolin OpenAPI spec does not
// publish a response schema, so this shape was captured by sampling
// live traffic against the enterprise self-host. Fields that the
// server can return null (actor*, userAgent, siteResourceId, metadata,
// headers, query) are modeled as pointer types so callers can
// distinguish "no value" from a zero / empty value.
type RequestLogEntry struct {
	ID                 int64           `json:"id"`
	Timestamp          int64           `json:"timestamp"`
	OrgID              string          `json:"orgId,omitempty"`
	Action             bool            `json:"action"`
	Reason             int64           `json:"reason"`
	ActorType          *string         `json:"actorType"`
	Actor              *string         `json:"actor"`
	ActorID            *string         `json:"actorId"`
	ResourceID         int64           `json:"resourceId"`
	SiteResourceID     *int64          `json:"siteResourceId"`
	IP                 string          `json:"ip,omitempty"`
	Location           string          `json:"location,omitempty"`
	UserAgent          *string         `json:"userAgent"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
	Headers            json.RawMessage `json:"headers,omitempty"`
	Query              json.RawMessage `json:"query,omitempty"`
	OriginalRequestURL string          `json:"originalRequestURL,omitempty"`
	Scheme             string          `json:"scheme,omitempty"`
	Host               string          `json:"host,omitempty"`
	Path               string          `json:"path,omitempty"`
	Method             string          `json:"method,omitempty"`
	TLS                bool            `json:"tls"`
	ResourceName       string          `json:"resourceName,omitempty"`
	ResourceNiceID     string          `json:"resourceNiceId,omitempty"`

	// Raw is the full JSON of the entry as received from the server,
	// preserved so consumers can still reach any field added by the
	// server after this struct was written.
	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON captures the full raw entry in Raw before unpacking the
// known fields. This keeps unknown attributes available to downstream
// consumers without forcing us to model the whole shape.
func (e *RequestLogEntry) UnmarshalJSON(data []byte) error {
	e.Raw = append(e.Raw[:0], data...)
	type alias RequestLogEntry
	return json.Unmarshal(data, (*alias)(e))
}

// RequestLogResponse is the full response body returned by GET
// /org/{org}/logs/request.
type RequestLogResponse struct {
	Log              []RequestLogEntry          `json:"log"`
	Pagination       RequestLogPagination       `json:"pagination"`
	FilterAttributes RequestLogFilterAttributes `json:"filterAttributes"`
}

// ListRequestLogs queries the request audit log for an organization. Returns
// the matching entries plus pagination and filter dimension metadata.
//
// This endpoint requires an active Pangolin Cloud subscription for the
// access/action/connection log variants; the request log itself is
// available on the free tier as well.
func (c *Client) ListRequestLogs(ctx context.Context, orgID string, q RequestLogQuery) (*RequestLogResponse, error) {
	path := fmt.Sprintf("/org/%s/logs/request", orgID)
	if qs := buildRequestLogQuery(q); qs != "" {
		path += "?" + qs
	}
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var out RequestLogResponse
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse request logs: %w", err)
	}
	return &out, nil
}

// buildRequestLogQuery encodes the non-empty fields of a RequestLogQuery
// into a URL query string. Returned string does not include the leading
// '?'. Order is stable (struct field order) so tests can assert.
func buildRequestLogQuery(q RequestLogQuery) string {
	values := url.Values{}
	for k, v := range map[string]string{
		"timeStart":  q.TimeStart,
		"timeEnd":    q.TimeEnd,
		"action":     q.Action,
		"method":     q.Method,
		"reason":     q.Reason,
		"resourceId": q.ResourceID,
		"actor":      q.Actor,
		"location":   q.Location,
		"host":       q.Host,
		"path":       q.Path,
		"limit":      q.Limit,
		"offset":     q.Offset,
	} {
		if v != "" {
			values.Set(k, v)
		}
	}
	return values.Encode()
}

// --- Invitations ---

// InviteRole is one role binding embedded inside an Invitation list item.
type InviteRole struct {
	RoleID   int    `json:"roleId"`
	RoleName string `json:"roleName"`
}

// Invitation is a pending org invite as returned by
// GET /org/{org}/invitations.
type Invitation struct {
	InviteID  string       `json:"inviteId"`
	Email     string       `json:"email"`
	ExpiresAt int64        `json:"expiresAt"`
	Roles     []InviteRole `json:"roles"`
}

// CreateInviteRequest is the payload for POST /org/{org}/create-invite.
//
// Use either RoleID (single role) or RoleIDs (multiple roles). In
// observed responses the upstream accepts both shapes; current
// practice is to send a single RoleID.
type CreateInviteRequest struct {
	Email      string `json:"email"`
	RoleID     int    `json:"roleId,omitempty"`
	RoleIDs    []int  `json:"roleIds,omitempty"`
	ValidHours int    `json:"validHours,omitempty"`
	SendEmail  bool   `json:"sendEmail"`
	Regenerate bool   `json:"regenerate,omitempty"`
}

// CreateInviteResponse is what POST create-invite returns. Note the
// API does not echo the InviteID directly; the ID is the first segment
// of the token in InviteLink (before the '-'). Callers needing the ID
// should list invitations and match on email.
type CreateInviteResponse struct {
	InviteLink string `json:"inviteLink"`
	ExpiresAt  int64  `json:"expiresAt"`
}

// CreateInvite invites a user to join an organization.
func (c *Client) CreateInvite(ctx context.Context, orgID string, req *CreateInviteRequest) (*CreateInviteResponse, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/org/%s/create-invite", orgID), req)
	if err != nil {
		return nil, err
	}
	var out CreateInviteResponse
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse invite response: %w", err)
	}
	return &out, nil
}

// ListInvitations returns all open invitations of an organization.
// The API returns pagination.total as a string ("0") rather than a
// number, so the wrapper is unmarshalled into a struct that ignores
// pagination entirely.
func (c *Client) ListInvitations(ctx context.Context, orgID string) ([]Invitation, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/invitations", orgID), nil)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Invitations []Invitation `json:"invitations"`
	}
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse invitations: %w", err)
	}
	return wrapper.Invitations, nil
}

// GetInvitation returns a single invitation by ID. The Pangolin API
// does not expose a per-id GET, so this lists and filters.
func (c *Client) GetInvitation(ctx context.Context, orgID, inviteID string) (*Invitation, error) {
	invites, err := c.ListInvitations(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for i := range invites {
		if invites[i].InviteID == inviteID {
			return &invites[i], nil
		}
	}
	return nil, fmt.Errorf("invitation %s: %w", inviteID, ErrNotFound)
}

// FindInvitationByEmail looks up an invitation by email address. Used
// right after CreateInvite to discover the inviteId, which the create
// response does not include.
func (c *Client) FindInvitationByEmail(ctx context.Context, orgID, email string) (*Invitation, error) {
	invites, err := c.ListInvitations(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for i := range invites {
		if invites[i].Email == email {
			return &invites[i], nil
		}
	}
	return nil, fmt.Errorf("invitation for %s: %w", email, ErrNotFound)
}

// DeleteInvitation cancels a pending invitation.
func (c *Client) DeleteInvitation(ctx context.Context, orgID, inviteID string) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/org/%s/invitations/%s", orgID, inviteID), nil)
	return err
}

// --- Logs analytics ---

// LogsAnalyticsQuery filters a /logs/analytics request. All fields
// optional. TimeStart / TimeEnd are ISO 8601 / RFC 3339 strings.
// ResourceID narrows the analytics to a single resource.
type LogsAnalyticsQuery struct {
	TimeStart  string
	TimeEnd    string
	ResourceID string
}

// LogsAnalyticsCountry is one row of the requestsPerCountry breakdown.
type LogsAnalyticsCountry struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

// LogsAnalyticsDay is one row of the requestsPerDay breakdown.
//
// Quirk: the upstream emits the count fields as JSON strings (e.g.
// "18797") rather than numbers, but the day field is a plain string.
// Custom UnmarshalJSON below normalizes the count fields to int64 so
// downstream callers see uniform numeric types.
type LogsAnalyticsDay struct {
	Day          string `json:"day"`
	AllowedCount int64  `json:"-"`
	BlockedCount int64  `json:"-"`
	TotalCount   int64  `json:"-"`
}

// UnmarshalJSON decodes the count fields tolerantly: the upstream
// emits them as strings ("18797"); accept ints too in case that
// changes upstream.
func (d *LogsAnalyticsDay) UnmarshalJSON(data []byte) error {
	var raw struct {
		Day          string          `json:"day"`
		AllowedCount json.RawMessage `json:"allowedCount"`
		BlockedCount json.RawMessage `json:"blockedCount"`
		TotalCount   json.RawMessage `json:"totalCount"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	d.Day = raw.Day
	parseCount := func(field string, src json.RawMessage) (int64, error) {
		if len(src) == 0 || string(src) == "null" {
			return 0, nil
		}
		// strip surrounding quotes if the server emitted a string
		trimmed := string(src)
		if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
			trimmed = trimmed[1 : len(trimmed)-1]
		}
		n, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse %s %q: %w", field, src, err)
		}
		return n, nil
	}
	var err error
	if d.AllowedCount, err = parseCount("allowedCount", raw.AllowedCount); err != nil {
		return err
	}
	if d.BlockedCount, err = parseCount("blockedCount", raw.BlockedCount); err != nil {
		return err
	}
	if d.TotalCount, err = parseCount("totalCount", raw.TotalCount); err != nil {
		return err
	}
	return nil
}

// LogsAnalytics is the response body of GET /org/{org}/logs/analytics.
type LogsAnalytics struct {
	RequestsPerCountry []LogsAnalyticsCountry `json:"requestsPerCountry"`
	RequestsPerDay     []LogsAnalyticsDay     `json:"requestsPerDay"`
	TotalBlocked       int64                  `json:"totalBlocked"`
	TotalRequests      int64                  `json:"totalRequests"`
}

// GetLogsAnalytics queries the request analytics for an organization.
// Returns the per-country and per-day breakdowns plus totals.
//
// Requires an active enterprise subscription on Pangolin Cloud. On
// self-hosted enterprise installs the endpoint is always available.
func (c *Client) GetLogsAnalytics(ctx context.Context, orgID string, q LogsAnalyticsQuery) (*LogsAnalytics, error) {
	path := fmt.Sprintf("/org/%s/logs/analytics", orgID)
	values := url.Values{}
	for k, v := range map[string]string{
		"timeStart":  q.TimeStart,
		"timeEnd":    q.TimeEnd,
		"resourceId": q.ResourceID,
	} {
		if v != "" {
			values.Set(k, v)
		}
	}
	if qs := values.Encode(); qs != "" {
		path += "?" + qs
	}
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var out LogsAnalytics
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse logs analytics: %w", err)
	}
	return &out, nil
}
