package resources

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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
	_ resource.Resource                = &ResourceUserResource{}
	_ resource.ResourceWithImportState = &ResourceUserResource{}
)

// ResourceUserResource manages the assignment of a user to an HTTP resource.
type ResourceUserResource struct {
	client *client.Client
}

// ResourceUserModel describes the resource data model.
type ResourceUserModel struct {
	ResourceID types.Int64  `tfsdk:"resource_id"`
	UserID     types.String `tfsdk:"user_id"`
}

// NewResourceUserResource returns a new resource factory.
func NewResourceUserResource() resource.Resource {
	return &ResourceUserResource{}
}

func (r *ResourceUserResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource_user"
}

func (r *ResourceUserResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Assigns a user to a Pangolin HTTP resource.",
		Attributes: map[string]schema.Attribute{
			"resource_id": schema.Int64Attribute{
				Description: "The ID of the HTTP resource.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"user_id": schema.StringAttribute{
				Description: "The ID of the user to assign.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
		},
	}
}

func (r *ResourceUserResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ResourceUserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ResourceUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.AddUserToResource(ctx, int(plan.ResourceID.ValueInt64()), plan.UserID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to assign user to resource", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ResourceUserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// The Pangolin API does not expose an endpoint to list users assigned to a resource.
	// Preserve existing state as-is.
	var state ResourceUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ResourceUserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "User assignments cannot be updated in-place. Please recreate the resource.")
}

func (r *ResourceUserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ResourceUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.RemoveUserFromResource(ctx, int(state.ResourceID.ValueInt64()), state.UserID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to remove user from resource", err.Error())
		return
	}
}

func (r *ResourceUserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: "{resource_id}/{user_id}"
	idx := strings.Index(req.ID, "/")
	if idx < 0 {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected format: {resource_id}/{user_id}, got: %q", req.ID))
		return
	}

	resourceIDStr := req.ID[:idx]
	userID := req.ID[idx+1:]

	resourceID, err := strconv.ParseInt(resourceIDStr, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid resource ID", fmt.Sprintf("Cannot parse %q as integer", resourceIDStr))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &ResourceUserModel{
		ResourceID: types.Int64Value(resourceID),
		UserID:     types.StringValue(userID),
	})...)
}
