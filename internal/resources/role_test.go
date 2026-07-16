package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

// -----------------------------------------------------------------------------
// roleToModel: wire -> state, with prior-preservation semantics
// -----------------------------------------------------------------------------

func TestRoleToModel_Nominal(t *testing.T) {
	role := &client.Role{
		RoleID:                42,
		Name:                  "eng",
		Description:           "engineers",
		IsAdmin:               boolPtr(false),
		OrgID:                 "org1",
		OrgName:               "Acme",
		RequireDeviceApproval: true,
		AllowSSH:              boolPtr(true),
		SSHSudoMode:           "restricted",
		SSHSudoCommandsRaw:    `["sudo","wheel"]`,
		SSHCreateHomeDir:      true,
		SSHUnixGroupsRaw:      `["docker","admin"]`,
	}
	m, diags := roleToModel(context.Background(), role, RoleResourceModel{})
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if m.ID.ValueInt64() != 42 {
		t.Errorf("ID = %d", m.ID.ValueInt64())
	}
	if m.Name.ValueString() != "eng" {
		t.Errorf("Name = %q", m.Name.ValueString())
	}
	if m.Description.ValueString() != "engineers" {
		t.Errorf("Description = %q", m.Description.ValueString())
	}
	if m.OrgName.ValueString() != "Acme" {
		t.Errorf("OrgName = %q", m.OrgName.ValueString())
	}
	if m.IsAdmin.ValueBool() {
		t.Errorf("IsAdmin should be false")
	}
	if !m.RequireDeviceApproval.ValueBool() {
		t.Errorf("RequireDeviceApproval")
	}
	if !m.AllowSSH.ValueBool() {
		t.Errorf("AllowSSH should reflect server value true")
	}
	if m.SSHSudoMode.ValueString() != "restricted" {
		t.Errorf("SSHSudoMode = %q", m.SSHSudoMode.ValueString())
	}
	if !m.SSHCreateHomeDir.ValueBool() {
		t.Errorf("SSHCreateHomeDir")
	}
	var sudo []string
	diags = m.SSHSudoCommands.ElementsAs(context.Background(), &sudo, false)
	if diags.HasError() {
		t.Fatalf("sudo diags: %v", diags)
	}
	if len(sudo) != 2 || sudo[0] != "sudo" || sudo[1] != "wheel" {
		t.Errorf("SSHSudoCommands = %v", sudo)
	}
	var groups []string
	diags = m.SSHUnixGroups.ElementsAs(context.Background(), &groups, false)
	if diags.HasError() {
		t.Fatalf("groups diags: %v", diags)
	}
	if len(groups) != 2 || groups[0] != "docker" || groups[1] != "admin" {
		t.Errorf("SSHUnixGroups = %v", groups)
	}
}

func TestRoleToModel_AllowSSH_PreservedFromPrior_When119ServerOmits(t *testing.T) {
	// 1.19 Read stopped emitting allowSsh. roleToModel must
	// preserve the value the plan supplied so the diff is a no-op.
	role := &client.Role{
		RoleID:      1,
		Name:        "eng",
		Description: "engineers",
		AllowSSH:    nil, // 1.19 server omits
	}
	prior := RoleResourceModel{AllowSSH: types.BoolValue(true)}
	m, diags := roleToModel(context.Background(), role, prior)
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}
	if !m.AllowSSH.ValueBool() {
		t.Errorf("expected AllowSSH preserved from prior=true, got %v", m.AllowSSH)
	}
}

func TestRoleToModel_AllowSSH_ServerWins(t *testing.T) {
	// If the server DID emit allowSsh, its value wins over the prior.
	role := &client.Role{
		RoleID:   1,
		Name:     "eng",
		AllowSSH: boolPtr(false),
	}
	prior := RoleResourceModel{AllowSSH: types.BoolValue(true)}
	m, diags := roleToModel(context.Background(), role, prior)
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}
	if m.AllowSSH.ValueBool() {
		t.Errorf("expected server value (false) to win over prior (true)")
	}
}

func TestRoleToModel_AllowSSH_DefaultsFalse_WhenBothNilAndPriorNull(t *testing.T) {
	// The Import path: prior is empty and server omits allowSsh. The
	// mapper falls back to false so the state is well-formed.
	role := &client.Role{RoleID: 1, Name: "eng", AllowSSH: nil}
	m, diags := roleToModel(context.Background(), role, RoleResourceModel{})
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}
	if m.AllowSSH.IsNull() || m.AllowSSH.IsUnknown() {
		t.Errorf("AllowSSH must not be null/unknown, got %v", m.AllowSSH)
	}
	if m.AllowSSH.ValueBool() {
		t.Errorf("expected AllowSSH to default to false")
	}
}

func TestRoleToModel_IsAdmin_NilTreatedAsFalse(t *testing.T) {
	role := &client.Role{RoleID: 1, Name: "eng", IsAdmin: nil}
	m, diags := roleToModel(context.Background(), role, RoleResourceModel{})
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}
	if m.IsAdmin.IsNull() || m.IsAdmin.IsUnknown() {
		t.Errorf("IsAdmin expected non-null false, got %v", m.IsAdmin)
	}
	if m.IsAdmin.ValueBool() {
		t.Errorf("IsAdmin nil should mean false")
	}
}

func TestRoleToModel_EmptyListsRaw(t *testing.T) {
	// Both `""` and `"[]"` on the wire decode to an empty list.
	for _, raw := range []string{"", "[]"} {
		role := &client.Role{
			RoleID:             1,
			Name:               "eng",
			SSHSudoCommandsRaw: raw,
			SSHUnixGroupsRaw:   raw,
		}
		m, diags := roleToModel(context.Background(), role, RoleResourceModel{})
		if diags.HasError() {
			t.Fatalf("diags for raw=%q: %v", raw, diags)
		}
		var sudo []string
		_ = m.SSHSudoCommands.ElementsAs(context.Background(), &sudo, false)
		if len(sudo) != 0 {
			t.Errorf("raw=%q: SSHSudoCommands = %v, want empty", raw, sudo)
		}
		var groups []string
		_ = m.SSHUnixGroups.ElementsAs(context.Background(), &groups, false)
		if len(groups) != 0 {
			t.Errorf("raw=%q: SSHUnixGroups = %v", raw, groups)
		}
	}
}

func TestRoleToModel_MalformedRawSurfacesDiag(t *testing.T) {
	role := &client.Role{
		RoleID:             1,
		Name:               "eng",
		SSHSudoCommandsRaw: "not-json",
	}
	_, diags := roleToModel(context.Background(), role, RoleResourceModel{})
	if !diags.HasError() {
		t.Fatalf("expected error diags for malformed raw list")
	}
}

// -----------------------------------------------------------------------------
// applyRoleSSHFields: state -> wire
// -----------------------------------------------------------------------------

func TestApplyRoleSSHFields_UnknownsLeavePointersNil(t *testing.T) {
	plan := RoleResourceModel{
		RequireDeviceApproval: types.BoolUnknown(),
		AllowSSH:              types.BoolUnknown(),
		SSHSudoMode:           types.StringUnknown(),
		SSHCreateHomeDir:      types.BoolUnknown(),
		SSHSudoCommands:       types.ListNull(types.StringType),
		SSHUnixGroups:         types.ListUnknown(types.StringType),
	}
	req := &client.CreateRoleRequest{}
	var diags diag.Diagnostics
	applyRoleSSHFields(context.Background(), plan,
		&req.RequireDeviceApproval, &req.AllowSSH, &req.SSHSudoMode,
		&req.SSHSudoCommands, &req.SSHCreateHomeDir, &req.SSHUnixGroups, &diags)
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}
	if req.RequireDeviceApproval != nil || req.AllowSSH != nil ||
		req.SSHSudoMode != nil || req.SSHCreateHomeDir != nil {
		t.Errorf("unknowns must not populate pointers, got %+v", req)
	}
	if req.SSHSudoCommands != nil {
		t.Errorf("null list must not populate SSHSudoCommands")
	}
	if req.SSHUnixGroups != nil {
		t.Errorf("unknown list must not populate SSHUnixGroups")
	}
}

func TestApplyRoleSSHFields_FullPayload(t *testing.T) {
	ctx := context.Background()
	sudoList, _ := types.ListValueFrom(ctx, types.StringType, []string{"a", "b"})
	groupsList, _ := types.ListValueFrom(ctx, types.StringType, []string{"docker"})
	plan := RoleResourceModel{
		RequireDeviceApproval: types.BoolValue(true),
		AllowSSH:              types.BoolValue(false),
		SSHSudoMode:           types.StringValue("full"),
		SSHCreateHomeDir:      types.BoolValue(true),
		SSHSudoCommands:       sudoList,
		SSHUnixGroups:         groupsList,
	}
	req := &client.CreateRoleRequest{}
	var diags diag.Diagnostics
	applyRoleSSHFields(ctx, plan,
		&req.RequireDeviceApproval, &req.AllowSSH, &req.SSHSudoMode,
		&req.SSHSudoCommands, &req.SSHCreateHomeDir, &req.SSHUnixGroups, &diags)
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}
	if req.RequireDeviceApproval == nil || *req.RequireDeviceApproval != true {
		t.Errorf("RequireDeviceApproval")
	}
	if req.AllowSSH == nil || *req.AllowSSH != false {
		t.Errorf("AllowSSH=false must be sent, not omitted")
	}
	if req.SSHSudoMode == nil || *req.SSHSudoMode != "full" {
		t.Errorf("SSHSudoMode")
	}
	if req.SSHCreateHomeDir == nil || *req.SSHCreateHomeDir != true {
		t.Errorf("SSHCreateHomeDir")
	}
	if len(req.SSHSudoCommands) != 2 || req.SSHSudoCommands[0] != "a" {
		t.Errorf("SSHSudoCommands = %v", req.SSHSudoCommands)
	}
	if len(req.SSHUnixGroups) != 1 || req.SSHUnixGroups[0] != "docker" {
		t.Errorf("SSHUnixGroups = %v", req.SSHUnixGroups)
	}
}

// -----------------------------------------------------------------------------
// Round-trip: model -> create request -> emulate server -> roleToModel
// -----------------------------------------------------------------------------

func TestRoleResource_RoundTripPreservesFields(t *testing.T) {
	ctx := context.Background()
	sudoList, _ := types.ListValueFrom(ctx, types.StringType, []string{"a", "b"})
	groupsList, _ := types.ListValueFrom(ctx, types.StringType, []string{"docker"})
	plan := RoleResourceModel{
		Name:                  types.StringValue("eng"),
		Description:           types.StringValue("engineers"),
		RequireDeviceApproval: types.BoolValue(true),
		AllowSSH:              types.BoolValue(true),
		SSHSudoMode:           types.StringValue("restricted"),
		SSHCreateHomeDir:      types.BoolValue(true),
		SSHSudoCommands:       sudoList,
		SSHUnixGroups:         groupsList,
	}
	req := &client.CreateRoleRequest{
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
	}
	var diags diag.Diagnostics
	applyRoleSSHFields(ctx, plan,
		&req.RequireDeviceApproval, &req.AllowSSH, &req.SSHSudoMode,
		&req.SSHSudoCommands, &req.SSHCreateHomeDir, &req.SSHUnixGroups, &diags)
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}

	// Emulate server: build a Role back from the request. The server
	// serializes the list fields as JSON strings, not native arrays -
	// that's the sshSudoCommands/sshUnixGroups quirk.
	role := &client.Role{
		RoleID:                7,
		Name:                  req.Name,
		Description:           req.Description,
		RequireDeviceApproval: derefBool(req.RequireDeviceApproval),
		AllowSSH:              req.AllowSSH,
		SSHSudoMode:           derefString(req.SSHSudoMode),
		SSHSudoCommandsRaw:    `["a","b"]`,
		SSHCreateHomeDir:      derefBool(req.SSHCreateHomeDir),
		SSHUnixGroupsRaw:      `["docker"]`,
	}
	back, diags := roleToModel(ctx, role, plan)
	if diags.HasError() {
		t.Fatalf("back-map diags: %v", diags)
	}
	if back.Name.ValueString() != "eng" || back.Description.ValueString() != "engineers" {
		t.Errorf("name/desc lost")
	}
	if !back.AllowSSH.ValueBool() {
		t.Errorf("AllowSSH lost")
	}
	if back.SSHSudoMode.ValueString() != "restricted" {
		t.Errorf("SSHSudoMode lost")
	}
	var sudo []string
	_ = back.SSHSudoCommands.ElementsAs(ctx, &sudo, false)
	if len(sudo) != 2 || sudo[0] != "a" {
		t.Errorf("SSHSudoCommands lost: %v", sudo)
	}
}

func derefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
