package resources

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &RoleResource{}
	_ resource.ResourceWithImportState = &RoleResource{}
)

// RoleResource manages a Pangolin role.
type RoleResource struct {
	client *client.Client
}

// RoleResourceModel describes the resource data model.
type RoleResourceModel struct {
	ID          types.Int64  `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`

	// SSH bastion fields
	RequireDeviceApproval types.Bool   `tfsdk:"require_device_approval"`
	AllowSSH              types.Bool   `tfsdk:"allow_ssh"`
	SSHSudoMode           types.String `tfsdk:"ssh_sudo_mode"`
	SSHSudoCommands       types.List   `tfsdk:"ssh_sudo_commands"`
	SSHCreateHomeDir      types.Bool   `tfsdk:"ssh_create_home_dir"`
	SSHUnixGroups         types.List   `tfsdk:"ssh_unix_groups"`

	// Read-only org context
	IsAdmin types.Bool   `tfsdk:"is_admin"`
	OrgName types.String `tfsdk:"org_name"`
}

// NewRoleResource returns a new resource factory.
func NewRoleResource() resource.Resource {
	return &RoleResource{}
}

func (r *RoleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (r *RoleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Pangolin role. Roles carry both standard RBAC fields (`name`, `description`) and " +
			"the SSH bastion configuration: whether members of this role may open SSH sessions through Pangolin, " +
			"how sudo is gated, which Unix groups to grant on the target host, and whether to auto-create a home directory.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "The numeric ID of the role.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The name of the role.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"description": schema.StringAttribute{
				Description: "The description of the role.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			// SSH bastion
			"require_device_approval": schema.BoolAttribute{
				Description: "Require an administrator to approve each new device before its user can use this role.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"allow_ssh": schema.BoolAttribute{
				Description: "Whether members of this role may open SSH sessions through the Pangolin bastion.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"ssh_sudo_mode": schema.StringAttribute{
				Description: "How sudo is gated for SSH sessions opened by members of this role. " +
					"`none` disables sudo, `full` allows any command, `restricted` allows only `ssh_sudo_commands`.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringvalidator.OneOf("none", "full", "restricted"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"ssh_sudo_commands": schema.ListAttribute{
				Description: "List of sudo command patterns allowed when `ssh_sudo_mode = \"restricted\"`. Ignored otherwise.",
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.UseStateForUnknown(),
				},
			},
			"ssh_create_home_dir": schema.BoolAttribute{
				Description: "Whether to create the user's home directory on the host when they first log in.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"ssh_unix_groups": schema.ListAttribute{
				Description: "Unix groups added to the user account on the host when they log in via SSH.",
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.UseStateForUnknown(),
				},
			},
			// Read-only org context
			"is_admin": schema.BoolAttribute{
				Description: "Whether this is the built-in admin role of the organization. Admin roles cannot be deleted.",
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"org_name": schema.StringAttribute{
				Description: "Display name of the organization the role belongs to.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *RoleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", "Expected *client.Client")
		return
	}
	r.client = c
}

func (r *RoleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan RoleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq := &client.CreateRoleRequest{
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
	}
	applyRoleSSHFields(ctx, plan, &createReq.RequireDeviceApproval, &createReq.AllowSSH,
		&createReq.SSHSudoMode, &createReq.SSHSudoCommands, &createReq.SSHCreateHomeDir,
		&createReq.SSHUnixGroups, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	role, err := r.client.CreateRole(ctx, createReq)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create role", err.Error())
		return
	}

	// Re-read to fetch the computed fields the create response may not echo.
	got, err := r.client.GetRoleByID(ctx, role.RoleID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read role after create", err.Error())
		return
	}
	state, diags := roleToModel(ctx, got)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *RoleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state RoleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	role, err := r.client.GetRoleByID(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read role", err.Error())
		return
	}
	next, diags := roleToModel(ctx, role)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *RoleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan RoleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateReq := &client.UpdateRoleRequest{
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
	}
	applyRoleSSHFields(ctx, plan, &updateReq.RequireDeviceApproval, &updateReq.AllowSSH,
		&updateReq.SSHSudoMode, &updateReq.SSHSudoCommands, &updateReq.SSHCreateHomeDir,
		&updateReq.SSHUnixGroups, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.UpdateRole(ctx, int(plan.ID.ValueInt64()), updateReq); err != nil {
		resp.Diagnostics.AddError("Failed to update role", err.Error())
		return
	}

	got, err := r.client.GetRoleByID(ctx, int(plan.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to read role after update", err.Error())
		return
	}
	state, diags := roleToModel(ctx, got)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *RoleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state RoleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The Pangolin API requires a replacement role for any users currently
	// assigned this role. Pick the first available role that is not the
	// one being deleted.
	roles, err := r.client.ListRoles(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list roles for deletion", err.Error())
		return
	}

	roleID := int(state.ID.ValueInt64())
	replacementID := roleID
	for _, role := range roles {
		if role.RoleID != roleID {
			replacementID = role.RoleID
			break
		}
	}

	if err := r.client.DeleteRole(ctx, roleID, replacementID); err != nil {
		resp.Diagnostics.AddError("Failed to delete role", err.Error())
		return
	}
}

func (r *RoleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ID", fmt.Sprintf("Cannot parse role ID %q as integer", req.ID))
		return
	}

	role, err := r.client.GetRoleByID(ctx, int(id))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import role", err.Error())
		return
	}
	state, diags := roleToModel(ctx, role)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// applyRoleSSHFields extracts the SSH bastion fields from a plan and
// writes them into the pointer-typed request fields used by the client
// for both Create and Update.
func applyRoleSSHFields(
	ctx context.Context,
	plan RoleResourceModel,
	requireDeviceApproval **bool, allowSSH **bool,
	sshSudoMode **string, sshSudoCommands *[]string,
	sshCreateHomeDir **bool, sshUnixGroups *[]string,
	diags *diag.Diagnostics,
) {
	if !plan.RequireDeviceApproval.IsNull() && !plan.RequireDeviceApproval.IsUnknown() {
		v := plan.RequireDeviceApproval.ValueBool()
		*requireDeviceApproval = &v
	}
	if !plan.AllowSSH.IsNull() && !plan.AllowSSH.IsUnknown() {
		v := plan.AllowSSH.ValueBool()
		*allowSSH = &v
	}
	if !plan.SSHSudoMode.IsNull() && !plan.SSHSudoMode.IsUnknown() {
		v := plan.SSHSudoMode.ValueString()
		*sshSudoMode = &v
	}
	if !plan.SSHCreateHomeDir.IsNull() && !plan.SSHCreateHomeDir.IsUnknown() {
		v := plan.SSHCreateHomeDir.ValueBool()
		*sshCreateHomeDir = &v
	}
	if !plan.SSHSudoCommands.IsNull() && !plan.SSHSudoCommands.IsUnknown() {
		var items []string
		diags.Append(plan.SSHSudoCommands.ElementsAs(ctx, &items, false)...)
		if !diags.HasError() {
			*sshSudoCommands = items
		}
	}
	if !plan.SSHUnixGroups.IsNull() && !plan.SSHUnixGroups.IsUnknown() {
		var items []string
		diags.Append(plan.SSHUnixGroups.ElementsAs(ctx, &items, false)...)
		if !diags.HasError() {
			*sshUnixGroups = items
		}
	}
}

// roleToModel maps a *client.Role onto a RoleResourceModel, decoding
// the JSON-serialized list fields (sshSudoCommands, sshUnixGroups)
// into proper Terraform string lists.
func roleToModel(ctx context.Context, role *client.Role) (RoleResourceModel, diag.Diagnostics) {
	var allDiags diag.Diagnostics

	sudoCommands, err := client.ParseSSHList(role.SSHSudoCommandsRaw)
	if err != nil {
		allDiags.AddError("Invalid ssh_sudo_commands shape", err.Error())
	}
	unixGroups, err := client.ParseSSHList(role.SSHUnixGroupsRaw)
	if err != nil {
		allDiags.AddError("Invalid ssh_unix_groups shape", err.Error())
	}

	sudoCommandsList, diags := types.ListValueFrom(ctx, types.StringType, sudoCommands)
	allDiags.Append(diags...)
	unixGroupsList, diags := types.ListValueFrom(ctx, types.StringType, unixGroups)
	allDiags.Append(diags...)

	isAdmin := types.BoolValue(false)
	if role.IsAdmin != nil {
		isAdmin = types.BoolValue(*role.IsAdmin)
	}

	return RoleResourceModel{
		ID:                    types.Int64Value(int64(role.RoleID)),
		Name:                  types.StringValue(role.Name),
		Description:           types.StringValue(role.Description),
		RequireDeviceApproval: types.BoolValue(role.RequireDeviceApproval),
		AllowSSH:              types.BoolValue(role.AllowSSH),
		SSHSudoMode:           types.StringValue(role.SSHSudoMode),
		SSHSudoCommands:       sudoCommandsList,
		SSHCreateHomeDir:      types.BoolValue(role.SSHCreateHomeDir),
		SSHUnixGroups:         unixGroupsList,
		IsAdmin:               isAdmin,
		OrgName:               types.StringValue(role.OrgName),
	}, allDiags
}
