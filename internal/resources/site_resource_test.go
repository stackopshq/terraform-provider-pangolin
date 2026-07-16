package resources

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

// -----------------------------------------------------------------------------
// hydrateSiteResourceState: wire -> state
// -----------------------------------------------------------------------------

func TestHydrateSiteResourceState_HTTPMode_ListResponse(t *testing.T) {
	// GET /org/{org}/site-resources (LIST) shape: carries SiteIDs, the
	// scalars are all populated. HTTP mode also fills scheme /
	// domainId / subdomain / fullDomain / destinationPort.
	siteRes := &client.SiteResource{
		SiteResourceID:   17,
		OrgID:            "org1",
		NiceID:           "nice-17",
		Name:             "http-svc",
		Mode:             "http",
		Destination:      "backend.local",
		Alias:            "",
		TCPPortRange:     "443,80",
		UDPPortRange:     "",
		DisableICMP:      true,
		AuthDaemonPort:   0,
		AuthDaemonMode:   "site",
		PamMode:          "",
		Enabled:          true,
		SSL:              true,
		NetworkID:        3,
		Scheme:           strPtr("https"),
		ProxyPort:        intPtr(8443),
		DestinationPort:  intPtr(8080),
		AliasAddress:     nil,
		DomainID:         strPtr("dom-1"),
		Subdomain:        strPtr("api"),
		FullDomain:       strPtr("api.example.com"),
		DefaultNetworkID: intPtr(2),
		SiteIDs:          []int{5, 6},
	}
	m := SitePrivateResourceModel{}
	hydrateSiteResourceState(&m, siteRes)

	if got := m.NiceID.ValueString(); got != "nice-17" {
		t.Errorf("NiceID = %q", got)
	}
	if got := m.Name.ValueString(); got != "http-svc" {
		t.Errorf("Name = %q", got)
	}
	if got := m.Mode.ValueString(); got != "http" {
		t.Errorf("Mode = %q", got)
	}
	if got := m.Destination.ValueString(); got != "backend.local" {
		t.Errorf("Destination = %q", got)
	}
	if got := m.Alias.ValueString(); got != "" {
		t.Errorf("Alias = %q, want empty", got)
	}
	if got := m.TCPPortRange.ValueString(); got != "443,80" {
		t.Errorf("TCPPortRange = %q", got)
	}
	if !m.DisableICMP.ValueBool() {
		t.Errorf("DisableICMP expected true")
	}
	if !m.Enabled.ValueBool() || !m.SSL.ValueBool() {
		t.Errorf("Enabled/SSL expected true")
	}
	if got := m.NetworkID.ValueInt64(); got != 3 {
		t.Errorf("NetworkID = %d", got)
	}
	if got := m.DefaultNetworkID.ValueInt64(); got != 2 {
		t.Errorf("DefaultNetworkID = %d", got)
	}
	// SiteID derived from SiteIDs[0].
	if got := m.SiteID.ValueInt64(); got != 5 {
		t.Errorf("SiteID = %d, want 5 (SiteIDs[0])", got)
	}
	if got := m.Scheme.ValueString(); got != "https" {
		t.Errorf("Scheme = %q", got)
	}
	if !m.AliasAddress.IsNull() {
		t.Errorf("AliasAddress expected null")
	}
	if got := m.DomainID.ValueString(); got != "dom-1" {
		t.Errorf("DomainID = %q", got)
	}
	if got := m.Subdomain.ValueString(); got != "api" {
		t.Errorf("Subdomain = %q", got)
	}
	if got := m.FullDomain.ValueString(); got != "api.example.com" {
		t.Errorf("FullDomain = %q", got)
	}
	if got := m.ProxyPort.ValueInt64(); got != 8443 {
		t.Errorf("ProxyPort = %d", got)
	}
	if got := m.DestinationPort.ValueInt64(); got != 8080 {
		t.Errorf("DestinationPort = %d", got)
	}
	if !m.PamMode.IsNull() {
		t.Errorf("PamMode expected null (empty on wire)")
	}
}

func TestHydrateSiteResourceState_L4Mode_CreateResponse(t *testing.T) {
	// CREATE / UPDATE shape: no SiteIDs slice, so state.SiteID stays
	// at its plan value (we simulate a plan-populated value here).
	siteRes := &client.SiteResource{
		SiteResourceID: 42,
		OrgID:          "org1",
		NiceID:         "nice-42",
		Name:           "tcp-svc",
		Mode:           "cidr",
		Destination:    "10.0.0.0/24",
		Alias:          "svc.internal",
		TCPPortRange:   "8000-8100",
		UDPPortRange:   "",
		DisableICMP:    false,
		AuthDaemonPort: 9000,
		AuthDaemonMode: "remote",
		PamMode:        "push",
		Enabled:        true,
		SSL:            false,
		NetworkID:      3,
	}
	// SiteID = 77 comes from the plan; hydrate must NOT overwrite it
	// when the response has no SiteIDs.
	m := SitePrivateResourceModel{SiteID: types.Int64Value(77)}
	hydrateSiteResourceState(&m, siteRes)

	if got := m.SiteID.ValueInt64(); got != 77 {
		t.Errorf("SiteID overwritten: got %d, want 77 (preserved from plan)", got)
	}
	if got := m.Mode.ValueString(); got != "cidr" {
		t.Errorf("Mode = %q", got)
	}
	if got := m.Alias.ValueString(); got != "svc.internal" {
		t.Errorf("Alias = %q", got)
	}
	if got := m.TCPPortRange.ValueString(); got != "8000-8100" {
		t.Errorf("TCPPortRange = %q", got)
	}
	if got := m.PamMode.ValueString(); got != "push" {
		t.Errorf("PamMode = %q", got)
	}
	if got := m.AuthDaemonMode.ValueString(); got != "remote" {
		t.Errorf("AuthDaemonMode = %q", got)
	}
	if got := m.AuthDaemonPort.ValueInt64(); got != 9000 {
		t.Errorf("AuthDaemonPort = %d", got)
	}
	// L4 mode leaves http-specific fields null.
	if !m.Scheme.IsNull() {
		t.Errorf("Scheme expected null for cidr mode")
	}
	if !m.DomainID.IsNull() || !m.Subdomain.IsNull() || !m.FullDomain.IsNull() {
		t.Errorf("HTTP-only fields expected null for cidr mode")
	}
	if !m.ProxyPort.IsNull() {
		t.Errorf("ProxyPort expected null")
	}
	if !m.DestinationPort.IsNull() {
		t.Errorf("DestinationPort expected null")
	}
	if !m.DefaultNetworkID.IsNull() {
		t.Errorf("DefaultNetworkID expected null")
	}
	if !m.AliasAddress.IsNull() {
		t.Errorf("AliasAddress expected null")
	}
}

func TestHydrateSiteResourceState_HostMode_AllNullPointers(t *testing.T) {
	// Pre-1.19 or minimal `host` mode payload: every optional pointer
	// is nil on the wire. State must surface those as TF null.
	siteRes := &client.SiteResource{
		SiteResourceID: 3,
		NiceID:         "nice-3",
		Name:           "host-svc",
		Mode:           "host",
		Destination:    "host.internal",
		Alias:          "svc",
		Enabled:        true,
	}
	m := SitePrivateResourceModel{}
	hydrateSiteResourceState(&m, siteRes)
	for name, v := range map[string]bool{
		"Scheme":           m.Scheme.IsNull(),
		"AliasAddress":     m.AliasAddress.IsNull(),
		"DomainID":         m.DomainID.IsNull(),
		"Subdomain":        m.Subdomain.IsNull(),
		"FullDomain":       m.FullDomain.IsNull(),
		"ProxyPort":        m.ProxyPort.IsNull(),
		"DestinationPort":  m.DestinationPort.IsNull(),
		"DefaultNetworkID": m.DefaultNetworkID.IsNull(),
		"PamMode":          m.PamMode.IsNull(),
	} {
		if !v {
			t.Errorf("%s expected null", name)
		}
	}
}

// -----------------------------------------------------------------------------
// Round-trip via JSON to catch struct-tag drift between wire and Go.
// -----------------------------------------------------------------------------

func TestSiteResource_JSONRoundTrip(t *testing.T) {
	// Emulate a full LIST payload the way Pangolin emits it: notice
	// tcpPortRangeString / udpPortRangeString / disableIcmp / etc.
	raw := `{
		"siteResourceId": 17,
		"orgId": "org1",
		"niceId": "nice-17",
		"name": "http-svc",
		"mode": "http",
		"destination": "backend.local",
		"alias": "",
		"tcpPortRangeString": "443,80",
		"udpPortRangeString": "",
		"disableIcmp": true,
		"authDaemonPort": 0,
		"authDaemonMode": "site",
		"pamMode": "",
		"enabled": true,
		"ssl": true,
		"networkId": 3,
		"scheme": "https",
		"proxyPort": 8443,
		"destinationPort": 8080,
		"aliasAddress": null,
		"domainId": "dom-1",
		"subdomain": "api",
		"fullDomain": "api.example.com",
		"defaultNetworkId": 2,
		"siteIds": [5,6],
		"siteNames": ["a","b"],
		"siteNiceIds": ["s-a","s-b"],
		"siteAddresses": ["1.1.1.1","2.2.2.2"],
		"siteOnlines": [true,false]
	}`
	var sr client.SiteResource
	if err := json.Unmarshal([]byte(raw), &sr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sr.TCPPortRange != "443,80" {
		t.Errorf("TCPPortRange tag broken: %q", sr.TCPPortRange)
	}
	if sr.UDPPortRange != "" {
		t.Errorf("UDPPortRange tag broken: %q", sr.UDPPortRange)
	}
	if !sr.DisableICMP {
		t.Errorf("DisableICMP tag broken")
	}
	if len(sr.SiteIDs) != 2 || sr.SiteIDs[0] != 5 {
		t.Errorf("SiteIDs tag broken: %v", sr.SiteIDs)
	}

	m := SitePrivateResourceModel{}
	hydrateSiteResourceState(&m, &sr)
	if m.SiteID.ValueInt64() != 5 {
		t.Errorf("SiteID from list not derived: %d", m.SiteID.ValueInt64())
	}
	if m.Scheme.ValueString() != "https" {
		t.Errorf("Scheme lost across round-trip")
	}
	if m.FullDomain.ValueString() != "api.example.com" {
		t.Errorf("FullDomain lost across round-trip")
	}
}
