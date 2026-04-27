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
	// Callers may retry with backoff.
	ErrServer = errors.New("pangolin: server error")
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

// doRequest performs an HTTP request and returns the parsed API response.
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) (*APIResponse, error) {
	url := fmt.Sprintf("%s/v1%s", c.BaseURL, path)

	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonBody)
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
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
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

// classifyError maps an HTTP status code to a wrapped sentinel error so
// callers can distinguish missing resources, auth failures, permission
// failures and retryable server errors with errors.Is. Unknown status
// codes fall through to a plain error.
func classifyError(status int, message string) error {
	switch {
	case status == http.StatusNotFound:
		return fmt.Errorf("API error (status 404): %s: %w", message, ErrNotFound)
	case status == http.StatusUnauthorized:
		return fmt.Errorf("API error (status 401): %s: %w", message, ErrUnauthorized)
	case status == http.StatusForbidden:
		return fmt.Errorf("API error (status 403): %s: %w", message, ErrForbidden)
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
type Site struct {
	SiteID              int    `json:"siteId"`
	NiceID              string `json:"niceId"`
	Name                string `json:"name"`
	Type                string `json:"type"`
	Online              bool   `json:"online"`
	Address             string `json:"address"`
	DockerSocketEnabled bool   `json:"dockerSocketEnabled"`
}

// CreateSiteRequest is the payload for creating a site.
type CreateSiteRequest struct {
	Name                string `json:"name"`
	Type                string `json:"type"`
	NewtID              string `json:"newtId,omitempty"`
	Secret              string `json:"secret,omitempty"`
	DockerSocketEnabled bool   `json:"dockerSocketEnabled"`
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

// DeleteSite deletes a site by ID.
func (c *Client) DeleteSite(ctx context.Context, siteID int) error {
	_, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/site/%d", siteID), nil)
	return err
}

// --- Domains ---

// Domain represents a Pangolin domain.
type Domain struct {
	DomainID   string `json:"domainId"`
	BaseDomain string `json:"baseDomain"`
	Verified   bool   `json:"verified"`
	Type       string `json:"type"`
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

// Target represents a backend target for a resource.
type Target struct {
	TargetID   int    `json:"targetId"`
	ResourceID int    `json:"resourceId"`
	SiteID     int    `json:"siteId"`
	IP         string `json:"ip"`
	Method     string `json:"method"`
	Port       int    `json:"port"`
	Enabled    bool   `json:"enabled"`
}

// CreateTargetRequest is the payload for creating a target.
type CreateTargetRequest struct {
	IP     string `json:"ip"`
	Port   int    `json:"port"`
	Method string `json:"method"`
	SiteID int    `json:"siteId"`
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

// UpdateTargetRequest is the payload for updating a target.
type UpdateTargetRequest struct {
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Method  string `json:"method"`
	Enabled bool   `json:"enabled"`
	SiteID  int    `json:"siteId"`
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
type Role struct {
	RoleID      int    `json:"roleId"`
	Name        string `json:"name"`
	Description string `json:"description"`
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

// User represents a Pangolin user.
type User struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
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

// CreateRoleRequest is the payload for creating a role.
type CreateRoleRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
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

// UpdateRoleRequest is the payload for updating a role.
type UpdateRoleRequest struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
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

// UpdateOrgRequest is the payload for updating an organization.
type UpdateOrgRequest struct {
	Name string `json:"name"`
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
type IDP struct {
	IDPId              int    `json:"idpId"`
	Name               string `json:"name"`
	Type               string `json:"type"`
	AutoProvision      bool   `json:"autoProvision"`
	Tags               string `json:"tags"`
	DefaultRoleMapping string `json:"defaultRoleMapping"`
	DefaultOrgMapping  string `json:"defaultOrgMapping"`
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

// GetDomainByID retrieves a domain by ID (via list + filter).
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
