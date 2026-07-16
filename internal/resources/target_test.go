package resources

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

// -----------------------------------------------------------------------------
// targetToModel: wire -> state
// -----------------------------------------------------------------------------

func TestTargetToModel_FullPayload(t *testing.T) {
	// hcHeaders comes back as a JSON-encoded string (per the wire
	// asymmetry documented on Target.UnmarshalJSON). Simulate the
	// post-UnmarshalJSON state where HCHeadersRaw is a string.
	rawHeaders := `[{"name":"X-Probe","value":"yes"},{"name":"X-Env","value":"prod"}]`
	tgt := &client.Target{
		TargetID:             8,
		ResourceID:           2,
		SiteID:               5,
		IP:                   "10.0.0.1",
		Port:                 8080,
		Method:               "https",
		Enabled:              true,
		HCEnabled:            true,
		HCPath:               strPtr("/health"),
		HCScheme:             strPtr("https"),
		HCMode:               strPtr("http"),
		HCHostname:           strPtr("probe.internal"),
		HCPort:               intPtr(8081),
		HCInterval:           intPtr(10),
		HCUnhealthyInterval:  intPtr(3),
		HCTimeout:            intPtr(2),
		HCHeadersRaw:         &rawHeaders,
		HCFollowRedirects:    boolPtr(true),
		HCMethod:             strPtr("GET"),
		HCStatus:             intPtr(200),
		HCTLSServerName:      strPtr("probe.svc"),
		HCHealthyThreshold:   intPtr(2),
		HCUnhealthyThreshold: intPtr(3),
		HCHealth:             "healthy",
		Path:                 strPtr("/api"),
		PathMatchType:        strPtr("prefix"),
		RewritePath:          strPtr("/v1/api"),
		RewritePathType:      strPtr("prefix"),
		Priority:             intPtr(10),
	}

	m := targetToModel(tgt, TargetResourceModel{})

	if m.ID.ValueInt64() != 8 || m.ResourceID.ValueInt64() != 2 || m.SiteID.ValueInt64() != 5 {
		t.Errorf("ids wrong: id=%d res=%d site=%d",
			m.ID.ValueInt64(), m.ResourceID.ValueInt64(), m.SiteID.ValueInt64())
	}
	if m.IP.ValueString() != "10.0.0.1" || m.Port.ValueInt64() != 8080 {
		t.Errorf("ip/port wrong")
	}
	if m.Method.ValueString() != "https" || !m.Enabled.ValueBool() {
		t.Errorf("method/enabled wrong")
	}
	if !m.HCEnabled.ValueBool() {
		t.Errorf("HCEnabled")
	}
	if m.HCPath.ValueString() != "/health" {
		t.Errorf("HCPath")
	}
	if m.HCScheme.ValueString() != "https" || m.HCMode.ValueString() != "http" {
		t.Errorf("HCScheme/HCMode")
	}
	if m.HCHostname.ValueString() != "probe.internal" {
		t.Errorf("HCHostname")
	}
	if m.HCPort.ValueInt64() != 8081 || m.HCInterval.ValueInt64() != 10 ||
		m.HCUnhealthyInterval.ValueInt64() != 3 || m.HCTimeout.ValueInt64() != 2 {
		t.Errorf("HC int fields")
	}
	if len(m.HCHeaders) != 2 {
		t.Fatalf("HCHeaders len = %d, want 2", len(m.HCHeaders))
	}
	if m.HCHeaders[0].Name.ValueString() != "X-Probe" || m.HCHeaders[0].Value.ValueString() != "yes" {
		t.Errorf("HCHeaders[0] wrong: %+v", m.HCHeaders[0])
	}
	if m.HCHeaders[1].Name.ValueString() != "X-Env" {
		t.Errorf("HCHeaders[1] wrong: %+v", m.HCHeaders[1])
	}
	if !m.HCFollowRedirects.ValueBool() {
		t.Errorf("HCFollowRedirects")
	}
	if m.HCMethod.ValueString() != "GET" || m.HCStatus.ValueInt64() != 200 {
		t.Errorf("HC method/status")
	}
	if m.HCTLSServerName.ValueString() != "probe.svc" {
		t.Errorf("HCTLSServerName")
	}
	if m.HCHealthyThreshold.ValueInt64() != 2 || m.HCUnhealthyThreshold.ValueInt64() != 3 {
		t.Errorf("HC thresholds")
	}
	if m.HCHealth.ValueString() != "healthy" {
		t.Errorf("HCHealth")
	}
	if m.Path.ValueString() != "/api" || m.PathMatchType.ValueString() != "prefix" {
		t.Errorf("path routing")
	}
	if m.RewritePath.ValueString() != "/v1/api" || m.RewritePathType.ValueString() != "prefix" {
		t.Errorf("rewrite")
	}
	if m.Priority.ValueInt64() != 10 {
		t.Errorf("Priority")
	}
}

func TestTargetToModel_MinimalPayload(t *testing.T) {
	// A bare-minimum GET response: only the required fields, every
	// HC / routing pointer nil. The mapper must not crash and must
	// surface null for every optional.
	tgt := &client.Target{
		TargetID:   1,
		ResourceID: 2,
		SiteID:     3,
		IP:         "x",
		Port:       80,
		Method:     "http",
		Enabled:    false,
	}
	m := targetToModel(tgt, TargetResourceModel{})
	if m.HCEnabled.ValueBool() {
		t.Errorf("HCEnabled default should be false")
	}
	for name, v := range map[string]bool{
		"HCPath":               m.HCPath.IsNull(),
		"HCScheme":             m.HCScheme.IsNull(),
		"HCMode":               m.HCMode.IsNull(),
		"HCHostname":           m.HCHostname.IsNull(),
		"HCPort":               m.HCPort.IsNull(),
		"HCInterval":           m.HCInterval.IsNull(),
		"HCUnhealthyInterval":  m.HCUnhealthyInterval.IsNull(),
		"HCTimeout":            m.HCTimeout.IsNull(),
		"HCFollowRedirects":    m.HCFollowRedirects.IsNull(),
		"HCMethod":             m.HCMethod.IsNull(),
		"HCStatus":             m.HCStatus.IsNull(),
		"HCTLSServerName":      m.HCTLSServerName.IsNull(),
		"HCHealthyThreshold":   m.HCHealthyThreshold.IsNull(),
		"HCUnhealthyThreshold": m.HCUnhealthyThreshold.IsNull(),
		"Path":                 m.Path.IsNull(),
		"PathMatchType":        m.PathMatchType.IsNull(),
		"RewritePath":          m.RewritePath.IsNull(),
		"RewritePathType":      m.RewritePathType.IsNull(),
		"Priority":             m.Priority.IsNull(),
	} {
		if !v {
			t.Errorf("%s expected null", name)
		}
	}
	// Empty HCHeaders slice, not nil (targetToModel always allocates).
	if m.HCHeaders == nil {
		t.Errorf("HCHeaders expected non-nil empty slice")
	}
	if len(m.HCHeaders) != 0 {
		t.Errorf("HCHeaders len = %d, want 0", len(m.HCHeaders))
	}
}

func TestTargetToModel_MalformedHCHeadersFallsBackToEmpty(t *testing.T) {
	// Intentionally corrupt string. targetToModel is documented to
	// silently fall back to empty rather than fail Read.
	raw := "not-json-at-all"
	tgt := &client.Target{
		TargetID: 1, ResourceID: 2, SiteID: 3,
		IP: "x", Port: 80, Method: "http",
		HCHeadersRaw: &raw,
	}
	m := targetToModel(tgt, TargetResourceModel{})
	if len(m.HCHeaders) != 0 {
		t.Errorf("expected empty HCHeaders on malformed input, got %d entries", len(m.HCHeaders))
	}
}

// -----------------------------------------------------------------------------
// applyTargetHCFields: state -> wire (pointer fields on request struct)
// -----------------------------------------------------------------------------

// invokeApplyTargetHCFields wires a plan through the vararg-style helper
// against a fresh CreateTargetRequest and returns it. Reduces the noise
// at every call site of the many-pointer signature.
func invokeApplyTargetHCFields(plan TargetResourceModel) *client.CreateTargetRequest {
	req := &client.CreateTargetRequest{}
	applyTargetHCFields(plan,
		&req.HCEnabled, &req.HCPath, &req.HCScheme, &req.HCMode, &req.HCHostname,
		&req.HCPort, &req.HCInterval, &req.HCUnhealthyInterval, &req.HCTimeout,
		&req.HCHeaders, &req.HCFollowRedirects, &req.HCMethod, &req.HCStatus,
		&req.HCTLSServerName, &req.HCHealthyThreshold, &req.HCUnhealthyThreshold,
		&req.Path, &req.PathMatchType, &req.RewritePath, &req.RewritePathType,
		&req.Priority)
	return req
}

func TestApplyTargetHCFields_AllUnknownOrNull_LeavesRequestEmpty(t *testing.T) {
	// A plan where every hc_* + routing field is null or unknown must
	// leave the CreateTargetRequest optional pointers all nil.
	plan := TargetResourceModel{
		HCEnabled:            types.BoolUnknown(),
		HCPath:               types.StringNull(),
		HCScheme:             types.StringUnknown(),
		HCMode:               types.StringNull(),
		HCHostname:           types.StringUnknown(),
		HCPort:               types.Int64Null(),
		HCInterval:           types.Int64Unknown(),
		HCUnhealthyInterval:  types.Int64Null(),
		HCTimeout:            types.Int64Unknown(),
		HCFollowRedirects:    types.BoolNull(),
		HCMethod:             types.StringUnknown(),
		HCStatus:             types.Int64Null(),
		HCTLSServerName:      types.StringUnknown(),
		HCHealthyThreshold:   types.Int64Null(),
		HCUnhealthyThreshold: types.Int64Unknown(),
		Path:                 types.StringNull(),
		PathMatchType:        types.StringUnknown(),
		RewritePath:          types.StringNull(),
		RewritePathType:      types.StringUnknown(),
		Priority:             types.Int64Null(),
	}
	req := invokeApplyTargetHCFields(plan)

	// Every pointer field on the request struct must remain nil, and
	// HCHeaders must not be populated when plan.HCHeaders is empty.
	if req.HCEnabled != nil || req.HCPath != nil || req.HCScheme != nil ||
		req.HCMode != nil || req.HCHostname != nil ||
		req.HCPort != nil || req.HCInterval != nil ||
		req.HCUnhealthyInterval != nil || req.HCTimeout != nil ||
		req.HCFollowRedirects != nil || req.HCMethod != nil ||
		req.HCStatus != nil || req.HCTLSServerName != nil ||
		req.HCHealthyThreshold != nil || req.HCUnhealthyThreshold != nil ||
		req.Path != nil || req.PathMatchType != nil ||
		req.RewritePath != nil || req.RewritePathType != nil ||
		req.Priority != nil {
		t.Errorf("expected all pointer fields nil, got %+v", req)
	}
	if req.HCHeaders != nil {
		t.Errorf("HCHeaders expected nil when plan slice empty")
	}

	// The JSON payload for the fixed fields (IP/Port/etc unset) must
	// omit every optional key. Even ip/port serialize as "" and 0
	// respectively (Required at the schema level - not our concern).
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Should not contain any of the hc keys or routing keys.
	forbidden := []string{"hcEnabled", "hcPath", "hcScheme", "hcMode",
		"hcHostname", "hcPort", "hcInterval", "hcUnhealthyInterval",
		"hcTimeout", "hcHeaders", "hcFollowRedirects", "hcMethod",
		"hcStatus", "hcTlsServerName", "hcHealthyThreshold",
		"hcUnhealthyThreshold", "path", "pathMatchType",
		"rewritePath", "rewritePathType", "priority"}
	s := string(raw)
	for _, k := range forbidden {
		if contains(s, `"`+k+`"`) {
			t.Errorf("payload leaked optional key %q: %s", k, s)
		}
	}
}

func TestApplyTargetHCFields_AllSet(t *testing.T) {
	plan := TargetResourceModel{
		HCEnabled:            types.BoolValue(true),
		HCPath:               types.StringValue("/health"),
		HCScheme:             types.StringValue("https"),
		HCMode:               types.StringValue("http"),
		HCHostname:           types.StringValue("probe"),
		HCPort:               types.Int64Value(8081),
		HCInterval:           types.Int64Value(10),
		HCUnhealthyInterval:  types.Int64Value(3),
		HCTimeout:            types.Int64Value(2),
		HCFollowRedirects:    types.BoolValue(true),
		HCMethod:             types.StringValue("GET"),
		HCStatus:             types.Int64Value(200),
		HCTLSServerName:      types.StringValue("probe.svc"),
		HCHealthyThreshold:   types.Int64Value(2),
		HCUnhealthyThreshold: types.Int64Value(3),
		Path:                 types.StringValue("/api"),
		PathMatchType:        types.StringValue("prefix"),
		RewritePath:          types.StringValue("/v1/api"),
		RewritePathType:      types.StringValue("prefix"),
		Priority:             types.Int64Value(10),
		HCHeaders: []TargetHCHeaderModel{
			{Name: types.StringValue("X-Probe"), Value: types.StringValue("yes")},
		},
	}
	req := invokeApplyTargetHCFields(plan)
	if req.HCEnabled == nil || *req.HCEnabled != true {
		t.Errorf("HCEnabled")
	}
	if req.HCPath == nil || *req.HCPath != "/health" {
		t.Errorf("HCPath")
	}
	if req.HCPort == nil || *req.HCPort != 8081 {
		t.Errorf("HCPort")
	}
	if req.HCInterval == nil || *req.HCInterval != 10 {
		t.Errorf("HCInterval")
	}
	if req.HCTLSServerName == nil || *req.HCTLSServerName != "probe.svc" {
		t.Errorf("HCTLSServerName")
	}
	if req.Priority == nil || *req.Priority != 10 {
		t.Errorf("Priority")
	}
	if len(req.HCHeaders) != 1 || req.HCHeaders[0].Name != "X-Probe" ||
		req.HCHeaders[0].Value != "yes" {
		t.Errorf("HCHeaders = %+v", req.HCHeaders)
	}
}

// -----------------------------------------------------------------------------
// Round-trip via Target.UnmarshalJSON string quirk
// -----------------------------------------------------------------------------

func TestTarget_UnmarshalHCHeadersStringForm(t *testing.T) {
	// CREATE / UPDATE emit hcHeaders as a JSON-encoded string.
	raw := `{
		"targetId": 1, "resourceId": 2, "siteId": 3,
		"ip": "x", "port": 80, "method": "http", "enabled": true,
		"hcHeaders": "[{\"name\":\"X-A\",\"value\":\"1\"}]"
	}`
	var tgt client.Target
	if err := json.Unmarshal([]byte(raw), &tgt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m := targetToModel(&tgt, TargetResourceModel{})
	if len(m.HCHeaders) != 1 || m.HCHeaders[0].Name.ValueString() != "X-A" {
		t.Errorf("HCHeaders lost across UnmarshalJSON string form: %+v", m.HCHeaders)
	}
}

func TestTarget_UnmarshalHCHeadersArrayForm(t *testing.T) {
	// GET emits hcHeaders as a native JSON array.
	raw := `{
		"targetId": 1, "resourceId": 2, "siteId": 3,
		"ip": "x", "port": 80, "method": "http", "enabled": true,
		"hcHeaders": [{"name":"X-A","value":"1"}]
	}`
	var tgt client.Target
	if err := json.Unmarshal([]byte(raw), &tgt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m := targetToModel(&tgt, TargetResourceModel{})
	if len(m.HCHeaders) != 1 || m.HCHeaders[0].Name.ValueString() != "X-A" {
		t.Errorf("HCHeaders lost across UnmarshalJSON array form: %+v", m.HCHeaders)
	}
}

// tiny substring helper local to this test file so we don't drag in
// strings just for one call.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
