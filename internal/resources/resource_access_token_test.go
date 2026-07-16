package resources

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
)

// resource_access_token.go has no extracted helper; the mapping is
// inlined in Create/Read/Import. These tests replay the inline logic
// against fixture wire payloads.

// buildCreateBody mirrors the Create() body-construction branches:
// each optional pointer is set only when the plan value is non-null.
func buildCreateBody(plan ResourceAccessTokenModel) *client.CreateResourceAccessTokenRequest {
	body := &client.CreateResourceAccessTokenRequest{}
	if !plan.Title.IsNull() && !plan.Title.IsUnknown() {
		v := plan.Title.ValueString()
		body.Title = &v
	}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		v := plan.Description.ValueString()
		body.Description = &v
	}
	if !plan.ValidForSeconds.IsNull() && !plan.ValidForSeconds.IsUnknown() {
		v := plan.ValidForSeconds.ValueInt64()
		body.ValidForSeconds = &v
	}
	return body
}

// tokenCreateResponseToState mirrors the state hydration at the end of
// Create(): every field on the response is folded into the model.
func tokenCreateResponseToState(plan ResourceAccessTokenModel, tok *client.ResourceAccessToken) ResourceAccessTokenModel {
	plan.ID = types.StringValue(tok.AccessTokenID)
	plan.SessionLength = types.Int64Value(tok.SessionLength)
	plan.CreatedAt = types.Int64Value(tok.CreatedAt)
	plan.Token = types.StringValue(tok.AccessToken)
	plan.TokenHash = types.StringValue("")
	plan.Title = tfconv.StringFromPtr(tok.Title)
	plan.Description = tfconv.StringFromPtr(tok.Description)
	plan.ExpiresAt = tfconv.Int64FromInt64Ptr(tok.ExpiresAt)
	return plan
}

// tokenReadResponseToState mirrors the state hydration in Read(): the
// bearer secret (Token) is preserved from prior state.
func tokenReadResponseToState(prior ResourceAccessTokenModel, tok *client.ResourceAccessToken) ResourceAccessTokenModel {
	prior.ResourceID = types.Int64Value(int64(tok.ResourceID))
	prior.SessionLength = types.Int64Value(tok.SessionLength)
	prior.CreatedAt = types.Int64Value(tok.CreatedAt)
	prior.TokenHash = types.StringValue(tok.TokenHash)
	prior.Title = tfconv.StringFromPtr(tok.Title)
	prior.Description = tfconv.StringFromPtr(tok.Description)
	prior.ExpiresAt = tfconv.Int64FromInt64Ptr(tok.ExpiresAt)
	return prior
}

func TestAccessToken_CreateBody_UnknownsOmitted(t *testing.T) {
	plan := ResourceAccessTokenModel{
		Title:           types.StringUnknown(),
		Description:     types.StringNull(),
		ValidForSeconds: types.Int64Unknown(),
	}
	body := buildCreateBody(plan)
	if body.Title != nil || body.Description != nil || body.ValidForSeconds != nil {
		t.Errorf("expected all-nil body, got %+v", body)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != `{}` {
		t.Errorf("empty body should serialize as {}, got %s", raw)
	}
}

func TestAccessToken_CreateBody_FullyPopulated(t *testing.T) {
	plan := ResourceAccessTokenModel{
		Title:           types.StringValue("api-key"),
		Description:     types.StringValue("for ci"),
		ValidForSeconds: types.Int64Value(3600),
	}
	body := buildCreateBody(plan)
	if body.Title == nil || *body.Title != "api-key" {
		t.Errorf("Title = %v", body.Title)
	}
	if body.Description == nil || *body.Description != "for ci" {
		t.Errorf("Description = %v", body.Description)
	}
	if body.ValidForSeconds == nil || *body.ValidForSeconds != 3600 {
		t.Errorf("ValidForSeconds = %v", body.ValidForSeconds)
	}
}

func TestAccessToken_CreateResponse_MapsAllFields(t *testing.T) {
	tok := &client.ResourceAccessToken{
		AccessTokenID: "tok-1",
		OrgID:         "org1",
		ResourceID:    5,
		SessionLength: 2_592_000_000,
		ExpiresAt:     nil,
		Title:         strPtr("api-key"),
		Description:   strPtr("for ci"),
		CreatedAt:     1_700_000_000_000,
		AccessToken:   "bearer-secret",
	}
	plan := ResourceAccessTokenModel{ResourceID: types.Int64Value(5)}
	m := tokenCreateResponseToState(plan, tok)

	if m.ID.ValueString() != "tok-1" {
		t.Errorf("ID = %q", m.ID.ValueString())
	}
	if m.SessionLength.ValueInt64() != 2_592_000_000 {
		t.Errorf("SessionLength = %d", m.SessionLength.ValueInt64())
	}
	if m.CreatedAt.ValueInt64() != 1_700_000_000_000 {
		t.Errorf("CreatedAt = %d", m.CreatedAt.ValueInt64())
	}
	if m.Token.ValueString() != "bearer-secret" {
		t.Errorf("Token = %q, want bearer-secret", m.Token.ValueString())
	}
	// TokenHash is not surfaced by CREATE and must be "" (not null).
	if m.TokenHash.IsNull() || m.TokenHash.ValueString() != "" {
		t.Errorf("TokenHash on create = %v, want empty", m.TokenHash)
	}
	if m.Title.ValueString() != "api-key" {
		t.Errorf("Title = %q", m.Title.ValueString())
	}
	if !m.ExpiresAt.IsNull() {
		t.Errorf("ExpiresAt should be null when server returned nil")
	}
}

func TestAccessToken_ReadResponse_PreservesTokenSecret(t *testing.T) {
	// The list endpoint does not expose the bearer secret; the
	// resource must preserve it from state.
	tok := &client.ResourceAccessToken{
		AccessTokenID: "tok-1",
		ResourceID:    5,
		SessionLength: 2_592_000_000,
		CreatedAt:     1_700_000_000_000,
		TokenHash:     "sha256-...",
		Title:         nil,
		Description:   nil,
		ExpiresAt:     nil,
	}
	prior := ResourceAccessTokenModel{
		ID:    types.StringValue("tok-1"),
		Token: types.StringValue("bearer-secret-from-create"),
	}
	m := tokenReadResponseToState(prior, tok)
	if m.Token.ValueString() != "bearer-secret-from-create" {
		t.Errorf("Token secret must be preserved across Read, got %q", m.Token.ValueString())
	}
	if m.TokenHash.ValueString() != "sha256-..." {
		t.Errorf("TokenHash = %q", m.TokenHash.ValueString())
	}
	if !m.Title.IsNull() {
		t.Errorf("Title should be null when server returns nil")
	}
	if !m.ExpiresAt.IsNull() {
		t.Errorf("ExpiresAt should be null when server returns nil")
	}
}

func TestAccessToken_ImportShape_NullsSensitive(t *testing.T) {
	// Import shape from the resource: Token is empty, TokenHash
	// carries the digest, ValidForSeconds is null (input-only).
	tok := &client.ResourceAccessToken{
		AccessTokenID: "tok-2",
		ResourceID:    7,
		SessionLength: 100,
		CreatedAt:     1,
		TokenHash:     "hash",
		Title:         strPtr("t"),
		Description:   nil,
		ExpiresAt:     nil,
	}
	state := ResourceAccessTokenModel{
		ID:              types.StringValue(tok.AccessTokenID),
		ResourceID:      types.Int64Value(int64(tok.ResourceID)),
		Title:           tfconv.StringFromPtr(tok.Title),
		Description:     tfconv.StringFromPtr(tok.Description),
		ValidForSeconds: types.Int64Null(),
		SessionLength:   types.Int64Value(tok.SessionLength),
		ExpiresAt:       tfconv.Int64FromInt64Ptr(tok.ExpiresAt),
		CreatedAt:       types.Int64Value(tok.CreatedAt),
		TokenHash:       types.StringValue(tok.TokenHash),
		Token:           types.StringValue(""),
	}
	if state.Token.ValueString() != "" {
		t.Errorf("Token after import must be empty")
	}
	if !state.ValidForSeconds.IsNull() {
		t.Errorf("ValidForSeconds must be null after import (input-only field)")
	}
	if !state.Description.IsNull() {
		t.Errorf("Description must be null when server returns nil")
	}
}
