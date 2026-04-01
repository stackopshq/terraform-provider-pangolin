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
	_ resource.Resource                = &SiteResourceClientResource{}
	_ resource.ResourceWithImportState = &SiteResourceClientResource{}
)

// SiteResourceClientResource manages the assignment of an OLM client to a private site resource.
type SiteResourceClientResource struct {
	client *client.Client
}

// SiteResourceClientModel describes the resource data model.
type SiteResourceClientModel struct {
	SiteResourceID types.Int64 `tfsdk:"site_resource_id"`
	ClientID       types.Int64 `tfsdk:"client_id"`
}

// NewSiteResourceClientResource returns a new resource factory.
func NewSiteResourceClientResource() resource.Resource {
	return &SiteResourceClientResource{}
}

func (r *SiteResourceClientResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site_resource_client"
}

func (r *SiteResourceClientResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Assigns an OLM client to a Pangolin private site resource.",
		Attributes: map[string]schema.Attribute{
			"site_resource_id": schema.Int64Attribute{
				Description: "The ID of the private site resource.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"client_id": schema.Int64Attribute{
				Description: "The ID of the OLM client to assign.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *SiteResourceClientResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *SiteResourceClientResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SiteResourceClientModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.AddClientToSiteResource(int(plan.SiteResourceID.ValueInt64()), int(plan.ClientID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to assign client to site resource", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SiteResourceClientResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// The Pangolin API does not expose an endpoint to list clients assigned to a site resource.
	// Preserve existing state as-is.
	var state SiteResourceClientModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SiteResourceClientResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "Client assignments cannot be updated in-place. Please recreate the resource.")
}

func (r *SiteResourceClientResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SiteResourceClientModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.RemoveClientFromSiteResource(int(state.SiteResourceID.ValueInt64()), int(state.ClientID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to remove client from site resource", err.Error())
		return
	}
}

func (r *SiteResourceClientResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: "{site_resource_id}/{client_id}"
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected format: {site_resource_id}/{client_id}, got: %q", req.ID))
		return
	}

	siteResourceID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid site resource ID", fmt.Sprintf("Cannot parse %q as integer", parts[0]))
		return
	}

	clientID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid client ID", fmt.Sprintf("Cannot parse %q as integer", parts[1]))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &SiteResourceClientModel{
		SiteResourceID: types.Int64Value(siteResourceID),
		ClientID:       types.Int64Value(clientID),
	})...)
}
