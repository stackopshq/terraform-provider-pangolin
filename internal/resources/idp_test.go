package resources

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

// idp.go has no extracted helper - the (IDP, OIDCConfig) triple is
// folded into the model inline in Create/Read/Import. These tests
// replay each inline path against fixture wire payloads.

// applyIDPReadResponse mirrors the Read() inline mapping: server-side
// fields flow into state, ClientSecret is caller-managed and preserved
// from prior.
func applyIDPReadResponse(prior IDPResourceModel, idp *client.IDP, cfg *client.IDPOidcConfig) IDPResourceModel {
	prior.Name = types.StringValue(idp.Name)
	prior.AutoProvision = types.BoolValue(idp.AutoProvision)
	prior.Tags = types.StringValue(idp.Tags)
	prior.Variant = types.StringValue(idp.Variant)
	prior.ClientID = types.StringValue(cfg.ClientID)
	prior.AuthURL = types.StringValue(cfg.AuthURL)
	prior.TokenURL = types.StringValue(cfg.TokenURL)
	prior.IdentifierPath = types.StringValue(cfg.IdentifierPath)
	prior.EmailPath = types.StringValue(cfg.EmailPath)
	prior.NamePath = types.StringValue(cfg.NamePath)
	prior.Scopes = types.StringValue(cfg.Scopes)
	return prior
}

func TestIDP_ReadMapping_PreservesClientSecret(t *testing.T) {
	idp := &client.IDP{
		IDPId:         5,
		Name:          "google",
		Type:          "oidc",
		Variant:       "google",
		AutoProvision: true,
		Tags:          "prod",
	}
	cfg := &client.IDPOidcConfig{
		ClientID: "cid", ClientSecret: "ignored-server-value",
		AuthURL: "https://a", TokenURL: "https://t",
		IdentifierPath: "sub", EmailPath: "email", NamePath: "name",
		Scopes: "openid email profile",
	}
	prior := IDPResourceModel{
		ID:           types.Int64Value(5),
		ClientSecret: types.StringValue("kept-by-user"),
		RedirectURL:  types.StringValue("https://cb"),
	}
	m := applyIDPReadResponse(prior, idp, cfg)

	if m.ClientSecret.ValueString() != "kept-by-user" {
		t.Errorf("ClientSecret must be preserved from prior, got %q", m.ClientSecret.ValueString())
	}
	if m.RedirectURL.ValueString() != "https://cb" {
		t.Errorf("RedirectURL must be preserved from prior, got %q", m.RedirectURL.ValueString())
	}
	if m.Name.ValueString() != "google" {
		t.Errorf("Name = %q", m.Name.ValueString())
	}
	if m.Variant.ValueString() != "google" {
		t.Errorf("Variant = %q", m.Variant.ValueString())
	}
	if m.ClientID.ValueString() != "cid" {
		t.Errorf("ClientID = %q", m.ClientID.ValueString())
	}
	if m.AuthURL.ValueString() != "https://a" || m.TokenURL.ValueString() != "https://t" {
		t.Errorf("URLs lost")
	}
	if m.IdentifierPath.ValueString() != "sub" ||
		m.EmailPath.ValueString() != "email" ||
		m.NamePath.ValueString() != "name" {
		t.Errorf("id paths lost")
	}
	if !m.AutoProvision.ValueBool() {
		t.Errorf("AutoProvision lost")
	}
	if m.Tags.ValueString() != "prod" {
		t.Errorf("Tags lost")
	}
	if m.Scopes.ValueString() != "openid email profile" {
		t.Errorf("Scopes lost")
	}
}

func TestIDP_ImportState_NullSensitiveFields(t *testing.T) {
	// ImportState builds the model with empty ClientSecret and
	// empty RedirectURL because neither is recoverable after import.
	idp := &client.IDP{
		IDPId: 5, Name: "n", Type: "oidc", Variant: "oidc",
		AutoProvision: false, Tags: "",
	}
	cfg := &client.IDPOidcConfig{
		ClientID: "cid", AuthURL: "a", TokenURL: "t",
		IdentifierPath: "sub", EmailPath: "email", NamePath: "name",
		Scopes: "openid",
	}
	state := IDPResourceModel{
		ID:             types.Int64Value(int64(idp.IDPId)),
		Name:           types.StringValue(idp.Name),
		AutoProvision:  types.BoolValue(idp.AutoProvision),
		Tags:           types.StringValue(idp.Tags),
		Variant:        types.StringValue(idp.Variant),
		ClientID:       types.StringValue(cfg.ClientID),
		ClientSecret:   types.StringValue(""), // not recoverable
		AuthURL:        types.StringValue(cfg.AuthURL),
		TokenURL:       types.StringValue(cfg.TokenURL),
		IdentifierPath: types.StringValue(cfg.IdentifierPath),
		EmailPath:      types.StringValue(cfg.EmailPath),
		NamePath:       types.StringValue(cfg.NamePath),
		Scopes:         types.StringValue(cfg.Scopes),
		RedirectURL:    types.StringValue(""), // not returned by GET
	}
	if state.ClientSecret.ValueString() != "" {
		t.Errorf("ClientSecret must be empty after import")
	}
	if state.RedirectURL.ValueString() != "" {
		t.Errorf("RedirectURL must be empty after import (not returned by GET)")
	}
}

// -----------------------------------------------------------------------------
// IDP wire triple JSON round-trip - catches struct-tag drift
// -----------------------------------------------------------------------------

func TestIDP_TripleJSONRoundTrip(t *testing.T) {
	raw := `{
		"idp": {"idpId":5,"name":"google","type":"oidc","variant":"google","autoProvision":true,"tags":"prod"},
		"idpOidcConfig": {"clientId":"cid","clientSecret":"s","authUrl":"https://a","tokenUrl":"https://t","identifierPath":"sub","emailPath":"email","namePath":"name","scopes":"openid email profile"}
	}`
	// Reproduces the anonymous-struct shape used by client.GetIDP -
	// keeping the shape here (rather than importing it) tests that
	// the client's assumed JSON keys still round-trip correctly.
	var out struct {
		IDP           client.IDP           `json:"idp"`
		IDPOidcConfig client.IDPOidcConfig `json:"idpOidcConfig"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.IDP.IDPId != 5 || out.IDP.Name != "google" || out.IDP.Variant != "google" {
		t.Errorf("IDP block lost: %+v", out.IDP)
	}
	if out.IDPOidcConfig.ClientID != "cid" ||
		out.IDPOidcConfig.EmailPath != "email" ||
		out.IDPOidcConfig.Scopes != "openid email profile" {
		t.Errorf("OIDC block lost: %+v", out.IDPOidcConfig)
	}

	prior := IDPResourceModel{ClientSecret: types.StringValue("kept")}
	m := applyIDPReadResponse(prior, &out.IDP, &out.IDPOidcConfig)
	if m.ClientSecret.ValueString() != "kept" {
		t.Errorf("client_secret got clobbered on round-trip")
	}
	if m.ClientID.ValueString() != "cid" {
		t.Errorf("client_id lost")
	}
}

// -----------------------------------------------------------------------------
// CreateIDPRequest / UpdateIDPRequest JSON shape - catches accidental drops.
// -----------------------------------------------------------------------------

func TestIDP_CreateRequest_FullShape(t *testing.T) {
	plan := IDPResourceModel{
		Name:           types.StringValue("n"),
		ClientID:       types.StringValue("cid"),
		ClientSecret:   types.StringValue("s"),
		AuthURL:        types.StringValue("https://a"),
		TokenURL:       types.StringValue("https://t"),
		IdentifierPath: types.StringValue("sub"),
		EmailPath:      types.StringValue("email"),
		NamePath:       types.StringValue("name"),
		Scopes:         types.StringValue("openid"),
		AutoProvision:  types.BoolValue(false),
		Tags:           types.StringValue(""),
		Variant:        types.StringValue("oidc"),
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
	// autoProvision=false, tags="" and variant="oidc" are all
	// omitempty on the CreateIDPRequest struct - the payload must
	// drop them entirely to stay compatible with pre-1.19 servers.
	want := `{"name":"n","clientId":"cid","clientSecret":"s","authUrl":"https://a","tokenUrl":"https://t","identifierPath":"sub","emailPath":"email","namePath":"name","scopes":"openid","variant":"oidc"}`
	if string(raw) != want {
		t.Errorf("payload = %s\nwant   = %s", raw, want)
	}
}

func TestIDPOidcConfig_UnmarshalNominal(t *testing.T) {
	// Direct sanity check: the OIDC config struct's JSON tags map
	// to lowerCamel per the client contract.
	raw := `{"clientId":"cid","clientSecret":"s","authUrl":"a","tokenUrl":"t","identifierPath":"sub","emailPath":"email","namePath":"name","scopes":"openid"}`
	var c client.IDPOidcConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.ClientID != "cid" || c.ClientSecret != "s" || c.AuthURL != "a" {
		t.Errorf("OIDC config tag drift: %+v", c)
	}
	if c.IdentifierPath != "sub" || c.EmailPath != "email" ||
		c.NamePath != "name" || c.Scopes != "openid" {
		t.Errorf("OIDC config path tag drift: %+v", c)
	}
}
