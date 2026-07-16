package resources

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &UserRoleResource{}
	_ resource.ResourceWithImportState = &UserRoleResource{}
)

// UserRoleResource binds an additional role to a user - cumulative,
// unlike pangolin_role_user which assigns a single role to a user.
//
// Use this resource when a user must hold multiple roles
// simultaneously (e.g. Member + Admin). Use pangolin_role_user when
// the user should hold exactly one role and any previous bindings
// should be stripped.
type UserRoleResource struct {
	client *client.Client
}

// UserRoleResourceModel describes the resource data model.
type UserRoleResourceModel struct {
	UserID types.String `tfsdk:"user_id"`
	RoleID types.Int64  `tfsdk:"role_id"`
}

// NewUserRoleResource returns a new resource factory.
func NewUserRoleResource() resource.Resource {
	return &UserRoleResource{}
}

func (r *UserRoleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_role"
}

func (r *UserRoleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Binds an additional role to a user (cumulative). Unlike `pangolin_role_user`, which assigns a " +
			"single role to a user and strips other bindings, this resource adds one more role on top of any " +
			"existing bindings. Use it when a user must hold multiple roles simultaneously.\n\n" +
			"> **Note:** Both attributes are `RequiresReplace` - the binding is identified by its `(user_id, role_id)` " +
			"pair, so any change creates a new binding and removes the old one.",
		Attributes: map[string]schema.Attribute{
			"user_id": schema.StringAttribute{
				Description: "The Pangolin user ID (UUID-like string).",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"role_id": schema.Int64Attribute{
				Description: "The numeric ID of the role to bind.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
		},
	}
}

func (r *UserRoleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *UserRoleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan UserRoleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.AddRoleToUser(ctx, plan.UserID.ValueString(), int(plan.RoleID.ValueInt64())); err != nil {
		resp.Diagnostics.AddError("Failed to bind role to user", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *UserRoleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state UserRoleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	has, err := r.client.UserHasRole(ctx, state.UserID.ValueString(), int(state.RoleID.ValueInt64()))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			// User no longer exists.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read user/role binding", err.Error())
		return
	}
	if !has {
		// Role was detached out of band.
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *UserRoleResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"user_role update not supported",
		"Both attributes are RequiresReplace, so Update should never fire. File a bug if you see this error.",
	)
}

func (r *UserRoleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state UserRoleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.RemoveRoleFromUser(ctx, state.UserID.ValueString(), int(state.RoleID.ValueInt64())); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			// Already detached / user gone - treat as success.
			return
		}
		resp.Diagnostics.AddError("Failed to remove role from user", err.Error())
		return
	}
}

func (r *UserRoleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Composite ID: {user_id}/{role_id}
	idx := strings.LastIndex(req.ID, "/")
	if idx < 0 {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected format: {user_id}/{role_id}, got: %q", req.ID))
		return
	}
	userID := req.ID[:idx]
	roleIDStr := req.ID[idx+1:]
	roleID, err := strconv.ParseInt(roleIDStr, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid role ID", fmt.Sprintf("Cannot parse %q as integer", roleIDStr))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &UserRoleResourceModel{
		UserID: types.StringValue(userID),
		RoleID: types.Int64Value(roleID),
	})...)
}
