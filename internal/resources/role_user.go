package resources

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &RoleUserResource{}
	_ resource.ResourceWithImportState = &RoleUserResource{}
)

// RoleUserResource manages the assignment of a user to an organization role.
type RoleUserResource struct {
	client *client.Client
}

// RoleUserModel describes the resource data model.
type RoleUserModel struct {
	RoleID types.String `tfsdk:"role_id"`
	UserID types.String `tfsdk:"user_id"`
}

// NewRoleUserResource returns a new resource factory.
func NewRoleUserResource() resource.Resource {
	return &RoleUserResource{}
}

func (r *RoleUserResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role_user"
}

func (r *RoleUserResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Assigns a user to a Pangolin organization role.",
		Attributes: map[string]schema.Attribute{
			"role_id": schema.StringAttribute{
				Description: "The ID of the role.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_id": schema.StringAttribute{
				Description: "The ID of the user to assign.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *RoleUserResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *RoleUserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan RoleUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	roleID, err := strconv.Atoi(plan.RoleID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid role_id", fmt.Sprintf("Cannot parse %q as integer", plan.RoleID.ValueString()))
		return
	}

	if err := r.client.AddUserToRole(roleID, plan.UserID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to assign user to role", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *RoleUserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state RoleUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	roleID, err := strconv.Atoi(state.RoleID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid role_id", fmt.Sprintf("Cannot parse %q as integer", state.RoleID.ValueString()))
		return
	}

	users, err := r.client.ListRoleUsers(roleID)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read role users", err.Error())
		return
	}

	for _, uid := range users {
		if uid == state.UserID.ValueString() {
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}

	// Association no longer exists — remove from state.
	resp.State.RemoveResource(ctx)
}

func (r *RoleUserResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "Role user assignments cannot be updated in-place. Please recreate the resource.")
}

func (r *RoleUserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state RoleUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	roleID, err := strconv.Atoi(state.RoleID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid role_id", fmt.Sprintf("Cannot parse %q as integer", state.RoleID.ValueString()))
		return
	}

	if err := r.client.RemoveUserFromRole(roleID, state.UserID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to remove user from role", err.Error())
		return
	}
}

func (r *RoleUserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: "{role_id}/{user_id}"
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected format: {role_id}/{user_id}, got: %q", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &RoleUserModel{
		RoleID: types.StringValue(parts[0]),
		UserID: types.StringValue(parts[1]),
	})...)
}
