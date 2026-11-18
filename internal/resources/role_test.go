package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
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

// Role list fixtures in the server's shape: sorted by name, isAdmin true on
// the Admin role and null on every other one (createOrg / createRole never
// set it, and the column has no default).
const (
	roleAdmin  = `{"roleId":1,"orgId":"org","name":"Admin","isAdmin":true}`
	roleDevs   = `{"roleId":5,"orgId":"org","name":"Devs","isAdmin":null}`
	roleMember = `{"roleId":2,"orgId":"org","name":"Member","isAdmin":null}`
)

// roleDeleteTestServer lists roles (JSON array bodies: before, then after
// the DELETE), answers DELETE /role/5 with deleteStatus and records the
// replacement role ID sent with it. GET /role/5 answers getRole5, or 404
// when empty (a re-read then goes through the list).
func roleDeleteTestServer(t *testing.T, rolesJSON, rolesAfterDelete string, deleteStatus int, getRole5 string, deleted *string) *RoleResource {
	t.Helper()
	notFound := func(w http.ResponseWriter, msg string) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, `{"data":null,"success":false,"error":true,"message":%q,"status":404}`, msg)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/org/org/roles":
			list := rolesJSON
			if *deleted != "" {
				list = rolesAfterDelete
			}
			var items []json.RawMessage
			if err := json.Unmarshal([]byte(list), &items); err != nil {
				t.Errorf("roles fixture: %v", err)
				return
			}
			_, _ = fmt.Fprintf(w, `{"data":{"roles":%s,"pagination":{"total":%d,"page":1,"pageSize":1000}},"success":true,"error":false,"message":"Roles retrieved successfully","status":200}`, list, len(items))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/role/5":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			*deleted = body["roleId"]
			if deleteStatus != http.StatusOK {
				notFound(w, "Role with ID "+body["roleId"]+" not found")
				return
			}
			_, _ = w.Write([]byte(`{"data":null,"success":true,"error":false,"message":"Role deleted successfully","status":200}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/role/5":
			if getRole5 == "" {
				notFound(w, "Role with ID 5 not found")
				return
			}
			_, _ = fmt.Fprintf(w, `{"data":%s,"success":true,"error":false,"message":"Role retrieved successfully","status":200}`, getRole5)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return &RoleResource{client: client.NewClient(srv.URL, "k", "org")}
}

func deleteRole5(t *testing.T, r *RoleResource) diag.Diagnostics {
	t.Helper()
	return deleteRole5With(t, r, 0)
}

// deleteRole5With destroys role 5 with replacement_role_id set in the
// state, or unset when replacementID is 0.
func deleteRole5With(t *testing.T, r *RoleResource, replacementID int64) diag.Diagnostics {
	t.Helper()
	ctx := context.Background()
	var sresp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sresp)
	state := tfsdk.State{Schema: sresp.Schema, Raw: tftypes.NewValue(sresp.Schema.Type().TerraformType(ctx), nil)}
	if d := state.SetAttribute(ctx, path.Root("id"), int64(5)); d.HasError() {
		t.Fatalf("set state: %v", d)
	}
	if replacementID != 0 {
		if d := state.SetAttribute(ctx, path.Root("replacement_role_id"), replacementID); d.HasError() {
			t.Fatalf("set state: %v", d)
		}
	}
	resp := resource.DeleteResponse{State: state}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
	return resp.Diagnostics
}

func TestRoleDelete_Replacement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		roles   string
		want    string // replacement role ID sent, "" for no DELETE
		wantErr bool
	}{
		{"null isAdmin is non-admin, Admin first is skipped", `[` + roleAdmin + `,` + roleDevs + `,` + roleMember + `]`, "2", false},
		{"explicit false is non-admin", `[` + roleAdmin + `,` + roleDevs + `,{"roleId":2,"orgId":"org","name":"Member","isAdmin":false}]`, "2", false},
		{"only Admin left: no promotion", `[` + roleAdmin + `,` + roleDevs + `]`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var deleted string
			r := roleDeleteTestServer(t, tc.roles, "[]", http.StatusOK, "", &deleted)
			if got := deleteRole5(t, r).HasError(); got != tc.wantErr {
				t.Errorf("error = %v, want %v", got, tc.wantErr)
			}
			if deleted != tc.want {
				t.Errorf("replacement role = %q, want %q", deleted, tc.want)
			}
		})
	}
}

func TestRoleDelete_404(t *testing.T) {
	before := `[` + roleAdmin + `,` + roleDevs + `,` + roleMember + `]`
	for _, tc := range []struct {
		name      string
		after     string
		wantError bool
	}{
		{"role already gone", `[` + roleAdmin + `,` + roleMember + `]`, false},
		{"replacement gone, role still there", `[` + roleAdmin + `,` + roleDevs + `]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var deleted string
			r := roleDeleteTestServer(t, before, tc.after, http.StatusNotFound, "", &deleted)
			if got := deleteRole5(t, r).HasError(); got != tc.wantError {
				t.Errorf("error = %v, want %v", got, tc.wantError)
			}
		})
	}
}

func TestRoleDelete_NotInProviderOrg(t *testing.T) {
	list := `[` + roleAdmin + `,` + roleMember + `]`
	for _, tc := range []struct {
		name      string
		getRole5  string
		wantError bool
	}{
		{"gone", "", false},
		{"role of another org", `{"roleId":5,"orgId":"other","name":"Devs","isAdmin":null}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var deleted string
			r := roleDeleteTestServer(t, list, list, http.StatusOK, tc.getRole5, &deleted)
			if got := deleteRole5(t, r).HasError(); got != tc.wantError {
				t.Errorf("error = %v, want %v", got, tc.wantError)
			}
			if deleted != "" {
				t.Errorf("DELETE sent with replacement %q, want none", deleted)
			}
		})
	}
}

func TestRoleDelete_ReplacementRoleID(t *testing.T) {
	roleOps := `{"roleId":7,"orgId":"org","name":"Ops","isAdmin":null}`
	list := `[` + roleAdmin + `,` + roleDevs + `,` + roleMember + `,` + roleOps + `]`
	for _, tc := range []struct {
		name    string
		id      int64
		want    string // replacement role ID sent, "" for no DELETE
		wantErr bool
	}{
		{"valid, not the default choice", 7, "7", false},
		{"unset keeps the first non-admin by name", 0, "2", false},
		{"admin role", 1, "", true},
		{"the deleted role itself", 5, "", true},
		{"unknown role", 99, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var deleted string
			r := roleDeleteTestServer(t, list, "[]", http.StatusOK, "", &deleted)
			if got := deleteRole5With(t, r, tc.id).HasError(); got != tc.wantErr {
				t.Errorf("error = %v, want %v", got, tc.wantErr)
			}
			if deleted != tc.want {
				t.Errorf("replacement role = %q, want %q", deleted, tc.want)
			}
		})
	}
}

// replacement_role_id is not returned by the API: Read keeps the state value
// (no diff), import starts from an empty model and leaves it null.
func TestRoleToModel_ReplacementRoleIDFromPrior(t *testing.T) {
	role := &client.Role{RoleID: 5, Name: "Devs"}
	m, diags := roleToModel(context.Background(), role, RoleResourceModel{ReplacementRoleID: types.Int64Value(7)})
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}
	if !m.ReplacementRoleID.Equal(types.Int64Value(7)) {
		t.Errorf("read: replacement_role_id = %v, want 7", m.ReplacementRoleID)
	}
	m, _ = roleToModel(context.Background(), role, RoleResourceModel{})
	if !m.ReplacementRoleID.IsNull() {
		t.Errorf("import: replacement_role_id = %v, want null", m.ReplacementRoleID)
	}
}

// updateRole5 runs Update from a full state of role 5 to a plan built by
// edit, against a server answering the update and the re-read. It returns
// the requests the server received and the resulting state.
func updateRole5(t *testing.T, edit func(*RoleResourceModel)) ([]string, RoleResourceModel) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"roleId":5,"orgId":"org","name":"Devs","description":"new","isAdmin":null,"sshSudoMode":"none","sshSudoCommands":"[]","sshUnixGroups":"[]"},"success":true,"error":false,"message":"ok","status":200}`))
	}))
	t.Cleanup(srv.Close)
	r := &RoleResource{client: client.NewClient(srv.URL, "k", "org")}

	ctx := context.Background()
	var sresp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sresp)
	empty, _ := types.ListValueFrom(ctx, types.StringType, []string{})
	prior := RoleResourceModel{
		ID: types.Int64Value(5), Name: types.StringValue("Devs"), Description: types.StringValue("old"),
		RequireDeviceApproval: types.BoolValue(false), AllowSSH: types.BoolValue(false),
		SSHSudoMode: types.StringValue("none"), SSHSudoCommands: empty, SSHCreateHomeDir: types.BoolValue(false),
		SSHUnixGroups: empty, IsAdmin: types.BoolValue(false), OrgName: types.StringValue("Org"),
		ReplacementRoleID: types.Int64Null(),
	}
	planned := prior
	edit(&planned)
	state := tfsdk.State{Schema: sresp.Schema}
	plan := tfsdk.Plan{Schema: sresp.Schema}
	if d := state.Set(ctx, &prior); d.HasError() {
		t.Fatalf("set state: %v", d)
	}
	if d := plan.Set(ctx, &planned); d.HasError() {
		t.Fatalf("set plan: %v", d)
	}
	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{State: state, Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("update: %v", resp.Diagnostics)
	}
	var got RoleResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	return calls, got
}

func TestRoleUpdate_OnlyReplacementRoleIDMakesNoCall(t *testing.T) {
	calls, got := updateRole5(t, func(m *RoleResourceModel) { m.ReplacementRoleID = types.Int64Value(7) })
	if len(calls) != 0 {
		t.Errorf("requests = %v, want none (a full update resends allow_ssh)", calls)
	}
	if !got.ReplacementRoleID.Equal(types.Int64Value(7)) {
		t.Errorf("replacement_role_id = %v, want 7", got.ReplacementRoleID)
	}
}

func TestRoleUpdate_OtherFieldStillUpdates(t *testing.T) {
	calls, got := updateRole5(t, func(m *RoleResourceModel) {
		m.ReplacementRoleID = types.Int64Value(7)
		m.Description = types.StringValue("new")
	})
	if len(calls) == 0 || calls[0] != "POST /v1/role/5" {
		t.Errorf("requests = %v, want the role update first", calls)
	}
	if !got.ReplacementRoleID.Equal(types.Int64Value(7)) || got.Description.ValueString() != "new" {
		t.Errorf("state = %v / %v, want 7 / new", got.ReplacementRoleID, got.Description)
	}
}

func TestRoleSchema_ReplacementRoleIDAtLeastOne(t *testing.T) {
	var sresp resource.SchemaResponse
	(&RoleResource{}).Schema(context.Background(), resource.SchemaRequest{}, &sresp)
	attr := sresp.Schema.Attributes["replacement_role_id"].(schema.Int64Attribute)
	vresp := validator.Int64Response{}
	for _, v := range attr.Validators {
		v.ValidateInt64(context.Background(), validator.Int64Request{Path: path.Root("replacement_role_id"), ConfigValue: types.Int64Value(0)}, &vresp)
	}
	if !vresp.Diagnostics.HasError() {
		t.Error("replacement_role_id = 0 must be refused")
	}
}
