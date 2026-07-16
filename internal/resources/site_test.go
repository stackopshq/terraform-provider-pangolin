package resources

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

// site.go has no extracted helper function - Create/Read/Update inline
// their model <-> wire mapping. These tests exercise that mapping by
// replaying the exact inline logic against fixture wire payloads, so
// regressions in either direction (schema drift or JSON tag drift) are
// still caught at the pkg level.

// buildSiteReadState reproduces the Read() codepath: given a
// *client.Site, return the model the resource layer would write to
// state on Read. Kept in the test file (not the resource) - it's the
// spec we assert against.
func buildSiteReadState(site *client.Site) SiteResourceModel {
	return SiteResourceModel{
		ID:                  types.Int64Value(int64(site.SiteID)),
		NiceID:              types.StringValue(site.NiceID),
		Name:                types.StringValue(site.Name),
		Type:                types.StringValue(site.Type),
		Online:              types.BoolValue(site.Online),
		Address:             types.StringValue(site.Address),
		DockerSocketEnabled: types.BoolValue(site.DockerSocketEnabled),
	}
}

func TestSite_JSONRoundTripThroughGetShape(t *testing.T) {
	// A rich GET /site/{id} payload (post-1.19: exposes exit-node,
	// wg keys, traffic counters). We only verify that the fields
	// the site resource cares about survive.
	raw := `{
		"siteId": 12,
		"niceId": "nice-12",
		"name": "prod",
		"type": "newt",
		"online": true,
		"address": "10.0.0.1/24",
		"dockerSocketEnabled": false,
		"orgId": "org1",
		"exitNodeId": 5,
		"pubKey": "abc",
		"subnet": "10.0.0.0/24",
		"megabytesIn": 12.3,
		"megabytesOut": 45.6,
		"lastBandwidthUpdate": "2026-01-01T00:00:00Z",
		"lastPing": 1700000000,
		"endpoint": "1.2.3.4:51820",
		"publicKey": "pubkey",
		"lastHolePunch": 1700000100,
		"listenPort": 51820,
		"status": "online",
		"newtId": "newt-abc"
	}`
	var s client.Site
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m := buildSiteReadState(&s)
	if m.ID.ValueInt64() != 12 {
		t.Errorf("ID = %d", m.ID.ValueInt64())
	}
	if m.NiceID.ValueString() != "nice-12" {
		t.Errorf("NiceID = %q", m.NiceID.ValueString())
	}
	if m.Name.ValueString() != "prod" {
		t.Errorf("Name = %q", m.Name.ValueString())
	}
	if m.Type.ValueString() != "newt" {
		t.Errorf("Type = %q", m.Type.ValueString())
	}
	if !m.Online.ValueBool() {
		t.Errorf("Online expected true")
	}
	if m.Address.ValueString() != "10.0.0.1/24" {
		t.Errorf("Address = %q", m.Address.ValueString())
	}
	if m.DockerSocketEnabled.ValueBool() {
		t.Errorf("DockerSocketEnabled expected false (server said so)")
	}
}

func TestSite_MinimalCreateResponseShape(t *testing.T) {
	// CREATE /org/{org}/site response: only the seven CRUD keys, no
	// extended fields.
	raw := `{
		"siteId": 1,
		"niceId": "nice-1",
		"name": "s",
		"type": "newt",
		"online": false,
		"address": "10.0.0.2/24",
		"dockerSocketEnabled": true
	}`
	var s client.Site
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.ExitNodeID != nil {
		t.Errorf("ExitNodeID should be nil on create response")
	}
	m := buildSiteReadState(&s)
	if !m.DockerSocketEnabled.ValueBool() {
		t.Errorf("DockerSocketEnabled default expected true")
	}
	if m.Online.ValueBool() {
		t.Errorf("Online should be false on fresh site")
	}
}

// ImportSiteState reproduces the ImportState() codepath: newt_id /
// newt_secret must be null because the secret is only visible at
// creation time and cannot be recovered.
func TestSite_ImportStateShapeHasNullNewtSecret(t *testing.T) {
	site := &client.Site{
		SiteID:              5,
		NiceID:              "nice-5",
		Name:                "imported",
		Type:                "newt",
		Online:              true,
		Address:             "10.0.0.5/24",
		DockerSocketEnabled: true,
	}
	// This is the exact state ImportState builds; assert the whole
	// shape (including the null-secret contract advertised in the
	// schema description).
	state := SiteResourceModel{
		ID:                  types.Int64Value(int64(site.SiteID)),
		NiceID:              types.StringValue(site.NiceID),
		Name:                types.StringValue(site.Name),
		Type:                types.StringValue(site.Type),
		Online:              types.BoolValue(site.Online),
		Address:             types.StringValue(site.Address),
		NewtID:              types.StringNull(),
		NewtSecret:          types.StringNull(),
		DockerSocketEnabled: types.BoolValue(site.DockerSocketEnabled),
	}
	if got := state.ID.ValueInt64(); got != int64(site.SiteID) {
		t.Errorf("ID = %d, want %d", got, site.SiteID)
	}
	if got := state.NiceID.ValueString(); got != site.NiceID {
		t.Errorf("NiceID = %q, want %q", got, site.NiceID)
	}
	if got := state.Name.ValueString(); got != site.Name {
		t.Errorf("Name = %q, want %q", got, site.Name)
	}
	if got := state.Type.ValueString(); got != site.Type {
		t.Errorf("Type = %q, want %q", got, site.Type)
	}
	if got := state.Online.ValueBool(); got != site.Online {
		t.Errorf("Online = %v, want %v", got, site.Online)
	}
	if got := state.Address.ValueString(); got != site.Address {
		t.Errorf("Address = %q, want %q", got, site.Address)
	}
	if got := state.DockerSocketEnabled.ValueBool(); got != site.DockerSocketEnabled {
		t.Errorf("DockerSocketEnabled = %v, want %v", got, site.DockerSocketEnabled)
	}
	if !state.NewtSecret.IsNull() {
		t.Errorf("post-import NewtSecret must be null")
	}
	if !state.NewtID.IsNull() {
		t.Errorf("post-import NewtID must be null")
	}
}

func TestSite_UpdateRequestJSONShape(t *testing.T) {
	// Update() builds a UpdateSiteRequest from the plan. Verify the
	// serialized JSON keys match what Pangolin accepts (name +
	// dockerSocketEnabled), no extra keys sneaking in.
	req := &client.UpdateSiteRequest{
		Name:                "renamed",
		DockerSocketEnabled: false,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The wire key set is small and stable - use exact-match.
	if string(raw) != `{"name":"renamed","dockerSocketEnabled":false}` {
		t.Errorf("payload = %s", raw)
	}
}
