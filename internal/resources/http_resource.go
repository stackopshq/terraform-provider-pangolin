package resources

import (
	"context"
	"errors"
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
	ID                    types.Int64  `tfsdk:"id"`
	NiceID                types.String `tfsdk:"nice_id"`
	Name                  types.String `tfsdk:"name"`
	Subdomain             types.String `tfsdk:"subdomain"`
	FullDomain            types.String `tfsdk:"full_domain"`
	DomainID              types.String `tfsdk:"domain_id"`
	Protocol              types.String `tfsdk:"protocol"`
	SSO                   types.Bool   `tfsdk:"sso"`
	SSL                   types.Bool   `tfsdk:"ssl"`
	Enabled               types.Bool   `tfsdk:"enabled"`
	BlockAccess           types.Bool   `tfsdk:"block_access"`
	EmailWhitelistEnabled types.Bool   `tfsdk:"email_whitelist_enabled"`
	ApplyRules            types.Bool   `tfsdk:"apply_rules"`
	StickySession         types.Bool   `tfsdk:"sticky_session"`
	TLSServerName         types.String `tfsdk:"tls_server_name"`
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
			"sso": schema.BoolAttribute{
				Description: "Enable Pangolin SSO authentication on this resource. Set to false to make the resource publicly accessible.",
				Optional:    true,
				Computed:    true,
			},
			"ssl": schema.BoolAttribute{
				Description: "Enable SSL towards the backend.",
				Optional:    true,
				Computed:    true,
			},
			"enabled": schema.BoolAttribute{
				Description: "Enable or disable the resource.",
				Optional:    true,
				Computed:    true,
			},
			"block_access": schema.BoolAttribute{
				Description: "Block all access to the resource.",
				Optional:    true,
				Computed:    true,
			},
			"email_whitelist_enabled": schema.BoolAttribute{
				Description: "Enable the email whitelist on this resource.",
				Optional:    true,
				Computed:    true,
			},
			"apply_rules": schema.BoolAttribute{
				Description: "Enable evaluation of access rules on this resource.",
				Optional:    true,
				Computed:    true,
			},
			"sticky_session": schema.BoolAttribute{
				Description: "Enable sticky sessions (persistent sessions) on this resource.",
				Optional:    true,
				Computed:    true,
			},
			"tls_server_name": schema.StringAttribute{
				Description: "TLS server name for the backend. Set to null to clear.",
				Optional:    true,
				Computed:    true,
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

	// Apply user-specified settings (sso, ssl, etc.) via update.
	// buildUpdateRequest reads from plan which still holds the user's intended values.
	updated, err := r.client.UpdateResource(int(plan.ID.ValueInt64()), buildHTTPResourceUpdateRequest(plan))
	if err != nil {
		resp.Diagnostics.AddError("Failed to apply resource settings after creation", err.Error())
		return
	}

	plan = applyHTTPResourceResponse(plan, updated)
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
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read resource", err.Error())
		return
	}

	state = applyHTTPResourceResponse(state, resource)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HTTPResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HTTPResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	res, err := r.client.UpdateResource(int(plan.ID.ValueInt64()), buildHTTPResourceUpdateRequest(plan))
	if err != nil {
		resp.Diagnostics.AddError("Failed to update resource", err.Error())
		return
	}

	plan = applyHTTPResourceResponse(plan, res)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
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

	state := HTTPResourceModel{ID: types.Int64Value(int64(res.ResourceID))}
	state = applyHTTPResourceResponse(state, res)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// buildHTTPResourceUpdateRequest builds an UpdateResourceRequest from the model,
// sending only fields that the user explicitly set (non-unknown).
func buildHTTPResourceUpdateRequest(plan HTTPResourceModel) *client.UpdateResourceRequest {
	req := &client.UpdateResourceRequest{
		Name: plan.Name.ValueString(),
	}
	if !plan.Subdomain.IsNull() && !plan.Subdomain.IsUnknown() {
		s := plan.Subdomain.ValueString()
		req.Subdomain = &s
	}
	if !plan.SSO.IsUnknown() {
		v := plan.SSO.ValueBool()
		req.SSO = &v
	}
	if !plan.SSL.IsUnknown() {
		v := plan.SSL.ValueBool()
		req.SSL = &v
	}
	if !plan.Enabled.IsUnknown() {
		v := plan.Enabled.ValueBool()
		req.Enabled = &v
	}
	if !plan.BlockAccess.IsUnknown() {
		v := plan.BlockAccess.ValueBool()
		req.BlockAccess = &v
	}
	if !plan.EmailWhitelistEnabled.IsUnknown() {
		v := plan.EmailWhitelistEnabled.ValueBool()
		req.EmailWhitelistEnabled = &v
	}
	if !plan.ApplyRules.IsUnknown() {
		v := plan.ApplyRules.ValueBool()
		req.ApplyRules = &v
	}
	if !plan.StickySession.IsUnknown() {
		v := plan.StickySession.ValueBool()
		req.StickySession = &v
	}
	if !plan.TLSServerName.IsUnknown() {
		if plan.TLSServerName.IsNull() {
			req.TLSServerName = nil
		} else {
			s := plan.TLSServerName.ValueString()
			req.TLSServerName = &s
		}
	}
	return req
}

// applyHTTPResourceResponse copies API response fields into the model.
func applyHTTPResourceResponse(m HTTPResourceModel, res *client.Resource) HTTPResourceModel {
	m.NiceID = types.StringValue(res.NiceID)
	m.Name = types.StringValue(res.Name)
	m.FullDomain = types.StringValue(res.FullDomain)
	m.DomainID = types.StringValue(res.DomainID)
	if res.Subdomain != "" {
		m.Subdomain = types.StringValue(res.Subdomain)
	} else {
		m.Subdomain = types.StringNull()
	}
	m.SSO = types.BoolValue(res.SSO)
	m.SSL = types.BoolValue(res.SSL)
	m.Enabled = types.BoolValue(res.Enabled)
	m.BlockAccess = types.BoolValue(res.BlockAccess)
	m.EmailWhitelistEnabled = types.BoolValue(res.EmailWhitelistEnabled)
	m.ApplyRules = types.BoolValue(res.ApplyRules)
	m.StickySession = types.BoolValue(res.StickySession)
	if res.TLSServerName != nil {
		m.TLSServerName = types.StringValue(*res.TLSServerName)
	} else {
		m.TLSServerName = types.StringNull()
	}
	return m
}
