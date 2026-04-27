package resources

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &ResourceRoleResource{}
	_ resource.ResourceWithImportState = &ResourceRoleResource{}
)

// ResourceRoleResource manages the assignment of a role to an HTTP resource.
type ResourceRoleResource struct {
	client *client.Client
}

// ResourceRoleModel describes the resource data model.
type ResourceRoleModel struct {
	ResourceID types.Int64 `tfsdk:"resource_id"`
	RoleID     types.Int64 `tfsdk:"role_id"`
}

// NewResourceRoleResource returns a new resource factory.
func NewResourceRoleResource() resource.Resource {
	return &ResourceRoleResource{}
}

func (r *ResourceRoleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource_role"
}

func (r *ResourceRoleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Assigns a role to a Pangolin HTTP resource.",
		Attributes: map[string]schema.Attribute{
			"resource_id": schema.Int64Attribute{
				Description: "The ID of the HTTP resource.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"role_id": schema.Int64Attribute{
				Description: "The ID of the role to assign.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *ResourceRoleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ResourceRoleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ResourceRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.AddRoleToResource(ctx, int(plan.ResourceID.ValueInt64()), int(plan.RoleID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to assign role to resource", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ResourceRoleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// The Pangolin API does not expose an endpoint to list roles assigned to a resource.
	// Preserve existing state as-is.
	var state ResourceRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ResourceRoleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "Role assignments cannot be updated in-place. Please recreate the resource.")
}

func (r *ResourceRoleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ResourceRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.RemoveRoleFromResource(ctx, int(state.ResourceID.ValueInt64()), int(state.RoleID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to remove role from resource", err.Error())
		return
	}
}

func (r *ResourceRoleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: "{resource_id}/{role_id}"
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected format: {resource_id}/{role_id}, got: %q", req.ID))
		return
	}

	resourceID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid resource ID", fmt.Sprintf("Cannot parse %q as integer", parts[0]))
		return
	}

	roleID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid role ID", fmt.Sprintf("Cannot parse %q as integer", parts[1]))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &ResourceRoleModel{
		ResourceID: types.Int64Value(resourceID),
		RoleID:     types.Int64Value(roleID),
	})...)
}
