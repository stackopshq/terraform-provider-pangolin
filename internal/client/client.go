package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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

// NewClient creates a new Pangolin API client.
func NewClient(baseURL, apiKey, orgID string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		OrgID:   orgID,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// doRequest performs an HTTP request and returns the parsed API response.
func (c *Client) doRequest(method, path string, body interface{}) (*APIResponse, error) {
	url := fmt.Sprintf("%s/v1%s", c.BaseURL, path)

	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonBody)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response (status %d): %s", resp.StatusCode, string(respBody))
	}

	if apiResp.Error || resp.StatusCode >= 400 {
		return &apiResp, fmt.Errorf("API error (status %d): %s", resp.StatusCode, apiResp.Message)
	}

	return &apiResp, nil
}

// --- Site Defaults ---

// SiteDefaults represents the response from pick-site-defaults.
type SiteDefaults struct {
	NewtID        string `json:"newtId"`
	NewtSecret    string `json:"newtSecret"`
	ClientAddress string `json:"clientAddress"`
}

// GetSiteDefaults picks site defaults for creating a new site.
func (c *Client) GetSiteDefaults() (*SiteDefaults, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/org/%s/pick-site-defaults", c.OrgID), nil)
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
	SiteID  int    `json:"siteId"`
	NiceID  string `json:"niceId"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Online  bool   `json:"online"`
	Address string `json:"address"`
}

// CreateSiteRequest is the payload for creating a site.
type CreateSiteRequest struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	NewtID        string `json:"newtId"`
	Secret        string `json:"secret"`
	Address       string `json:"address"`
	ClientAddress string `json:"clientAddress"`
}

// CreateSite creates a new site in the organization.
func (c *Client) CreateSite(req *CreateSiteRequest) (*Site, error) {
	resp, err := c.doRequest("POST", fmt.Sprintf("/org/%s/site", c.OrgID), req)
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
func (c *Client) GetSite(siteID int) (*Site, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/site/%d", siteID), nil)
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
func (c *Client) DeleteSite(siteID int) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/site/%d", siteID), nil)
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
func (c *Client) ListDomains() ([]Domain, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/org/%s/domains", c.OrgID), nil)
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
	ResourceID int    `json:"resourceId"`
	NiceID     string `json:"niceId"`
	Name       string `json:"name"`
	Subdomain  string `json:"subdomain"`
	FullDomain string `json:"fullDomain"`
	DomainID   string `json:"domainId"`
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
func (c *Client) CreateResource(req *CreateResourceRequest) (*Resource, error) {
	resp, err := c.doRequest("POST", fmt.Sprintf("/org/%s/resource", c.OrgID), req)
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
func (c *Client) GetResource(resourceID int) (*Resource, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/resource/%d", resourceID), nil)
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
func (c *Client) DeleteResource(resourceID int) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/resource/%d", resourceID), nil)
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
}

// CreateTargetRequest is the payload for creating a target.
type CreateTargetRequest struct {
	IP     string `json:"ip"`
	Port   int    `json:"port"`
	Method string `json:"method"`
	SiteID int    `json:"siteId"`
}

// CreateTarget creates a new target for a resource.
func (c *Client) CreateTarget(resourceID int, req *CreateTargetRequest) (*Target, error) {
	resp, err := c.doRequest("POST", fmt.Sprintf("/resource/%d/target", resourceID), req)
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
func (c *Client) GetTarget(targetID int) (*Target, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/target/%d", targetID), nil)
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
func (c *Client) DeleteTarget(targetID int) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/target/%d", targetID), nil)
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
	Name           string `json:"name"`
	SiteID         int    `json:"siteId"`
	Mode           string `json:"mode"`
	Destination    string `json:"destination"`
	Alias          string `json:"alias"`
	TCPPortRange   string `json:"tcpPortRangeString"`
	UDPPortRange   string `json:"udpPortRangeString"`
	DisableICMP    bool   `json:"disableIcmp"`
	AuthDaemonMode string `json:"authDaemonMode"`
	RoleIDs        []int  `json:"roleIds"`
	UserIDs        []string `json:"userIds"`
}

// CreateSiteResource creates a new private site resource.
func (c *Client) CreateSiteResource(req *CreateSiteResourceRequest) (*SiteResource, error) {
	resp, err := c.doRequest("POST", fmt.Sprintf("/org/%s/site-resource", c.OrgID), req)
	if err != nil {
		return nil, err
	}

	var siteResource SiteResource
	if err := json.Unmarshal(resp.Data, &siteResource); err != nil {
		return nil, fmt.Errorf("failed to parse site resource: %w", err)
	}
	return &siteResource, nil
}

// GetSiteResource retrieves a site resource by ID.
func (c *Client) GetSiteResource(siteResourceID int) (*SiteResource, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/site-resource/%d", siteResourceID), nil)
	if err != nil {
		return nil, err
	}

	var siteResource SiteResource
	if err := json.Unmarshal(resp.Data, &siteResource); err != nil {
		return nil, fmt.Errorf("failed to parse site resource: %w", err)
	}
	return &siteResource, nil
}

// DeleteSiteResource deletes a site resource by ID.
func (c *Client) DeleteSiteResource(siteResourceID int) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/site-resource/%d", siteResourceID), nil)
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
func (c *Client) ListRoles() ([]Role, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/org/%s/roles", c.OrgID), nil)
	if err != nil {
		return nil, err
	}

	var rolesResp RolesResponse
	if err := json.Unmarshal(resp.Data, &rolesResp); err != nil {
		return nil, fmt.Errorf("failed to parse roles: %w", err)
	}
	return rolesResp.Roles, nil
}

// AddRoleToResource assigns a role to an HTTP resource.
func (c *Client) AddRoleToResource(resourceID, roleID int) error {
	body := map[string]int{"roleId": roleID}
	_, err := c.doRequest("POST", fmt.Sprintf("/resource/%d/roles/add", resourceID), body)
	return err
}

// RemoveRoleFromResource removes a role from an HTTP resource.
func (c *Client) RemoveRoleFromResource(resourceID, roleID int) error {
	body := map[string]int{"roleId": roleID}
	_, err := c.doRequest("POST", fmt.Sprintf("/resource/%d/roles/remove", resourceID), body)
	return err
}

// AddUserToResource assigns a user to an HTTP resource.
func (c *Client) AddUserToResource(resourceID int, userID string) error {
	body := map[string]string{"userId": userID}
	_, err := c.doRequest("POST", fmt.Sprintf("/resource/%d/users/add", resourceID), body)
	return err
}

// RemoveUserFromResource removes a user from an HTTP resource.
func (c *Client) RemoveUserFromResource(resourceID int, userID string) error {
	body := map[string]string{"userId": userID}
	_, err := c.doRequest("POST", fmt.Sprintf("/resource/%d/users/remove", resourceID), body)
	return err
}

// AddRoleToSiteResource assigns a role to a private site resource.
func (c *Client) AddRoleToSiteResource(siteResourceID, roleID int) error {
	body := map[string]int{"roleId": roleID}
	_, err := c.doRequest("POST", fmt.Sprintf("/site-resource/%d/roles/add", siteResourceID), body)
	return err
}

// RemoveRoleFromSiteResource removes a role from a private site resource.
func (c *Client) RemoveRoleFromSiteResource(siteResourceID, roleID int) error {
	body := map[string]int{"roleId": roleID}
	_, err := c.doRequest("POST", fmt.Sprintf("/site-resource/%d/roles/remove", siteResourceID), body)
	return err
}

// AddUserToSiteResource assigns a user to a private site resource.
func (c *Client) AddUserToSiteResource(siteResourceID int, userID string) error {
	body := map[string]string{"userId": userID}
	_, err := c.doRequest("POST", fmt.Sprintf("/site-resource/%d/users/add", siteResourceID), body)
	return err
}

// RemoveUserFromSiteResource removes a user from a private site resource.
func (c *Client) RemoveUserFromSiteResource(siteResourceID int, userID string) error {
	body := map[string]string{"userId": userID}
	_, err := c.doRequest("POST", fmt.Sprintf("/site-resource/%d/users/remove", siteResourceID), body)
	return err
}

// --- Users ---

// User represents a Pangolin user.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// UsersResponse wraps the users list response.
type UsersResponse struct {
	Users []User `json:"users"`
}

// ListUsers retrieves all users for the organization.
func (c *Client) ListUsers() ([]User, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/org/%s/users", c.OrgID), nil)
	if err != nil {
		return nil, err
	}

	var usersResp UsersResponse
	if err := json.Unmarshal(resp.Data, &usersResp); err != nil {
		return nil, fmt.Errorf("failed to parse users: %w", err)
	}
	return usersResp.Users, nil
}
