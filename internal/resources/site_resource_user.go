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
	_ resource.Resource                = &SiteResourceUserResource{}
	_ resource.ResourceWithImportState = &SiteResourceUserResource{}
)

// SiteResourceUserResource manages the assignment of a user to a private site resource.
type SiteResourceUserResource struct {
	client *client.Client
}

// SiteResourceUserModel describes the resource data model.
type SiteResourceUserModel struct {
	SiteResourceID types.Int64  `tfsdk:"site_resource_id"`
	UserID         types.String `tfsdk:"user_id"`
}

// NewSiteResourceUserResource returns a new resource factory.
func NewSiteResourceUserResource() resource.Resource {
	return &SiteResourceUserResource{}
}

func (r *SiteResourceUserResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site_resource_user"
}

func (r *SiteResourceUserResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Assigns a user to a Pangolin private site resource.",
		Attributes: map[string]schema.Attribute{
			"site_resource_id": schema.Int64Attribute{
				Description: "The ID of the private site resource.",
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

func (r *SiteResourceUserResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *SiteResourceUserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SiteResourceUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.AddUserToSiteResource(ctx, int(plan.SiteResourceID.ValueInt64()), plan.UserID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to assign user to site resource", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SiteResourceUserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// The Pangolin API does not expose an endpoint to list users assigned to a site resource.
	// Preserve existing state as-is.
	var state SiteResourceUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SiteResourceUserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "User assignments cannot be updated in-place. Please recreate the resource.")
}

func (r *SiteResourceUserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SiteResourceUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.RemoveUserFromSiteResource(ctx, int(state.SiteResourceID.ValueInt64()), state.UserID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to remove user from site resource", err.Error())
		return
	}
}

func (r *SiteResourceUserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: "{site_resource_id}/{user_id}"
	idx := strings.Index(req.ID, "/")
	if idx < 0 {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected format: {site_resource_id}/{user_id}, got: %q", req.ID))
		return
	}

	siteResourceIDStr := req.ID[:idx]
	userID := req.ID[idx+1:]

	siteResourceID, err := strconv.ParseInt(siteResourceIDStr, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid site resource ID", fmt.Sprintf("Cannot parse %q as integer", siteResourceIDStr))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &SiteResourceUserModel{
		SiteResourceID: types.Int64Value(siteResourceID),
		UserID:         types.StringValue(userID),
	})...)
}
