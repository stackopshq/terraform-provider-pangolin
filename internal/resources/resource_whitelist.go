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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &ResourceWhitelistResource{}
	_ resource.ResourceWithImportState = &ResourceWhitelistResource{}
)

// ResourceWhitelistResource manages a whitelist email entry on an HTTP resource.
type ResourceWhitelistResource struct {
	client *client.Client
}

// ResourceWhitelistModel describes the resource data model.
type ResourceWhitelistModel struct {
	ResourceID types.Int64  `tfsdk:"resource_id"`
	Email      types.String `tfsdk:"email"`
}

// NewResourceWhitelistResource returns a new resource factory.
func NewResourceWhitelistResource() resource.Resource {
	return &ResourceWhitelistResource{}
}

func (r *ResourceWhitelistResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource_whitelist"
}

func (r *ResourceWhitelistResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Adds an email address to the whitelist of a Pangolin HTTP resource.",
		Attributes: map[string]schema.Attribute{
			"resource_id": schema.Int64Attribute{
				Description: "The ID of the HTTP resource.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"email": schema.StringAttribute{
				Description: "The email address to whitelist.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *ResourceWhitelistResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ResourceWhitelistResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ResourceWhitelistModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.AddWhitelistToResource(int(plan.ResourceID.ValueInt64()), plan.Email.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to add email to resource whitelist", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ResourceWhitelistResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// The Pangolin API does not expose an endpoint to list whitelist entries.
	// Preserve existing state as-is.
	var state ResourceWhitelistModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ResourceWhitelistResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "Whitelist entries cannot be updated in-place. Please recreate the resource.")
}

func (r *ResourceWhitelistResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ResourceWhitelistModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.RemoveWhitelistFromResource(int(state.ResourceID.ValueInt64()), state.Email.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to remove email from resource whitelist", err.Error())
		return
	}
}

func (r *ResourceWhitelistResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: "{resource_id}/{email}"
	idx := strings.Index(req.ID, "/")
	if idx < 0 {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected format: {resource_id}/{email}, got: %q", req.ID))
		return
	}

	resourceIDStr := req.ID[:idx]
	email := req.ID[idx+1:]

	resourceID, err := strconv.ParseInt(resourceIDStr, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid resource ID", fmt.Sprintf("Cannot parse %q as integer", resourceIDStr))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &ResourceWhitelistModel{
		ResourceID: types.Int64Value(resourceID),
		Email:      types.StringValue(email),
	})...)
}
