package resources

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

// -----------------------------------------------------------------------------
// hydrateOrgIDPState: (IDP, OIDC config, per-org binding) triplet -> state
// -----------------------------------------------------------------------------

func TestHydrateOrgIDPState_Full(t *testing.T) {
	detail := &client.OrgIDPDetail{
		IDP: client.IDP{
			IDPId:         5,
			Name:          "google",
			Type:          "oidc",
			Variant:       "google",
			AutoProvision: true,
			Tags:          "prod",
		},
		IDPOidcConfig: client.IDPOidcConfig{
			ClientID:       "cid",
			ClientSecret:   "secret", // API sometimes returns it - we ignore.
			AuthURL:        "https://a",
			TokenURL:       "https://t",
			IdentifierPath: "sub",
			EmailPath:      "email",
			NamePath:       "name",
			Scopes:         "openid email profile",
		},
		IDPOrg: client.OrgIDPBindRow{
			IDPId:       5,
			OrgID:       "org1",
			RoleMapping: strPtr(`{"admin":"grp-admin"}`),
			OrgMapping:  strPtr(`{"acme":"org1"}`),
		},
	}
	// ClientSecret and RedirectURL are caller-managed; leave the
	// existing state untouched. Seed a value we can assert survives.
	state := OrgIDPResourceModel{
		ClientSecret: types.StringValue("kept-by-user"),
		RedirectURL:  types.StringValue("https://callback"),
	}
	hydrateOrgIDPState(&state, detail)

	if state.Name.ValueString() != "google" {
		t.Errorf("Name = %q", state.Name.ValueString())
	}
	if !state.AutoProvision.ValueBool() {
		t.Errorf("AutoProvision")
	}
	if state.Tags.ValueString() != "prod" {
		t.Errorf("Tags = %q", state.Tags.ValueString())
	}
	if state.Variant.ValueString() != "google" {
		t.Errorf("Variant = %q", state.Variant.ValueString())
	}
	if state.ClientID.ValueString() != "cid" {
		t.Errorf("ClientID = %q", state.ClientID.ValueString())
	}
	if state.AuthURL.ValueString() != "https://a" {
		t.Errorf("AuthURL = %q", state.AuthURL.ValueString())
	}
	if state.TokenURL.ValueString() != "https://t" {
		t.Errorf("TokenURL = %q", state.TokenURL.ValueString())
	}
	if state.IdentifierPath.ValueString() != "sub" {
		t.Errorf("IdentifierPath = %q", state.IdentifierPath.ValueString())
	}
	if state.EmailPath.ValueString() != "email" || state.NamePath.ValueString() != "name" {
		t.Errorf("email/name paths wrong")
	}
	if state.Scopes.ValueString() != "openid email profile" {
		t.Errorf("Scopes = %q", state.Scopes.ValueString())
	}
	if state.RoleMapping.ValueString() != `{"admin":"grp-admin"}` {
		t.Errorf("RoleMapping = %q", state.RoleMapping.ValueString())
	}
	if state.OrgMapping.ValueString() != `{"acme":"org1"}` {
		t.Errorf("OrgMapping = %q", state.OrgMapping.ValueString())
	}
	// Caller-managed fields must be untouched.
	if state.ClientSecret.ValueString() != "kept-by-user" {
		t.Errorf("ClientSecret must not be overwritten by hydrate, got %q", state.ClientSecret.ValueString())
	}
	if state.RedirectURL.ValueString() != "https://callback" {
		t.Errorf("RedirectURL must not be overwritten by hydrate, got %q", state.RedirectURL.ValueString())
	}
}

func TestHydrateOrgIDPState_NullRoleAndOrgMapping(t *testing.T) {
	// The per-org binding block emits `null` for unset mappings.
	// tfconv.StringFromPtr must surface those as TF null.
	detail := &client.OrgIDPDetail{
		IDP: client.IDP{IDPId: 1, Name: "n", Type: "oidc"},
		IDPOidcConfig: client.IDPOidcConfig{
			ClientID: "c", AuthURL: "a", TokenURL: "t", IdentifierPath: "sub",
			Scopes: "openid",
		},
		IDPOrg: client.OrgIDPBindRow{
			IDPId: 1, OrgID: "org1",
			RoleMapping: nil,
			OrgMapping:  nil,
		},
	}
	state := OrgIDPResourceModel{}
	hydrateOrgIDPState(&state, detail)
	if !state.RoleMapping.IsNull() {
		t.Errorf("RoleMapping expected null")
	}
	if !state.OrgMapping.IsNull() {
		t.Errorf("OrgMapping expected null")
	}
}

// -----------------------------------------------------------------------------
// OrgIDPDetail JSON shape sanity: catch struct-tag drift on the triple
// -----------------------------------------------------------------------------

func TestOrgIDPDetail_JSONRoundTrip(t *testing.T) {
	raw := `{
		"idp": {"idpId":5,"name":"google","type":"oidc","variant":"google","autoProvision":true,"tags":"prod"},
		"idpOidcConfig": {"clientId":"cid","clientSecret":"s","authUrl":"https://a","tokenUrl":"https://t","identifierPath":"sub","emailPath":"email","namePath":"name","scopes":"openid email profile"},
		"idpOrg": {"idpId":5,"orgId":"org1","roleMapping":"r","orgMapping":"o"}
	}`
	var d client.OrgIDPDetail
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.IDP.IDPId != 5 || d.IDP.Variant != "google" {
		t.Errorf("IDP block lost: %+v", d.IDP)
	}
	if d.IDPOidcConfig.ClientID != "cid" || d.IDPOidcConfig.EmailPath != "email" {
		t.Errorf("OIDC block lost: %+v", d.IDPOidcConfig)
	}
	if d.IDPOrg.OrgID != "org1" || d.IDPOrg.RoleMapping == nil || *d.IDPOrg.RoleMapping != "r" {
		t.Errorf("IDPOrg block lost: %+v", d.IDPOrg)
	}
	state := OrgIDPResourceModel{}
	hydrateOrgIDPState(&state, &d)
	if state.RoleMapping.ValueString() != "r" || state.OrgMapping.ValueString() != "o" {
		t.Errorf("mappings not surfaced after hydrate: %+v / %+v", state.RoleMapping, state.OrgMapping)
	}
}

// -----------------------------------------------------------------------------
// CreateIDPRequest build (used by Create() / Update()) - shape check
// -----------------------------------------------------------------------------

func TestOrgIDP_BuildCreateRequestFromModel(t *testing.T) {
	// This mirrors the inline mapping in Create() so we detect any
	// field the resource might silently drop.
	plan := OrgIDPResourceModel{
		Name:           types.StringValue("n"),
		ClientID:       types.StringValue("cid"),
		ClientSecret:   types.StringValue("secret"),
		AuthURL:        types.StringValue("https://a"),
		TokenURL:       types.StringValue("https://t"),
		IdentifierPath: types.StringValue("sub"),
		EmailPath:      types.StringValue("email"),
		NamePath:       types.StringValue("name"),
		Scopes:         types.StringValue("openid email"),
		AutoProvision:  types.BoolValue(true),
		Tags:           types.StringValue("prod"),
		Variant:        types.StringValue("google"),
	}
	req := &client.CreateIDPRequest{
		Name:           plan.Name.ValueString(),
		ClientID:       plan.ClientID.ValueString(),
		ClientSecret:   plan.ClientSecret.ValueString(),
		AuthURL:        plan.AuthURL.ValueString(),
		TokenURL:       plan.TokenURL.ValueString(),
		IdentifierPath: plan.IdentifierPath.ValueString(),
		EmailPath:      plan.EmailPath.ValueString(),
		NamePath:       plan.NamePath.ValueString(),
		Scopes:         plan.Scopes.ValueString(),
		AutoProvision:  plan.AutoProvision.ValueBool(),
		Tags:           plan.Tags.ValueString(),
		Variant:        plan.Variant.ValueString(),
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The exact key order is stable for a struct literal marshal.
	want := `{"name":"n","clientId":"cid","clientSecret":"secret","authUrl":"https://a","tokenUrl":"https://t","identifierPath":"sub","emailPath":"email","namePath":"name","scopes":"openid email","autoProvision":true,"tags":"prod","variant":"google"}`
	if string(raw) != want {
		t.Errorf("payload = %s", raw)
	}
}
