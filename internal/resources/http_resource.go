package resources

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &HTTPResource{}
	_ resource.ResourceWithImportState = &HTTPResource{}
)

// HTTPResource defines the resource implementation.
type HTTPResource struct {
	client *client.Client
}

// HTTPResourceModel describes the resource data model.
type HTTPResourceModel struct {
	ID         types.Int64  `tfsdk:"id"`
	NiceID     types.String `tfsdk:"nice_id"`
	Name       types.String `tfsdk:"name"`
	Subdomain  types.String `tfsdk:"subdomain"`
	FullDomain types.String `tfsdk:"full_domain"`
	DomainID   types.String `tfsdk:"domain_id"`
	Protocol   types.String `tfsdk:"protocol"`
}

// NewHTTPResource returns a new resource factory.
func NewHTTPResource() resource.Resource {
	return &HTTPResource{}
}

func (r *HTTPResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource"
}

func (r *HTTPResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Pangolin HTTP resource (public reverse proxy endpoint).",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "The numeric ID of the resource.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"nice_id": schema.StringAttribute{
				Description: "The human-readable ID of the resource.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The name of the resource.",
				Required:    true,
			},
			"subdomain": schema.StringAttribute{
				Description: "The subdomain for the resource. Set to null to use the base domain.",
				Optional:    true,
			},
			"full_domain": schema.StringAttribute{
				Description: "The full domain of the resource (computed).",
				Computed:    true,
			},
			"domain_id": schema.StringAttribute{
				Description: "The domain ID to attach this resource to.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"protocol": schema.StringAttribute{
				Description: "The protocol (tcp or udp). Defaults to tcp.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("tcp"),
			},
		},
	}
}

func (r *HTTPResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *HTTPResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HTTPResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq := &client.CreateResourceRequest{
		Name:     plan.Name.ValueString(),
		HTTP:     true,
		DomainID: plan.DomainID.ValueString(),
		Protocol: plan.Protocol.ValueString(),
	}

	if !plan.Subdomain.IsNull() && !plan.Subdomain.IsUnknown() {
		subdomain := plan.Subdomain.ValueString()
		createReq.Subdomain = &subdomain
	}

	resource, err := r.client.CreateResource(createReq)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create resource", err.Error())
		return
	}

	plan.ID = types.Int64Value(int64(resource.ResourceID))
	plan.NiceID = types.StringValue(resource.NiceID)
	plan.FullDomain = types.StringValue(resource.FullDomain)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *HTTPResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HTTPResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resource, err := r.client.GetResource(int(state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to read resource", err.Error())
		return
	}

	state.NiceID = types.StringValue(resource.NiceID)
	state.Name = types.StringValue(resource.Name)
	state.FullDomain = types.StringValue(resource.FullDomain)
	state.DomainID = types.StringValue(resource.DomainID)
	if resource.Subdomain != "" {
		state.Subdomain = types.StringValue(resource.Subdomain)
	} else {
		state.Subdomain = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HTTPResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "HTTP resources cannot be updated in-place. Please recreate the resource.")
}

func (r *HTTPResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HTTPResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteResource(int(state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to delete resource", err.Error())
		return
	}
}

func (r *HTTPResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ID", fmt.Sprintf("Cannot parse resource ID %q as integer", req.ID))
		return
	}

	res, err := r.client.GetResource(int(id))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import resource", err.Error())
		return
	}

	state := HTTPResourceModel{
		ID:         types.Int64Value(int64(res.ResourceID)),
		NiceID:     types.StringValue(res.NiceID),
		Name:       types.StringValue(res.Name),
		FullDomain: types.StringValue(res.FullDomain),
		DomainID:   types.StringValue(res.DomainID),
	}
	if res.Subdomain != "" {
		state.Subdomain = types.StringValue(res.Subdomain)
	} else {
		state.Subdomain = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
