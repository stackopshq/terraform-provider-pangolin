package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

func TestHydrateRedirectState_FullGetResponse(t *testing.T) {
	r := &client.Redirect{
		RedirectID:         7,
		NiceID:             "nice-7",
		Name:               "old-site",
		ResourceID:         intPtr(12),
		DestinationHost:    "https://example.com",
		PathMatchType:      "prefix",
		MatchPath:          strPtr("/old"),
		RewritePath:        strPtr("/new"),
		RewritePathType:    strPtr("prefix"),
		Priority:           intPtr(250),
		Permanent:          true,
		SSL:                true,
		Enabled:            true,
		ResourceFullDomain: strPtr("app.example.org"),
	}
	m := RedirectModel{}
	hydrateRedirectState(&m, r)

	if m.ID.ValueString() != "7" || m.NiceID.ValueString() != "nice-7" || m.Name.ValueString() != "old-site" {
		t.Errorf("identity fields wrong: %+v", m)
	}
	if m.DestinationHost.ValueString() != "https://example.com" {
		t.Errorf("DestinationHost = %q", m.DestinationHost.ValueString())
	}
	if m.ResourceID.ValueInt64() != 12 || !m.DomainID.IsNull() || !m.Subdomain.IsNull() {
		t.Errorf("attachment wrong: %+v", m)
	}
	if m.PathMatchType.ValueString() != "prefix" || m.MatchPath.ValueString() != "/old" ||
		m.RewritePath.ValueString() != "/new" || m.RewritePathType.ValueString() != "prefix" {
		t.Errorf("path fields wrong: %+v", m)
	}
	if m.Priority.ValueInt64() != 250 || !m.Permanent.ValueBool() || !m.SSL.ValueBool() || !m.Enabled.ValueBool() {
		t.Errorf("scalars wrong: %+v", m)
	}
	if m.ResourceFullDomain.ValueString() != "app.example.org" || !m.BaseDomain.IsNull() {
		t.Errorf("joined fields wrong: %+v", m)
	}
}

func TestHydrateRedirectState_BareCreateResponse_NullPriority(t *testing.T) {
	r := &client.Redirect{
		RedirectID:      3,
		NiceID:          "n",
		Name:            "r",
		DomainID:        strPtr("dom-1"),
		Subdomain:       strPtr("old"),
		DestinationHost: "https://example.com",
		PathMatchType:   "regex",
		SSL:             true,
		Enabled:         true,
	}
	m := RedirectModel{}
	hydrateRedirectState(&m, r)

	if !m.ResourceFullDomain.IsNull() || !m.BaseDomain.IsNull() {
		t.Errorf("joined fields should be null: %+v", m)
	}
	if m.Priority.ValueInt64() != 100 {
		t.Errorf("Priority = %d, want 100", m.Priority.ValueInt64())
	}
	if m.DomainID.ValueString() != "dom-1" || m.Subdomain.ValueString() != "old" || !m.ResourceID.IsNull() {
		t.Errorf("attachment wrong: %+v", m)
	}
	if !m.MatchPath.IsNull() || !m.RewritePath.IsNull() || !m.RewritePathType.IsNull() {
		t.Errorf("nullable path fields should be null: %+v", m)
	}
	if m.Permanent.ValueBool() {
		t.Errorf("Permanent should be false")
	}
}

func TestBuildRedirectUpdateRequest_ClearsNullableFields(t *testing.T) {
	plan := &RedirectModel{
		NiceID:          types.StringUnknown(),
		Name:            types.StringValue("r"),
		DestinationHost: types.StringValue("https://example.com"),
		ResourceID:      types.Int64Value(9),
		DomainID:        types.StringNull(),
		Subdomain:       types.StringNull(),
		PathMatchType:   types.StringValue("regex"),
		MatchPath:       types.StringNull(),
		RewritePath:     types.StringNull(),
		RewritePathType: types.StringNull(),
		Priority:        types.Int64Value(100),
		Permanent:       types.BoolValue(false),
		SSL:             types.BoolValue(true),
		Enabled:         types.BoolValue(true),
	}
	req := buildRedirectUpdateRequest(plan, "nice")

	if req.ResourceID == nil || *req.ResourceID != 9 {
		t.Errorf("ResourceID = %v", req.ResourceID)
	}
	if req.DomainID != nil || req.Subdomain != nil || req.MatchPath != nil ||
		req.RewritePath != nil || req.RewritePathType != nil {
		t.Errorf("cleared fields must be nil: %+v", req)
	}
	if req.NiceID != nil {
		t.Errorf("NiceID must be omitted when unknown")
	}
	if req.Priority == nil || *req.Priority != 100 || req.Permanent == nil || *req.Permanent ||
		req.SSL == nil || !*req.SSL || req.Enabled == nil || !*req.Enabled {
		t.Errorf("scalars wrong: %+v", req)
	}
	if req.Name == nil || *req.Name != "r" || req.DestinationHost == nil || req.PathMatchType == nil {
		t.Errorf("required fields wrong: %+v", req)
	}
}

func TestBuildRedirectUpdateRequest_SetValuesAndNiceID(t *testing.T) {
	plan := &RedirectModel{
		NiceID:          types.StringValue("new-nice"),
		Name:            types.StringValue("r"),
		DestinationHost: types.StringValue("https://example.com"),
		ResourceID:      types.Int64Null(),
		DomainID:        types.StringValue("dom-1"),
		Subdomain:       types.StringValue("old"),
		PathMatchType:   types.StringValue("prefix"),
		MatchPath:       types.StringValue("/a"),
		RewritePath:     types.StringValue("/b"),
		RewritePathType: types.StringValue("prefix"),
		Priority:        types.Int64Value(5),
		Permanent:       types.BoolValue(true),
		SSL:             types.BoolValue(true),
		Enabled:         types.BoolValue(false),
	}
	req := buildRedirectUpdateRequest(plan, "old-nice")

	if req.ResourceID != nil {
		t.Errorf("ResourceID must be nil")
	}
	if req.NiceID == nil || *req.NiceID != "new-nice" {
		t.Errorf("NiceID = %v", req.NiceID)
	}
	if req.DomainID == nil || *req.DomainID != "dom-1" || req.Subdomain == nil || *req.Subdomain != "old" ||
		req.MatchPath == nil || *req.MatchPath != "/a" || req.RewritePath == nil || *req.RewritePath != "/b" ||
		req.RewritePathType == nil || *req.RewritePathType != "prefix" {
		t.Errorf("values wrong: %+v", req)
	}
	if *req.Priority != 5 || !*req.Permanent || *req.Enabled {
		t.Errorf("scalars wrong: %+v", req)
	}

	// Unchanged nice_id is not sent.
	if got := buildRedirectUpdateRequest(plan, "new-nice"); got.NiceID != nil {
		t.Errorf("unchanged NiceID must be omitted")
	}
}

// validateRedirectConfig runs ValidateConfig with the given string/int
// attributes set (others null) and returns the error count.
func validateRedirectConfig(t *testing.T, vals map[string]tftypes.Value) int {
	t.Helper()
	ctx := context.Background()
	r := &RedirectResource{}
	var sresp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sresp)
	objType := sresp.Schema.Type().TerraformType(ctx).(tftypes.Object)

	all := map[string]tftypes.Value{}
	for name, typ := range objType.AttributeTypes {
		all[name] = tftypes.NewValue(typ, nil)
	}
	for k, v := range vals {
		all[k] = v
	}
	req := resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: sresp.Schema, Raw: tftypes.NewValue(objType, all)}}
	var resp resource.ValidateConfigResponse
	r.ValidateConfig(ctx, req, &resp)
	return resp.Diagnostics.ErrorsCount()
}

func TestRedirectValidateConfig_RewritePathRule(t *testing.T) {
	str := func(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }
	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)

	for _, tc := range []struct {
		name string
		vals map[string]tftypes.Value
		want int
	}{
		{"no rewrite at all", map[string]tftypes.Value{}, 0},
		{"path without type", map[string]tftypes.Value{"rewrite_path": str("/x")}, 0},
		{"prefix without path", map[string]tftypes.Value{"rewrite_path_type": str("prefix")}, 1},
		{"prefix with path", map[string]tftypes.Value{"rewrite_path_type": str("prefix"), "rewrite_path": str("/x")}, 0},
		{"stripPrefix without path", map[string]tftypes.Value{"rewrite_path_type": str("stripPrefix")}, 0},
		{"unknown type", map[string]tftypes.Value{"rewrite_path_type": unknown}, 0},
		{"unknown path", map[string]tftypes.Value{"rewrite_path_type": str("exact"), "rewrite_path": unknown}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateRedirectConfig(t, tc.vals); got != tc.want {
				t.Errorf("errors = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRedirectValidateConfig_SSLFalseWithResource(t *testing.T) {
	vals := map[string]tftypes.Value{
		"resource_id": tftypes.NewValue(tftypes.Number, 4),
		"ssl":         tftypes.NewValue(tftypes.Bool, false),
	}
	if got := validateRedirectConfig(t, vals); got != 1 {
		t.Errorf("errors = %d, want 1", got)
	}
}

func TestRedirectImportState_NonNumericID(t *testing.T) {
	ctx := context.Background()
	r := &RedirectResource{}
	var sresp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sresp)
	objType := sresp.Schema.Type().TerraformType(ctx)

	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: sresp.Schema, Raw: tftypes.NewValue(objType, nil)}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "abc"}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error for non-numeric id")
	}

	resp = resource.ImportStateResponse{State: tfsdk.State{Schema: sresp.Schema, Raw: tftypes.NewValue(objType, nil)}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "42"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", resp.Diagnostics)
	}
	var id types.String
	resp.State.GetAttribute(ctx, path.Root("id"), &id)
	if id.ValueString() != "42" {
		t.Errorf("id = %q", id.ValueString())
	}
}

func TestReconcileRedirectPlanned(t *testing.T) {
	planned := RedirectModel{
		Name:            types.StringValue("site"),
		DestinationHost: types.StringValue("https://Example.com"),
		Subdomain:       types.StringValue("App"),
		DomainID:        types.StringValue("d1"),
		MatchPath:       types.StringNull(),
		RewritePath:     types.StringNull(),
		RewritePathType: types.StringNull(),
	}
	got := RedirectModel{
		ID:              types.StringValue("1"),
		Name:            types.StringValue(" site "),
		DestinationHost: types.StringValue("https://example.com"),
		Subdomain:       types.StringValue("app"),
		DomainID:        types.StringValue("d1"),
		MatchPath:       types.StringValue(""),
		RewritePath:     types.StringValue(""),
		RewritePathType: types.StringValue(""),
	}
	keepEquivalentValues(&planned, &got)
	if got.DestinationHost.ValueString() != "https://Example.com" || got.Subdomain.ValueString() != "App" ||
		got.Name.ValueString() != "site" {
		t.Errorf("normalized values not kept: %+v", got)
	}
	if !got.MatchPath.IsNull() || !got.RewritePath.IsNull() || !got.RewritePathType.IsNull() {
		t.Errorf("empty strings should reconcile to null: %+v", got)
	}
	if got.ID.ValueString() != "1" {
		t.Errorf("server-owned id lost: %+v", got)
	}
}

func TestReconcileRedirectPlanned_RealDifferenceKept(t *testing.T) {
	planned := RedirectModel{
		Name:            types.StringValue("a"),
		DestinationHost: types.StringValue("https://a.example"),
		Subdomain:       types.StringValue("x"),
		MatchPath:       types.StringValue("/a"),
	}
	got := RedirectModel{
		Name:            types.StringValue("b"),
		DestinationHost: types.StringValue("https://b.example"),
		Subdomain:       types.StringValue("y"),
		MatchPath:       types.StringValue(""),
	}
	keepEquivalentValues(&planned, &got)
	if got.Name.ValueString() != "b" || got.DestinationHost.ValueString() != "https://b.example" ||
		got.Subdomain.ValueString() != "y" || got.MatchPath.ValueString() != "" {
		t.Errorf("real differences must be kept: %+v", got)
	}
}

func TestRedirectValidateConfig_SubdomainWithResource(t *testing.T) {
	str := func(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }
	num := tftypes.NewValue(tftypes.Number, 4)
	for _, tc := range []struct {
		name string
		vals map[string]tftypes.Value
		want int
	}{
		{"resource with subdomain", map[string]tftypes.Value{"resource_id": num, "subdomain": str("a")}, 1},
		{"resource unknown subdomain", map[string]tftypes.Value{"resource_id": num, "subdomain": tftypes.NewValue(tftypes.String, tftypes.UnknownValue)}, 0},
		{"unknown resource with subdomain", map[string]tftypes.Value{"resource_id": tftypes.NewValue(tftypes.Number, tftypes.UnknownValue), "subdomain": str("app")}, 0},
		{"both unknown", map[string]tftypes.Value{"resource_id": tftypes.NewValue(tftypes.Number, tftypes.UnknownValue), "subdomain": tftypes.NewValue(tftypes.String, tftypes.UnknownValue)}, 0},
		{"unknown resource ssl false", map[string]tftypes.Value{"resource_id": tftypes.NewValue(tftypes.Number, tftypes.UnknownValue), "ssl": tftypes.NewValue(tftypes.Bool, false)}, 0},
		{"domain with subdomain", map[string]tftypes.Value{"domain_id": str("d"), "subdomain": str("a")}, 0},
		{"resource alone", map[string]tftypes.Value{"resource_id": num}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateRedirectConfig(t, tc.vals); got != tc.want {
				t.Errorf("errors = %d, want %d", got, tc.want)
			}
		})
	}
}

func planModifyJoined(t *testing.T, stateNull bool, stateRes, planRes tftypes.Value, stateDom, planDom tftypes.Value) types.String {
	t.Helper()
	return planModifyJoinedWith(t, stateNull, stateRes, planRes, stateDom, planDom, types.StringValue("old.example.com"))
}

func planModifyJoinedWith(t *testing.T, stateNull bool, stateRes, planRes tftypes.Value, stateDom, planDom tftypes.Value, stateValue types.String) types.String {
	t.Helper()
	ctx := context.Background()
	r := &RedirectResource{}
	var sresp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sresp)
	objType := sresp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	build := func(res, dom tftypes.Value) tftypes.Value {
		all := map[string]tftypes.Value{}
		for name, typ := range objType.AttributeTypes {
			all[name] = tftypes.NewValue(typ, nil)
		}
		all["resource_id"] = res
		all["domain_id"] = dom
		return tftypes.NewValue(objType, all)
	}
	state := tfsdk.State{Schema: sresp.Schema, Raw: tftypes.NewValue(objType, nil)}
	if !stateNull {
		state.Raw = build(stateRes, stateDom)
	}
	plan := tfsdk.Plan{Schema: sresp.Schema, Raw: build(planRes, planDom)}
	req := planmodifier.StringRequest{
		Plan: plan, State: state,
		PlanValue: types.StringUnknown(), StateValue: stateValue,
	}
	resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
	keepWhenAttachmentUnchanged{}.PlanModifyString(ctx, req, resp)
	return resp.PlanValue
}

func TestKeepWhenAttachmentUnchanged(t *testing.T) {
	num := func(n int) tftypes.Value { return tftypes.NewValue(tftypes.Number, n) }
	nilNum := tftypes.NewValue(tftypes.Number, nil)
	str := func(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }
	nilStr := tftypes.NewValue(tftypes.String, nil)

	if got := planModifyJoined(t, false, num(1), num(1), nilStr, nilStr); got.ValueString() != "old.example.com" {
		t.Errorf("unchanged resource: got %v", got)
	}
	if got := planModifyJoined(t, false, nilNum, nilNum, str("d"), str("d")); got.ValueString() != "old.example.com" {
		t.Errorf("unchanged domain: got %v", got)
	}
	if got := planModifyJoined(t, false, num(1), num(2), nilStr, nilStr); !got.IsUnknown() {
		t.Errorf("changed resource must stay unknown: got %v", got)
	}
	if got := planModifyJoined(t, false, nilNum, nilNum, str("d"), str("e")); !got.IsUnknown() {
		t.Errorf("changed domain must stay unknown: got %v", got)
	}
	if got := planModifyJoined(t, true, nilNum, num(1), nilStr, nilStr); !got.IsUnknown() {
		t.Errorf("create must stay unknown: got %v", got)
	}
}

func TestHydrateRedirectState_NullPriorityKeepsKnown(t *testing.T) {
	r := &client.Redirect{RedirectID: 1}
	m := RedirectModel{Priority: types.Int64Value(42)}
	hydrateRedirectState(&m, r)
	if m.Priority.ValueInt64() != 42 {
		t.Errorf("Priority = %v, want 42 kept", m.Priority)
	}
	for _, p := range []types.Int64{types.Int64Null(), types.Int64Unknown()} {
		m = RedirectModel{Priority: p}
		hydrateRedirectState(&m, r)
		if m.Priority.ValueInt64() != 100 {
			t.Errorf("Priority = %v, want 100", m.Priority)
		}
	}
}

func TestKeepEquivalentValues_ReadAgainstPriorState(t *testing.T) {
	prior := RedirectModel{
		Name:            types.StringValue("site"),
		DestinationHost: types.StringValue("https://Example.com"),
		Subdomain:       types.StringNull(),
		MatchPath:       types.StringValue("/a"),
	}
	got := RedirectModel{
		Name:            types.StringValue(" site "),
		DestinationHost: types.StringValue("https://example.com"),
		Subdomain:       types.StringValue(""),
		MatchPath:       types.StringValue("/b"),
	}
	keepEquivalentValues(&prior, &got)
	if got.Name.ValueString() != "site" || got.DestinationHost.ValueString() != "https://Example.com" || !got.Subdomain.IsNull() {
		t.Errorf("equivalent values not kept: %+v", got)
	}
	if got.MatchPath.ValueString() != "/b" {
		t.Errorf("drift must be kept: %+v", got)
	}
}

func TestKeepEquivalentValues_ImportKeepsServerValues(t *testing.T) {
	prior := RedirectModel{ID: types.StringValue("1")}
	got := RedirectModel{
		Name:            types.StringValue("site"),
		DestinationHost: types.StringValue("https://example.com"),
		Subdomain:       types.StringValue("app"),
		MatchPath:       types.StringValue("/a"),
	}
	keepEquivalentValues(&prior, &got)
	if got.Name.ValueString() != "site" || got.DestinationHost.ValueString() != "https://example.com" ||
		got.Subdomain.ValueString() != "app" || got.MatchPath.ValueString() != "/a" {
		t.Errorf("import must keep server values: %+v", got)
	}
}

func TestBuildRedirectUpdateRequest_FromPlannedNotServer(t *testing.T) {
	planned := RedirectModel{
		Name:            types.StringValue("mine"),
		DestinationHost: types.StringValue("https://mine.example"),
		PathMatchType:   types.StringValue("prefix"),
		NiceID:          types.StringValue("wanted"),
		Priority:        types.Int64Unknown(),
	}
	req := buildRedirectUpdateRequest(&planned, "server-nice")
	if req.NiceID == nil || *req.NiceID != "wanted" || *req.Name != "mine" || req.Priority != nil {
		t.Errorf("unexpected request: %+v", req)
	}
}

func TestKeepWhenAttachmentUnchanged_NullStateValueStaysUnknown(t *testing.T) {
	num := tftypes.NewValue(tftypes.Number, 1)
	nilStr := tftypes.NewValue(tftypes.String, nil)
	got := planModifyJoinedWith(t, false, num, num, nilStr, nilStr, types.StringNull())
	if !got.IsUnknown() {
		t.Errorf("null state value must leave plan unknown: got %v", got)
	}
}
