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
	// ErrUnauthorized is returned for HTTP 401 - usually means the API
	// key is missing, malformed, expired, or has been revoked.
	ErrUnauthorized = errors.New("pangolin: unauthorized")
	// ErrForbidden is returned for HTTP 403 - usually means the API key
	// is valid but does not have access to the requested organization
	// or resource.
	ErrForbidden = errors.New("pangolin: forbidden")
	// ErrServer is returned for HTTP 5xx - the upstream is unhealthy.
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
// local debugging only - never use against a production Pangolin.
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
		return nil, fmt.Errorf("failed to parse response (status %d, content-type %q): %s",
			resp.StatusCode, resp.Header.Get("Content-Type"), truncateForDiag(respBody))
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

// truncateForDiag prepares an untrusted response body for a Terraform
// diagnostic. It collapses whitespace to a single line and caps the
// output so an HTML error page, stack trace, or otherwise long payload
// cannot flood the diag message.
func truncateForDiag(body []byte) string {
	const maxDiagBody = 512
	s := strings.Join(strings.Fields(string(body)), " ")
	if len(s) > maxDiagBody {
		return s[:maxDiagBody] + "... [truncated]"
	}
	return s
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
// richer payload than the create / list responses - exit-node assoc,
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
// richer shape than the older provider modeled - verification flags,
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

// ResourceHeader is one `{name, value}` request-header injection
// entry on a resource. Present on the wire on 1.19+.
type ResourceHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Resource represents a Pangolin HTTP resource.
//
// Pangolin 1.19 dropped the legacy `http bool` + `protocol` split in
// favor of a unified `mode` enum (http/ssh/rdp/vnc/tcp/udp) and
// added a large number of new fields around PAM, auth daemon,
// maintenance mode, proxy-protocol and resource-policy backing.
// Older server builds still emit the pre-1.19 fields; every 1.19+
// addition is `omitempty` in the request struct and safely absent
// on the response.
type Resource struct {
	// Identity
	ResourceID   int    `json:"resourceId"`
	ResourceGuid string `json:"resourceGuid,omitempty"`
	OrgID        string `json:"orgId,omitempty"`
	NiceID       string `json:"niceId"`
	Name         string `json:"name"`
	Subdomain    string `json:"subdomain"`
	FullDomain   string `json:"fullDomain"`
	DomainID     string `json:"domainId"`
	Protocol     string `json:"protocol,omitempty"`
	Wildcard     bool   `json:"wildcard,omitempty"`
	Health       string `json:"health,omitempty"`

	// Mode / L7 vs L4 (1.19)
	Mode           string `json:"mode,omitempty"`
	ProxyPort      *int   `json:"proxyPort,omitempty"`
	PamMode        string `json:"pamMode,omitempty"`
	AuthDaemonMode string `json:"authDaemonMode,omitempty"`
	AuthDaemonPort *int   `json:"authDaemonPort,omitempty"`

	// Access-control legacy inline fields - kept for backwards
	// compatibility on servers that still populate them directly.
	SSO                   bool    `json:"sso"`
	SSL                   bool    `json:"ssl"`
	Enabled               bool    `json:"enabled"`
	BlockAccess           bool    `json:"blockAccess"`
	EmailWhitelistEnabled bool    `json:"emailWhitelistEnabled"`
	ApplyRules            bool    `json:"applyRules"`
	StickySession         bool    `json:"stickySession"`
	TLSServerName         *string `json:"tlsServerName"`

	// 1.19 additions - routing / auth
	SetHostHeader *string          `json:"setHostHeader,omitempty"`
	EnableProxy   *bool            `json:"enableProxy,omitempty"`
	Headers       []ResourceHeader `json:"headers,omitempty"`
	SkipToIdpID   *int             `json:"skipToIdpId,omitempty"`
	PostAuthPath  *string          `json:"postAuthPath,omitempty"`

	// 1.19 additions - proxy-protocol
	ProxyProtocol        *bool `json:"proxyProtocol,omitempty"`
	ProxyProtocolVersion *int  `json:"proxyProtocolVersion,omitempty"`

	// 1.19 additions - maintenance mode
	MaintenanceModeEnabled   *bool   `json:"maintenanceModeEnabled,omitempty"`
	MaintenanceModeType      string  `json:"maintenanceModeType,omitempty"`
	MaintenanceTitle         *string `json:"maintenanceTitle,omitempty"`
	MaintenanceMessage       *string `json:"maintenanceMessage,omitempty"`
	MaintenanceEstimatedTime *string `json:"maintenanceEstimatedTime,omitempty"`

	// 1.19 additions - resource-policy backing (read-only; the CRUD
	// routes require a resource-policy-scoped API key and are not
	// exposed here). `DefaultResourcePolicyID` is auto-assigned by
	// the server on create; `ResourcePolicyID` is the shared policy
	// when the user attaches one.
	ResourcePolicyID        *int `json:"resourcePolicyId,omitempty"`
	DefaultResourcePolicyID *int `json:"defaultResourcePolicyId,omitempty"`
}

// UnmarshalJSON tolerates the Pangolin 1.19+ wire quirk where `sso`
// is emitted as a JSON number (0/1) on some server builds instead of
// a plain bool. Same 1.19 tag on the release; the sub-build patch
// level determines which shape you get, so the client accepts both
// and normalises to bool (see issue #50).
//
// Zero → false, non-zero → true. Older builds emit bool directly and
// pass through unchanged.
func (r *Resource) UnmarshalJSON(data []byte) error {
	type resourceAlias Resource
	aux := struct {
		SSO json.RawMessage `json:"sso"`
		*resourceAlias
	}{resourceAlias: (*resourceAlias)(r)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.SSO) == 0 || string(aux.SSO) == "null" {
		return nil
	}
	var b bool
	if err := json.Unmarshal(aux.SSO, &b); err == nil {
		r.SSO = b
		return nil
	}
	var n int
	if err := json.Unmarshal(aux.SSO, &n); err != nil {
		return fmt.Errorf("resource.sso: cannot decode %s as bool or int", aux.SSO)
	}
	r.SSO = n != 0
	return nil
}

// CreateResourceRequest is the payload for creating a Pangolin
// resource. Pre-1.19 you only had `http bool` + `protocol` + a
// domain/subdomain pair; 1.19+ added `mode` (http|tcp|udp) and
// `proxyPort` for the L4 modes.
//
// PAM / auth-daemon knobs (`pamMode`, `authDaemonMode`,
// `authDaemonPort`) are **not** accepted at Create - the server
// returns 400 "Unrecognized keys". They belong on the follow-up
// UpdateResource call, which is what the resource layer does after
// Create returns.
//
// `Subdomain` and `DomainID` use omitempty so an L4 resource
// creation payload does not carry the empty-string placeholders
// the server rejects with "Unrecognized keys: subdomain, domainId".
type CreateResourceRequest struct {
	Name      string  `json:"name"`
	HTTP      bool    `json:"http"`
	Subdomain *string `json:"subdomain,omitempty"`
	DomainID  string  `json:"domainId,omitempty"`
	Protocol  string  `json:"protocol"`

	// 1.19+ mode + edge listener port. Sent omitempty so older
	// server builds keep receiving the pre-1.19 payload unchanged.
	Mode      string `json:"mode,omitempty"`
	ProxyPort *int   `json:"proxyPort,omitempty"`
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

// ParseTargetHCHeaders decodes a target's hcHeaders response field -
// emitted by the server as a JSON-string (e.g. `"[]"` or `"[{\"name\":
// \"X-Probe\",\"value\":\"yes\"}]"`) - into a typed slice. Target's
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
		// JSON-encoded string form - decode the outer string layer
		var s string
		if err := json.Unmarshal(aux.HCHeaders, &s); err != nil {
			return fmt.Errorf("decode hcHeaders string: %w", err)
		}
		t.HCHeadersRaw = &s
	default:
		// Native array (or any other JSON value) - keep verbatim
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
// GET /resource/{id}/roles - only four fields per role, not the full
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

// ResourceUserEntry is one entry in the user list returned by
// GET /resource/{id}/users. Same wire shape as
// [SiteResourceUserEntry] - kept separate for clarity of domain.
type ResourceUserEntry struct {
	UserID   string  `json:"userId"`
	Username string  `json:"username"`
	Type     string  `json:"type"`
	IDPName  *string `json:"idpName"`
	IDPID    *int64  `json:"idpId"`
	Email    string  `json:"email"`
}

// ListResourceUsers returns the users currently bound to an HTTP
// resource. Response shape is `{users: [...]}`. Used by the
// resource_user Read to do real drift detection - the OpenAPI
// quirk is that this endpoint exists and works fine, despite an
// earlier (now stale) comment in the provider saying otherwise.
func (c *Client) ListResourceUsers(ctx context.Context, resourceID int) ([]ResourceUserEntry, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/resource/%d/users", resourceID), nil)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Users []ResourceUserEntry `json:"users"`
	}
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse resource users: %w", err)
	}
	return wrapper.Users, nil
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
// the CreateTargetRequest shape - every hc* / routing field is
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
//
// Three wire shapes feed this struct, with subtly different fields:
//
//   - CREATE (PUT /org/{org}/site-resource) and UPDATE
//     (POST /site-resource/{id}) return every scalar field but **omit**
//     the multi-site arrays (siteIds / siteNames / ...). The org-level
//     `siteId` documented in the OpenAPI is never returned.
//   - LIST (GET /org/{org}/site-resources) returns the scalars **plus**
//     the multi-site arrays. The singular site assignment is therefore
//     surfaced as SiteIDs[0]; the legacy `siteId` field is absent.
//
// Fields whose wire value is `null` when unset use pointer types so
// callers can distinguish unset (nil) from zero ("" / 0).
type SiteResource struct {
	SiteResourceID int    `json:"siteResourceId"`
	OrgID          string `json:"orgId"`
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
	PamMode        string `json:"pamMode,omitempty"`
	Enabled        bool   `json:"enabled"`
	SSL            bool   `json:"ssl"`
	NetworkID      int    `json:"networkId"`

	// Nullable scalars - wire emits literal `null` when unset.
	Scheme           *string `json:"scheme"`
	ProxyPort        *int    `json:"proxyPort"`
	DestinationPort  *int    `json:"destinationPort"`
	AliasAddress     *string `json:"aliasAddress"`
	DomainID         *string `json:"domainId"`
	Subdomain        *string `json:"subdomain"`
	FullDomain       *string `json:"fullDomain"`
	DefaultNetworkID *int    `json:"defaultNetworkId"`

	// LIST-only enrichments. The singular site attachment lives in
	// SiteIDs[0] when the slice is populated; Create/Update responses
	// leave these nil.
	SiteIDs       []int    `json:"siteIds,omitempty"`
	SiteNames     []string `json:"siteNames,omitempty"`
	SiteNiceIDs   []string `json:"siteNiceIds,omitempty"`
	SiteAddresses []string `json:"siteAddresses,omitempty"`
	SiteOnlines   []bool   `json:"siteOnlines,omitempty"`
}

// CreateSiteResourceRequest is the payload for creating a private site resource.
//
// Mode-specific fields:
//
//   - `cidr` / `host`: needs `Alias` + `TCPPortRange` / `UDPPortRange`.
//     The HTTP-only fields below are ignored.
//   - `http`: needs `DomainID`, `Subdomain`, `Scheme` (http|https) and
//     `DestinationPort`. The server rejects `mode = "http"` without
//     scheme+destinationPort set with HTTP 400 "HTTP mode requires
//     scheme (http or https) and a valid destination port". `Alias`
//     is null in this mode; `TCPPortRange` / `UDPPortRange` are
//     auto-filled by the server (`"443,80"` and `""` respectively).
//
// `proxyPort` is NOT a valid create input despite appearing on the
// wire response - the server fills it itself (`null` in every
// observation so far).
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
	PamMode        string   `json:"pamMode,omitempty"`
	RoleIDs        []int    `json:"roleIds"`
	UserIDs        []string `json:"userIds"`
	ClientIDs      []int    `json:"clientIds"`

	// HTTP-mode-only fields.
	DomainID        string `json:"domainId,omitempty"`
	Subdomain       string `json:"subdomain,omitempty"`
	Scheme          string `json:"scheme,omitempty"`
	DestinationPort int    `json:"destinationPort,omitempty"`
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
// schema accepts native arrays - kept as strings here so callers see
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
	// AllowSSH is a *bool so callers can distinguish "server did not
	// emit the field" (nil - the 1.19+ Read response no longer surfaces
	// allowSsh) from an explicit false. The Update/Create requests
	// keep pointer semantics too, so nil round-trips as "leave alone"
	// on the wire.
	AllowSSH           *bool  `json:"allowSsh,omitempty"`
	SSHSudoMode        string `json:"sshSudoMode,omitempty"`
	SSHSudoCommandsRaw string `json:"sshSudoCommands,omitempty"`
	SSHCreateHomeDir   bool   `json:"sshCreateHomeDir"`
	SSHUnixGroupsRaw   string `json:"sshUnixGroups,omitempty"`
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
// Pangolin server treats as a single-role assignment endpoint -
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

// SiteResourceRoleEntry is one entry in the role list returned by
// GET /site-resource/{id}/roles. Only RoleID is needed for drift
// detection on the assignment resource; the rest is surfaced for
// future datasources.
type SiteResourceRoleEntry struct {
	RoleID      int    `json:"roleId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsAdmin     bool   `json:"isAdmin"`
}

// ListSiteResourceRoles returns the roles currently assigned to a
// private site resource. Response shape is `{roles: [...]}`. The
// built-in Admin role is auto-attached at site-resource creation
// and will always appear here even when the caller never bound it
// via the API.
func (c *Client) ListSiteResourceRoles(ctx context.Context, siteResourceID int) ([]SiteResourceRoleEntry, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/site-resource/%d/roles", siteResourceID), nil)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Roles []SiteResourceRoleEntry `json:"roles"`
	}
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse site resource roles: %w", err)
	}
	return wrapper.Roles, nil
}

// SiteResourceUserEntry is one entry in the user list returned by
// GET /site-resource/{id}/users.
type SiteResourceUserEntry struct {
	UserID   string  `json:"userId"`
	Username string  `json:"username"`
	Type     string  `json:"type"`
	IDPName  *string `json:"idpName"`
	IDPID    *int64  `json:"idpId"`
	Email    string  `json:"email"`
}

// ListSiteResourceUsers returns the users currently assigned to a
// private site resource. Response shape is `{users: [...]}`.
func (c *Client) ListSiteResourceUsers(ctx context.Context, siteResourceID int) ([]SiteResourceUserEntry, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/site-resource/%d/users", siteResourceID), nil)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Users []SiteResourceUserEntry `json:"users"`
	}
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse site resource users: %w", err)
	}
	return wrapper.Users, nil
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
// modeled - IDP linkage, 2FA flag, ownership marker, dateCreated,
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
// username - usernames are unique only within an IDP, not globally.
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

// UpdateResourceRequest is the payload for updating a Pangolin
// resource. Every field is optional - the server only touches the
// keys that are present in the payload. Pre-1.19 servers ignore the
// 1.19-only fields, so it's safe to always send `omitempty`.
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

	// 1.19+ additions - routing / auth. `enableProxy`,
	// `proxyProtocol` and `proxyProtocolVersion` were probed on the
	// live 1.19 ent server: POST /resource/{id} answers with
	// `Unrecognized key`. They stay on the wire struct (Read
	// surfaces them) but not on the write payload.
	SetHostHeader *string           `json:"setHostHeader,omitempty"`
	Headers       *[]ResourceHeader `json:"headers,omitempty"`
	SkipToIdpID   *int              `json:"skipToIdpId,omitempty"`
	PostAuthPath  *string           `json:"postAuthPath,omitempty"`

	// 1.19+ additions - maintenance mode
	MaintenanceModeEnabled   *bool   `json:"maintenanceModeEnabled,omitempty"`
	MaintenanceModeType      string  `json:"maintenanceModeType,omitempty"`
	MaintenanceTitle         *string `json:"maintenanceTitle,omitempty"`
	MaintenanceMessage       *string `json:"maintenanceMessage,omitempty"`
	MaintenanceEstimatedTime *string `json:"maintenanceEstimatedTime,omitempty"`

	// 1.19+ additions - PAM / auth-daemon (mode is create-only,
	// so it is not exposed on Update).
	PamMode        string `json:"pamMode,omitempty"`
	AuthDaemonMode string `json:"authDaemonMode,omitempty"`
	AuthDaemonPort *int   `json:"authDaemonPort,omitempty"`
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
	PamMode        string   `json:"pamMode,omitempty"`
	RoleIDs        []int    `json:"roleIds"`
	UserIDs        []string `json:"userIds"`
	ClientIDs      []int    `json:"clientIds"`

	// HTTP-mode-only fields. Required when the underlying resource
	// is in `http` mode; ignored for `cidr` / `host`.
	DomainID        string `json:"domainId,omitempty"`
	Subdomain       string `json:"subdomain,omitempty"`
	Scheme          string `json:"scheme,omitempty"`
	DestinationPort int    `json:"destinationPort,omitempty"`
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
// fields are optional - pointer types so that omitempty drops them when
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

// GetRole retrieves a role by ID via the direct GET /role/{id}
// endpoint that Pangolin 1.19+ exposes. On older servers (or when
// the route is not wired) the caller should fall back to
// GetRoleByID, which handles that automatically.
func (c *Client) GetRole(ctx context.Context, roleID int) (*Role, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/role/%d", roleID), nil)
	if err != nil {
		return nil, err
	}
	var role Role
	if err := json.Unmarshal(resp.Data, &role); err != nil {
		return nil, fmt.Errorf("failed to parse role: %w", err)
	}
	return &role, nil
}

// GetRoleByID retrieves a role by ID. It prefers the direct
// GET /role/{id} endpoint introduced in Pangolin 1.19; if the
// server returns 404 (route not wired on older builds) it falls
// back to the historical list+filter path via ListRoles. This
// keeps the client backwards-compatible without paying the extra
// round-trip on 1.19+ deployments.
func (c *Client) GetRoleByID(ctx context.Context, roleID int) (*Role, error) {
	role, err := c.GetRole(ctx, roleID)
	if err == nil {
		return role, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	roles, listErr := c.ListRoles(ctx)
	if listErr != nil {
		return nil, listErr
	}
	for _, r := range roles {
		if r.RoleID == roleID {
			out := r
			return &out, nil
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

// APIKeyAction is one row of the action list bound to an API key.
// The wire shape is intentionally minimal - a single `actionId`
// per row, which is a server-defined camelCase operation name
// (e.g. `getOrg`, `listSites`, `createResource`). The catalog is
// closed and not introspectable from the spec; the OpenAPI
// `operationId` field is empty for every route in the live spec.
type APIKeyAction struct {
	ActionID string `json:"actionId"`
}

// ListAPIKeyActions returns the actions currently bound to an API
// key, in upstream insertion order. Response shape is
// `{actions: [...], pagination: {...}}`; the pagination block
// surfaces a `total` count and a `limit` of 1000 by default which is
// generous enough to ignore.
func (c *Client) ListAPIKeyActions(ctx context.Context, apiKeyID string) ([]APIKeyAction, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/api-key/%s/actions", c.OrgID, apiKeyID), nil)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Actions []APIKeyAction `json:"actions"`
	}
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse api key actions: %w", err)
	}
	return wrapper.Actions, nil
}

// SetAPIKeyActions replaces the set of actions bound to an API key
// with the given list. Live constraints observed on the enterprise
// tenant (the OpenAPI advertises a different schema; the spec is
// wrong):
//   - The input list must be non-empty. Empty arrays are rejected
//     with HTTP 400 `Validation error: Invalid input: expected
//     string, received undefined at "actionIds[0]"`. To "clear" the
//     actions on a key, delete the key instead.
//   - OpenAPI says `maxItems: 1` but the server happily accepts a
//     multi-element array and replaces the full set in one call.
//   - The action IDs are not validated against the OpenAPI routes -
//     they form a closed server-side enum (camelCase operation
//     names like `getOrg`, `listSites`). Unknown IDs return HTTP 400
//     `One or more actions do not exist`; surface as a plain error
//     so callers can read the message.
func (c *Client) SetAPIKeyActions(ctx context.Context, apiKeyID string, actionIDs []string) error {
	body := map[string]any{"actionIds": actionIDs}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/org/%s/api-key/%s/actions", c.OrgID, apiKeyID), body)
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
	OlmID    string `json:"olmId"`
	Name     string `json:"name"`
	Online   bool   `json:"online"`
	Archived bool   `json:"archived"`
	Blocked  bool   `json:"blocked"`
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

// UserDevice is the wire shape of an entry returned by
// GET /org/{org}/user-devices.
//
// The Pangolin server has never populated `devices: []` on this
// tenant (no end-user device registered yet), so the items shape is
// inferred from the sibling GET /org/{org}/clients endpoint that
// shares the "Clients retrieved successfully" message and the same
// `Client` OpenAPI tag. The 20 scalar fields below all appeared on
// live Client rows; nullable upstream fields use pointer types.
//
// One field omitted on purpose: `sites: []` is empty in the only
// live sample we've seen. Its element shape is unknown - could be
// `[]string` (nice IDs), `[]int` (numeric IDs), or `[]SiteRef`. Add
// it in a follow-up PR once a real value is observable, per the
// repo's "always probe before typing" rule.
type UserDevice struct {
	ClientID      int      `json:"clientId"`
	OrgID         string   `json:"orgId"`
	Name          string   `json:"name"`
	PubKey        *string  `json:"pubKey"`
	Subnet        string   `json:"subnet"`
	MegabytesIn   *float64 `json:"megabytesIn"`
	MegabytesOut  *float64 `json:"megabytesOut"`
	OrgName       string   `json:"orgName"`
	Type          string   `json:"type"`
	Online        bool     `json:"online"`
	OLMVersion    *string  `json:"olmVersion"`
	UserID        *string  `json:"userId"`
	Username      *string  `json:"username"`
	UserEmail     *string  `json:"userEmail"`
	NiceID        string   `json:"niceId"`
	Agent         *string  `json:"agent"`
	ApprovalState *string  `json:"approvalState"`
	OLMArchived   bool     `json:"olmArchived"`
	Archived      bool     `json:"archived"`
	Blocked       bool     `json:"blocked"`
}

// UserDevicesPage wraps the list endpoint's pagination payload. The
// `Total`, `Page`, `PageSize` triplet differs from the org-scoped
// site-resources / api-keys lists, which use `total / limit /
// offset`. The framework reads `total` as an int (not a stringified
// int as on /org/{org}/idp).
type UserDevicesPage struct {
	Devices    []UserDevice `json:"devices"`
	Pagination struct {
		Total    int `json:"total"`
		Page     int `json:"page"`
		PageSize int `json:"pageSize"`
	} `json:"pagination"`
}

// ListUserDevicesOptions controls the upstream filter / sort / paging
// behavior of GET /org/{org}/user-devices. Empty zero values map to
// "not specified" on the wire - the server then applies its defaults
// (pageSize=20, page=1, status=[active,pending], order=asc).
type ListUserDevicesOptions struct {
	PageSize int
	Page     int
	Query    string

	// SortBy is "megabytesIn" or "megabytesOut". Anything else is
	// passed through and the server will 400.
	SortBy string

	// Order is "asc" or "desc".
	Order string

	// Online filters by online status when non-nil.
	Online *bool

	// Agent filters by device agent. One of "windows", "android",
	// "cli", "olm", "macos", "ios", "ipados", "unknown".
	Agent string

	// Status filters by device approval / lifecycle status. Each
	// entry is one of "active", "pending", "denied", "blocked",
	// "archived". Comma-joined on the wire - the upstream parses
	// the CSV.
	Status []string
}

// ListUserDevices returns the user-bound device list (page +
// pagination block). Distinct from [Client.ListOLMClients]: this
// endpoint lists clients with a user binding (phones, laptops,
// browsers - see the `agent` enum), while ListOLMClients lists
// org-level OLM clients with no user association.
func (c *Client) ListUserDevices(ctx context.Context, opts *ListUserDevicesOptions) (*UserDevicesPage, error) {
	path := fmt.Sprintf("/org/%s/user-devices", c.OrgID)
	if opts != nil {
		params := url.Values{}
		if opts.PageSize > 0 {
			params.Set("pageSize", strconv.Itoa(opts.PageSize))
		}
		if opts.Page > 0 {
			params.Set("page", strconv.Itoa(opts.Page))
		}
		if opts.Query != "" {
			params.Set("query", opts.Query)
		}
		if opts.SortBy != "" {
			params.Set("sort_by", opts.SortBy)
		}
		if opts.Order != "" {
			params.Set("order", opts.Order)
		}
		if opts.Online != nil {
			params.Set("online", strconv.FormatBool(*opts.Online))
		}
		if opts.Agent != "" {
			params.Set("agent", opts.Agent)
		}
		if len(opts.Status) > 0 {
			params.Set("status", strings.Join(opts.Status, ","))
		}
		if enc := params.Encode(); enc != "" {
			path += "?" + enc
		}
	}

	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var page UserDevicesPage
	if err := json.Unmarshal(resp.Data, &page); err != nil {
		return nil, fmt.Errorf("failed to parse user devices page: %w", err)
	}
	return &page, nil
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
//
// The upstream endpoint wraps the updated entity in a single-element array
// (i.e. `"data": [{...}]`), so we unmarshal into a slice first and fall back
// to a bare object for older server builds that return the entity directly.
func (c *Client) UpdateOLMClient(ctx context.Context, clientID int, req *UpdateOLMClientRequest) (*OLMClient, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/client/%d", clientID), req)
	if err != nil {
		return nil, err
	}
	var clients []OLMClient
	if err := json.Unmarshal(resp.Data, &clients); err == nil {
		if len(clients) == 0 {
			return nil, fmt.Errorf("OLM client update returned empty array")
		}
		return &clients[0], nil
	}
	var single OLMClient
	if err := json.Unmarshal(resp.Data, &single); err != nil {
		return nil, fmt.Errorf("failed to parse OLM client: %w", err)
	}
	return &single, nil
}

// DeleteOLMClient deletes an OLM client by ID.
func (c *Client) DeleteOLMClient(ctx context.Context, clientID int) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/client/%d", clientID), nil)
	return err
}

// ArchiveClient archives an OLM client.
func (c *Client) ArchiveClient(ctx context.Context, clientID int) error {
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/client/%d/archive", clientID), nil)
	return err
}

// UnarchiveClient un-archives an OLM client.
func (c *Client) UnarchiveClient(ctx context.Context, clientID int) error {
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/client/%d/unarchive", clientID), nil)
	return err
}

// BlockClient blocks an OLM client.
func (c *Client) BlockClient(ctx context.Context, clientID int) error {
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/client/%d/block", clientID), nil)
	return err
}

// UnblockClient un-blocks an OLM client.
func (c *Client) UnblockClient(ctx context.Context, clientID int) error {
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/client/%d/unblock", clientID), nil)
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
// [...]}` - the items are either plain email strings or `{email}`
// objects depending on the server build, so the unmarshaler accepts
// both forms and normalizes to a `[]string` of emails.
//
// Returns an empty slice when the resource has no whitelist
// configured or when email_whitelist_enabled is off. The 400 error
// "Email whitelist is not enabled for this resource" only fires on
// add/remove, not on the GET - confirmed live.
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
// Admin role is mandatory on every resource - the API rejects any
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

// SiteResourceClientEntry is one entry in the client list returned
// by GET /site-resource/{id}/clients. The slim shape is intentional
// - the upstream payload only carries clientId / name / subnet.
type SiteResourceClientEntry struct {
	ClientID int    `json:"clientId"`
	Name     string `json:"name"`
	Subnet   string `json:"subnet"`
}

// ListSiteResourceClients returns the OLM clients currently
// assigned to a private site resource. Response shape is
// `{clients: [...]}`.
func (c *Client) ListSiteResourceClients(ctx context.Context, siteResourceID int) ([]SiteResourceClientEntry, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/site-resource/%d/clients", siteResourceID), nil)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Clients []SiteResourceClientEntry `json:"clients"`
	}
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse site resource clients: %w", err)
	}
	return wrapper.Clients, nil
}

// SetSiteResourceRoles replaces the non-admin role bindings of a
// private site resource with the given set. Mirrors the resource-
// level quirk (see [Client.SetResourceRoles]): the built-in Admin
// role (id 1) is auto-attached at creation and the API rejects any
// roleIds list that *includes* role 1 with `Admin role cannot be
// assigned to site resources`. Callers should pass only the
// additional roles to bind; Admin stays put.
func (c *Client) SetSiteResourceRoles(ctx context.Context, siteResourceID int, roleIDs []int) error {
	if roleIDs == nil {
		roleIDs = []int{}
	}
	body := map[string]any{"roleIds": roleIDs}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/site-resource/%d/roles", siteResourceID), body)
	return err
}

// SetSiteResourceUsers replaces the user bindings of a private site
// resource with the given set. Pass an empty slice to clear all
// per-user bindings (role-based access still applies).
func (c *Client) SetSiteResourceUsers(ctx context.Context, siteResourceID int, userIDs []string) error {
	if userIDs == nil {
		userIDs = []string{}
	}
	body := map[string]any{"userIds": userIDs}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/site-resource/%d/users", siteResourceID), body)
	return err
}

// SetSiteResourceClients replaces the OLM client bindings of a
// private site resource with the given set. Pass an empty slice to
// clear all client bindings.
func (c *Client) SetSiteResourceClients(ctx context.Context, siteResourceID int, clientIDs []int) error {
	if clientIDs == nil {
		clientIDs = []int{}
	}
	body := map[string]any{"clientIds": clientIDs}
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/site-resource/%d/clients", siteResourceID), body)
	return err
}

// AddClientToSiteResourcesResponse is the upstream payload returned
// when bulk-assigning an OLM client to several site resources at once.
// `AddedCount + SkippedCount` equals the length of the input list; a
// skipped entry means the client was already bound to that site
// resource.
type AddClientToSiteResourcesResponse struct {
	AddedCount      int   `json:"addedCount"`
	SkippedCount    int   `json:"skippedCount"`
	SiteResourceIDs []int `json:"siteResourceIds"`
}

// AddClientToSiteResources bulk-assigns an OLM client to one or more
// private site resources in a single call. Wire constraints observed
// live on the enterprise tenant:
//   - The input list must be non-empty; the API rejects `siteResourceIds: []`
//     with HTTP 400 `Validation error: At least one siteResourceId is required`.
//   - If the client is already bound to every requested site resource,
//     the API responds with HTTP 409 `Client is already assigned to all
//     specified site resources` (surfaced here as a generic error from
//     [Client.doRequest]).
//   - Partial overlap: `addedCount` reflects newly created bindings;
//     `skippedCount` reflects ones that were already in place.
func (c *Client) AddClientToSiteResources(ctx context.Context, clientID int, siteResourceIDs []int) (*AddClientToSiteResourcesResponse, error) {
	body := map[string]any{"siteResourceIds": siteResourceIDs}
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/client/%d/site-resources", clientID), body)
	if err != nil {
		return nil, err
	}
	var out AddClientToSiteResourcesResponse
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse client/site-resources response: %w", err)
	}
	return &out, nil
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

// ListSiteResourcesForSite returns the private site resources attached
// to a specific site. The upstream payload leaks the SQL join shape -
// each item is a struct of `{siteNetworks, networks, siteResources}`
// where the inner `siteResources` key is the actual SiteResource entity.
// This helper unwraps and returns a clean `[]SiteResource`.
//
// Marginal value vs the org-wide [Client.ListSiteResources] for most
// callers - the same filtering is achievable in HCL via `for x in
// data.pangolin_site_resources.all.site_resources : x if x.site_id == N`.
// Exposed mainly to round out coverage of the API surface.
func (c *Client) ListSiteResourcesForSite(ctx context.Context, siteID int) ([]SiteResource, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/site/%d/resources", c.OrgID, siteID), nil)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		SiteResources []struct {
			SiteResources SiteResource `json:"siteResources"`
		} `json:"siteResources"`
	}
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse per-site resources: %w", err)
	}
	out := make([]SiteResource, 0, len(wrapper.SiteResources))
	for _, row := range wrapper.SiteResources {
		out = append(out, row.SiteResources)
	}
	return out, nil
}

// --- Organizations ---

// Org represents a Pangolin organization.
type Org struct {
	OrgID         string `json:"orgId"`
	Name          string `json:"name"`
	Subnet        string `json:"subnet"`
	UtilitySubnet string `json:"utilitySubnet"`
	CreatedAt     string `json:"createdAt,omitempty"`

	// Security policies - nullable upstream (null = unset / inherit).
	RequireTwoFactor      *bool `json:"requireTwoFactor"`
	MaxSessionLengthHours *int  `json:"maxSessionLengthHours"`
	PasswordExpiryDays    *int  `json:"passwordExpiryDays"`

	// Audit log retention (days). 0 means disabled for that log stream.
	SettingsLogRetentionDaysRequest    int `json:"settingsLogRetentionDaysRequest"`
	SettingsLogRetentionDaysAccess     int `json:"settingsLogRetentionDaysAccess"`
	SettingsLogRetentionDaysAction     int `json:"settingsLogRetentionDaysAction"`
	SettingsLogRetentionDaysConnection int `json:"settingsLogRetentionDaysConnection"`

	// SSH CA - used to sign certificates for SSH bastion access. The
	// private key is returned in clear by the API; treat as secret.
	SSHCaPrivateKey string `json:"sshCaPrivateKey,omitempty"`
	SSHCaPublicKey  string `json:"sshCaPublicKey,omitempty"`

	// Billing - IsBillingOrg=true means this org carries the billing
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

// ResetOrgBandwidth wipes the bandwidth counters tracked per site for
// an organization. The endpoint is fire-and-forget (response body is
// `data: {}`); intended for admin / billing operations and exposed
// here to round out organization-level coverage. Not surfaced as a
// Terraform resource - the action is imperative, not declarative.
func (c *Client) ResetOrgBandwidth(ctx context.Context, orgID string) error {
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/org/%s/reset-bandwidth", orgID), map[string]any{})
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

// TwoFAStatus is the trimmed response of POST /user/{id}/2fa.
// The wire key on the response (`twoFactorRequested`) differs from
// the request key (`twoFactorSetupRequested`) - kept distinct so
// the caller sees what the server actually emitted.
type TwoFAStatus struct {
	UserID             string `json:"userId"`
	TwoFactorRequested bool   `json:"twoFactorRequested"`
}

// SetUser2FAStatus flips the user's 2FA setup flag. Passing `true`
// marks the user as needing to set up 2FA on next login; passing
// `false` clears the request. The endpoint is root-only - fails
// with HTTP 403 `Key does not have root access` on a non-admin key.
//
// Companion broken endpoint (documented in repo memory): PUT
// /org/{org}/user/{id}/client (create OLM client + bind to user)
// returns 404 HTML on the current enterprise build; the route is
// not wired even with a root key.
func (c *Client) SetUser2FAStatus(ctx context.Context, userID string, requested bool) (*TwoFAStatus, error) {
	body := map[string]any{"twoFactorSetupRequested": requested}
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/user/%s/2fa", userID), body)
	if err != nil {
		return nil, err
	}
	var out TwoFAStatus
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse 2FA response: %w", err)
	}
	return &out, nil
}

// ListOrgs returns every organization visible to the calling key.
// Root-only - fails with HTTP 403 on a non-admin key. The response
// items reuse the existing [Org] struct shape (full payload incl.
// SSH CA private key, billing fields, log retention settings).
func (c *Client) ListOrgs(ctx context.Context) ([]Org, error) {
	resp, err := c.doRequest(ctx, "GET", "/orgs", nil)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Orgs []Org `json:"orgs"`
	}
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse orgs list: %w", err)
	}
	return wrapper.Orgs, nil
}

// RootUserDetail is the wire shape of GET /user/{userId} - the root
// (cross-org) single-user lookup. Distinct from [User] (per-org list
// shape) in two ways: it uses `userId` instead of `id` as the JSON
// key, and it surfaces fields not visible in the org-scoped variants:
//
//   - ServerAdmin - true for users with server-admin scope (the
//     sentinel identifying a "root" account).
//   - TwoFactorSetupRequested - the request-side flag flipped by
//     [Client.SetUser2FAStatus]; the org-scoped User struct only
//     carries TwoFactorEnabled (whether 2FA is actually set up).
//   - IDPName / IDPID - nullable. Internal users have both nil;
//     external IDP-provisioned users carry a name + numeric ID.
//
// Root-only - fails with HTTP 403 on a non-admin key.
type RootUserDetail struct {
	UserID                  string  `json:"userId"`
	Email                   string  `json:"email"`
	Username                string  `json:"username"`
	Name                    *string `json:"name"`
	Type                    string  `json:"type"`
	TwoFactorEnabled        bool    `json:"twoFactorEnabled"`
	TwoFactorSetupRequested bool    `json:"twoFactorSetupRequested"`
	EmailVerified           bool    `json:"emailVerified"`
	ServerAdmin             bool    `json:"serverAdmin"`
	IDPName                 *string `json:"idpName"`
	IDPID                   *int64  `json:"idpId"`
	DateCreated             string  `json:"dateCreated"`
}

// GetUserByID retrieves the root (cross-org) detail of a user by ID.
// Root-only - fails with HTTP 403 on a non-admin key. Distinct from
// the org-scoped [Client.GetUser] (`GET /org/{org}/user/{id}`) by
// the additional fields surfaced on [RootUserDetail] (ServerAdmin,
// TwoFactorSetupRequested, EmailVerified, DateCreated, IDPName).
func (c *Client) GetUserByID(ctx context.Context, userID string) (*RootUserDetail, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/user/%s", userID), nil)
	if err != nil {
		return nil, err
	}
	var out RootUserDetail
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse root user detail: %w", err)
	}
	return &out, nil
}

// --- Blueprint ---
//
// Blueprints are an append-only audit log of declarative "apply"
// payloads - each PUT records a new entry with an auto-generated
// pet-name (`productive-defenseless-toothpick`, `linear-general-elver`,
// …) and never overwrites prior ones. There is no DELETE endpoint;
// once applied, a blueprint persists. Because of this, the package
// exposes Apply + Get + List as plain client methods but no
// Terraform resource - the PUT semantics don't fit declarative state
// (every Create would mint a new audit record on every plan).

// Blueprint is the slim row shape returned by
// GET /org/{org}/blueprints. `CreatedAt` is epoch **seconds** here -
// distinct from the millisecond timestamps used on every other
// Pangolin list endpoint (`/access-tokens`, `/api-keys`, …).
type Blueprint struct {
	BlueprintID int    `json:"blueprintId"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Succeeded   bool   `json:"succeeded"`
	OrgID       string `json:"orgId"`
	CreatedAt   int64  `json:"createdAt"`
}

// BlueprintDetail extends [Blueprint] with the per-entry fields only
// exposed by the single GET: `Message` (the server's apply outcome
// text) and `Contents` (the decoded JSON payload that was applied,
// base64-stripped).
type BlueprintDetail struct {
	Blueprint
	Message  string `json:"message"`
	Contents string `json:"contents"`
}

// ApplyBlueprintRequest is the wire body for PUT /org/{org}/blueprint.
// The `Blueprint` field is a base64-encoded JSON document - the
// server decodes, parses, and dispatches it through the same code
// path the Pangolin UI uses for "apply blueprint".
type ApplyBlueprintRequest struct {
	Blueprint string `json:"blueprint"`
}

// ApplyBlueprint applies a base64-encoded JSON document to the org.
// The blueprint mints a new audit record regardless of whether
// anything actually changes upstream - repeated identical applies
// pile up new entries; there is no idempotency guarantee.
//
// The server returns 201 with `data: null` on success; the freshly
// created blueprint's numeric ID is NOT echoed back. Callers needing
// to identify the new entry should list afterwards and pick the
// highest `BlueprintID`.
//
// Empty / malformed JSON payloads return HTTP 400 with descriptive
// validation error messages - surfaced as a plain error by
// [Client.doRequest].
func (c *Client) ApplyBlueprint(ctx context.Context, base64JSON string) error {
	body := ApplyBlueprintRequest{Blueprint: base64JSON}
	_, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/org/%s/blueprint", c.OrgID), body)
	return err
}

// GetBlueprint retrieves a single blueprint audit record by ID,
// including the raw `Contents` (decoded JSON payload that was
// applied) and the server's apply `Message`.
func (c *Client) GetBlueprint(ctx context.Context, blueprintID int) (*BlueprintDetail, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/blueprint/%d", c.OrgID, blueprintID), nil)
	if err != nil {
		return nil, err
	}
	var out BlueprintDetail
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse blueprint: %w", err)
	}
	return &out, nil
}

// ListBlueprints returns every blueprint audit record for the org,
// in upstream order (not necessarily sorted by ID - observed live as
// approximately insertion order but with some interleaving).
func (c *Client) ListBlueprints(ctx context.Context) ([]Blueprint, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/blueprints", c.OrgID), nil)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Blueprints []Blueprint `json:"blueprints"`
	}
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse blueprints list: %w", err)
	}
	return wrapper.Blueprints, nil
}

// --- IDP ---

// IDP represents a Pangolin Identity Provider.
//
// Variant refines Type for OIDC providers - observed values are
// "oidc" (generic), "google", "azure". The Pangolin UI uses the
// variant to pre-fill provider-specific URLs and tweak the consent
// flow; downstream consumers can branch on it to render different
// help text.
//
// OrgCount comes from the API as a JSON-encoded string (e.g. "0")
// in the LIST response - kept as string here to match the wire.
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
// Variant is optional - defaults to "oidc" server-side when omitted.
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

// --- Org-scoped IDP CRUD ---
//
// These are the 5 routes under `/org/{orgId}/idp*` - distinct from the
// system-wide `/idp*` routes already covered above. The org-scoped
// variants auto-bind the IDP to the calling org at creation time, so
// they replace the two-step `pangolin_idp` + `pangolin_idp_org`
// workflow with a single resource. Probed live on the enterprise
// tenant:
//
//   - PUT  /org/{org}/idp/oidc     → CreateOrgIDP (auto-bind)
//   - GET  /org/{org}/idp          → ListOrgIDPs (org-scoped slim list)
//   - GET  /org/{org}/idp/{id}     → GetOrgIDP (with `idpOrg` mapping block)
//   - POST /org/{org}/idp/{id}/oidc → UpdateOrgIDP
//   - DELETE /org/{org}/idp/{id}   → DeleteOrgIDP
//
// The list endpoint emits a slim row shape (no orgCount / autoProvision
// - see [OrgIDPListItem]). Single GET wraps three blocks together -
// the org-binding row lives in `idpOrg`.

// OrgIDPListItem is the slim row returned by the org-scoped list.
// Differs from [IDP] (system-wide list) by carrying the binding `orgId`
// and dropping the `orgCount` / `autoProvision` / `defaultRoleMapping`
// / `defaultOrgMapping` fields.
type OrgIDPListItem struct {
	IDPId   int    `json:"idpId"`
	OrgID   string `json:"orgId"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Variant string `json:"variant,omitempty"`
	Tags    string `json:"tags"`
}

// OrgIDPDetail wraps the three blocks returned by the org-scoped
// single GET. `IDPOrg` carries the per-org binding settings
// (`roleMapping`, `orgMapping`) that the system-wide GET puts in a
// separate `idp_org_policy` flow.
type OrgIDPDetail struct {
	IDP           IDP           `json:"idp"`
	IDPOidcConfig IDPOidcConfig `json:"idpOidcConfig"`
	IDPOrg        OrgIDPBindRow `json:"idpOrg"`
}

// OrgIDPBindRow is the org-binding metadata embedded in the
// single-GET response. Mirrors [IDPOrgPolicy] but lives inside the
// detail payload rather than as a standalone row.
type OrgIDPBindRow struct {
	IDPId       int     `json:"idpId"`
	OrgID       string  `json:"orgId"`
	RoleMapping *string `json:"roleMapping"`
	OrgMapping  *string `json:"orgMapping"`
}

// CreateOrgIDP creates an OIDC IDP auto-bound to the given org. The
// request body has the same shape as [CreateIDPRequest] - the only
// difference is the path. Response mirrors [CreateIDPResponse]
// (`{idpId, redirectUrl}`).
func (c *Client) CreateOrgIDP(ctx context.Context, orgID string, req *CreateIDPRequest) (*CreateIDPResponse, error) {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/org/%s/idp/oidc", orgID), req)
	if err != nil {
		return nil, err
	}
	var result CreateIDPResponse
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse org IDP create response: %w", err)
	}
	return &result, nil
}

// ListOrgIDPs returns the IDPs bound to the given org. Empty list
// means none of the system-wide IDPs are bound to this org.
func (c *Client) ListOrgIDPs(ctx context.Context, orgID string) ([]OrgIDPListItem, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/idp", orgID), nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		IDPs []OrgIDPListItem `json:"idps"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse org IDPs list: %w", err)
	}
	return result.IDPs, nil
}

// GetOrgIDP retrieves the full IDP detail (incl. OIDC config and the
// per-org binding row) for an IDP bound to the given org.
func (c *Client) GetOrgIDP(ctx context.Context, orgID string, idpID int) (*OrgIDPDetail, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/idp/%d", orgID, idpID), nil)
	if err != nil {
		return nil, err
	}
	var result OrgIDPDetail
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse org IDP detail: %w", err)
	}
	return &result, nil
}

// UpdateOrgIDP updates the OIDC config of an org-scoped IDP. Same
// body shape as [UpdateIDPRequest]; response is the trimmed
// `{idpId}` only - fetch with [Client.GetOrgIDP] if the full payload
// is needed.
func (c *Client) UpdateOrgIDP(ctx context.Context, orgID string, idpID int, req *UpdateIDPRequest) error {
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/org/%s/idp/%d/oidc", orgID, idpID), req)
	return err
}

// DeleteOrgIDP unbinds and deletes an IDP from the given org.
func (c *Client) DeleteOrgIDP(ctx context.Context, orgID string, idpID int) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/org/%s/idp/%d", orgID, idpID), nil)
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
// (GET /org/{org}/domain/{id}) - preferred over GetDomainByID since
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

// PatchDomainRequest is the body for the narrow PATCH endpoint.
// The OpenAPI does not advertise a request schema; probed live, the
// only two accepted keys are `certResolver` (string, nullable) and
// `preferWildcardCert` (bool). Sending any other key - including
// `baseDomain` / `type` / `verified` - fails the validator with
// `Unrecognized key: "..."` (HTTP 400). Pointer types preserve the
// distinction between "leave unchanged" (nil - omitted from the
// body) and "set to null/false" (non-nil).
type PatchDomainRequest struct {
	CertResolver       *string `json:"certResolver,omitempty"`
	PreferWildcardCert *bool   `json:"preferWildcardCert,omitempty"`
}

// PatchDomainResponse is the trimmed payload returned by the PATCH
// endpoint. The API echoes only the two updatable fields plus the
// domain ID, not the full Domain entity.
type PatchDomainResponse struct {
	DomainID           string  `json:"domainId"`
	CertResolver       *string `json:"certResolver"`
	PreferWildcardCert bool    `json:"preferWildcardCert"`
}

// PatchDomain updates the cert-resolver settings on a domain. The
// upstream endpoint accepts only `certResolver` and
// `preferWildcardCert`; everything else is rejected with a 400. Use
// [Client.GetDomain] after the call if the full updated Domain
// payload is needed - the PATCH response is intentionally narrow.
func (c *Client) PatchDomain(ctx context.Context, orgID, domainID string, req *PatchDomainRequest) (*PatchDomainResponse, error) {
	resp, err := c.doRequest(ctx, "PATCH", fmt.Sprintf("/org/%s/domain/%s", orgID, domainID), req)
	if err != nil {
		return nil, err
	}
	var out PatchDomainResponse
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse PatchDomain response: %w", err)
	}
	return &out, nil
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

// --- Resource Access Tokens ---

// ResourceAccessToken represents an access token bound to an HTTP
// resource. Three wire shapes feed this struct with subtly different
// fields:
//
//   - CREATE (POST /resource/{id}/access-token) returns the freshly
//     generated `accessToken` (the bearer secret) but **does not**
//     return `tokenHash` / `resourceName` / `resourceNiceId` / `siteName`.
//     The secret is only visible here - store it before discarding the
//     response.
//   - LIST per-resource (GET /resource/{id}/access-tokens) and LIST
//     org-wide (GET /org/{orgId}/access-tokens) both omit `accessToken`
//     entirely and surface `tokenHash` + the enrichment fields instead.
//     There is no way to recover the bearer secret after creation.
//
// Nullable upstream fields use pointer types so callers can
// distinguish unset (nil) from zero ("" / 0).
type ResourceAccessToken struct {
	AccessTokenID string  `json:"accessTokenId"`
	OrgID         string  `json:"orgId"`
	ResourceID    int     `json:"resourceId"`
	SessionLength int64   `json:"sessionLength"`
	ExpiresAt     *int64  `json:"expiresAt"`
	Title         *string `json:"title"`
	Description   *string `json:"description"`
	CreatedAt     int64   `json:"createdAt"`

	// CREATE-only: the bearer secret. Empty on list responses.
	AccessToken string `json:"accessToken,omitempty"`

	// LIST-only enrichments. Empty on the CREATE response.
	TokenHash      string  `json:"tokenHash,omitempty"`
	ResourceName   string  `json:"resourceName,omitempty"`
	ResourceNiceID string  `json:"resourceNiceId,omitempty"`
	SiteName       *string `json:"siteName,omitempty"`
}

// CreateResourceAccessTokenRequest is the request body for
// POST /resource/{id}/access-token. All fields are optional; the API
// fills in sensible defaults (sessionLength ≈ 30 days, expiresAt
// derived from createdAt + sessionLength when validForSeconds is set,
// otherwise null = never expires).
type CreateResourceAccessTokenRequest struct {
	Title           *string `json:"title,omitempty"`
	Description     *string `json:"description,omitempty"`
	ValidForSeconds *int64  `json:"validForSeconds,omitempty"`
}

// CreateResourceAccessToken provisions a new access token on an HTTP
// resource. The returned struct's AccessToken field carries the bearer
// secret - only visible here. Subsequent reads expose only TokenHash.
func (c *Client) CreateResourceAccessToken(ctx context.Context, resourceID int, req *CreateResourceAccessTokenRequest) (*ResourceAccessToken, error) {
	if req == nil {
		req = &CreateResourceAccessTokenRequest{}
	}
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/resource/%d/access-token", resourceID), req)
	if err != nil {
		return nil, err
	}
	var out ResourceAccessToken
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse resource access token: %w", err)
	}
	return &out, nil
}

// ResourceAccessTokenListResponse wraps the paginated list payload
// returned by both per-resource and org-wide list endpoints.
type ResourceAccessTokenListResponse struct {
	AccessTokens []ResourceAccessToken `json:"accessTokens"`
	Pagination   struct {
		Total  int `json:"total"`
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	} `json:"pagination"`
}

// ListResourceAccessTokens returns the access tokens bound to a
// specific HTTP resource. The bearer secrets are not exposed by this
// endpoint - only TokenHash + enrichment fields.
func (c *Client) ListResourceAccessTokens(ctx context.Context, resourceID int) ([]ResourceAccessToken, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/resource/%d/access-tokens", resourceID), nil)
	if err != nil {
		return nil, err
	}
	var wrapper ResourceAccessTokenListResponse
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse resource access tokens: %w", err)
	}
	return wrapper.AccessTokens, nil
}

// ListOrgAccessTokens returns every access token in the organization,
// regardless of which resource they're bound to. Useful for org-wide
// audit datasources.
func (c *Client) ListOrgAccessTokens(ctx context.Context) ([]ResourceAccessToken, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/org/%s/access-tokens", c.OrgID), nil)
	if err != nil {
		return nil, err
	}
	var wrapper ResourceAccessTokenListResponse
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse org access tokens: %w", err)
	}
	return wrapper.AccessTokens, nil
}

// GetResourceAccessToken retrieves a single access token by ID via
// list+filter on the org-wide list endpoint. The Pangolin API does
// not expose a per-id GET, so this scales linearly with the number
// of tokens in the org.
func (c *Client) GetResourceAccessToken(ctx context.Context, accessTokenID string) (*ResourceAccessToken, error) {
	tokens, err := c.ListOrgAccessTokens(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range tokens {
		if t.AccessTokenID == accessTokenID {
			tok := t
			return &tok, nil
		}
	}
	return nil, fmt.Errorf("access token %s: %w", accessTokenID, ErrNotFound)
}

// DeleteResourceAccessToken revokes an access token by its ID.
// The endpoint is keyed by the token ID alone - no resource scope is
// required.
func (c *Client) DeleteResourceAccessToken(ctx context.Context, accessTokenID string) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/access-token/%s", accessTokenID), nil)
	return err
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
// FilterAttributes.Resources slice - the server returns resource
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

// AuditLogKind selects which audit log stream to query. Values match the
// URL segment used by /org/{org}/logs/{kind}.
type AuditLogKind string

const (
	// AuditLogAccess covers per-request access decisions on protected
	// HTTP resources.
	AuditLogAccess AuditLogKind = "access"
	// AuditLogAction covers admin/mutation actions performed via the
	// Integration API or UI.
	AuditLogAction AuditLogKind = "action"
	// AuditLogConnection covers VPN/tunnel connection lifecycle events
	// from OLM clients and site connectors.
	AuditLogConnection AuditLogKind = "connection"
)

// AuditLogQuery filters an access/action/connection audit log query.
// All fields are optional and are only sent to the server when set.
// The per-log-kind filter semantics vary but the URL parameter names
// are stable across the three streams observed on Pangolin enterprise.
type AuditLogQuery struct {
	TimeStart  string
	TimeEnd    string
	Action     string
	Actor      string
	ResourceID string
	Limit      string
	Offset     string
}

// AuditLogsResponse is the wrapper returned by
// GET /org/{org}/logs/{access,action,connection}. The entry shape is
// deliberately opaque: on the current server build the log slice is
// empty across the three streams (no events accumulated on the tested
// tenant), so we surface each entry as a raw JSON payload and let
// operators jsondecode() what they need.
//
// FilterAttributes also varies per stream (access returns
// actors/resources/locations, action returns actors/actions, connection
// returns protocols/destAddrs/clients/resources/users). Modeling it as
// map[string]RawMessage keeps the client honest about the fact that
// each stream ships a different set of dimensions.
type AuditLogsResponse struct {
	Log              []json.RawMessage          `json:"log"`
	Pagination       RequestLogPagination       `json:"pagination"`
	FilterAttributes map[string]json.RawMessage `json:"filterAttributes"`
}

// ListAuditLogs queries one of the access/action/connection audit log
// streams for an organization. Returns the raw entries (each a JSON
// blob preserved verbatim) plus pagination and per-stream filter
// dimensions. Requires an active subscription on Pangolin Cloud; on
// self-host enterprise the endpoint is available with an admin token.
func (c *Client) ListAuditLogs(ctx context.Context, orgID string, kind AuditLogKind, q AuditLogQuery) (*AuditLogsResponse, error) {
	path := fmt.Sprintf("/org/%s/logs/%s", orgID, kind)
	values := url.Values{}
	for k, v := range map[string]string{
		"timeStart":  q.TimeStart,
		"timeEnd":    q.TimeEnd,
		"action":     q.Action,
		"actor":      q.Actor,
		"resourceId": q.ResourceID,
		"limit":      q.Limit,
		"offset":     q.Offset,
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
	var out AuditLogsResponse
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse %s audit logs: %w", kind, err)
	}
	return &out, nil
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
