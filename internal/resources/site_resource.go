package resources

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &SitePrivateResource{}
	_ resource.ResourceWithImportState = &SitePrivateResource{}
)

// SitePrivateResource defines the resource implementation.
type SitePrivateResource struct {
	client *client.Client
}

// SitePrivateResourceModel describes the resource data model.
type SitePrivateResourceModel struct {
	ID              types.Int64  `tfsdk:"id"`
	NiceID          types.String `tfsdk:"nice_id"`
	SiteID          types.Int64  `tfsdk:"site_id"`
	Name            types.String `tfsdk:"name"`
	Mode            types.String `tfsdk:"mode"`
	Destination     types.String `tfsdk:"destination"`
	DestinationPort types.Int64  `tfsdk:"destination_port"`
	Alias           types.String `tfsdk:"alias"`
	DomainID        types.String `tfsdk:"domain_id"`
	Subdomain       types.String `tfsdk:"subdomain"`
	FullDomain      types.String `tfsdk:"full_domain"`
	Scheme          types.String `tfsdk:"scheme"`
	SSL             types.Bool   `tfsdk:"ssl"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	TCPPortRange    types.String `tfsdk:"tcp_port_range"`
	UDPPortRange    types.String `tfsdk:"udp_port_range"`
	DisableICMP     types.Bool   `tfsdk:"disable_icmp"`
	AuthDaemonMode  types.String `tfsdk:"auth_daemon_mode"`
	AuthDaemonPort  types.Int64  `tfsdk:"auth_daemon_port"`
}

// NewSitePrivateResource returns a new resource factory.
func NewSitePrivateResource() resource.Resource {
	return &SitePrivateResource{}
}

func (r *SitePrivateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site_resource"
}

func (r *SitePrivateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Pangolin private site resource (VPN-accessible endpoint, including private HTTP resources).",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "The numeric ID of the site resource.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"nice_id": schema.StringAttribute{
				Description: "The human-readable ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"site_id": schema.Int64Attribute{
				Description: "The site ID this resource belongs to.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The name of the private resource.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"mode": schema.StringAttribute{
				Description: "The mode: 'host', 'cidr', or 'http'.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("host", "cidr", "http"),
				},
			},
			"destination": schema.StringAttribute{
				Description: "The destination hostname for host/http mode, or CIDR for cidr mode.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"destination_port": schema.Int64Attribute{
				Description: "The destination port for private HTTP mode.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"alias": schema.StringAttribute{
				Description: "The internal DNS alias for host/cidr private resources.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"domain_id": schema.StringAttribute{
				Description: "The Pangolin domain ID used for private HTTP full-domain routing.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"subdomain": schema.StringAttribute{
				Description: "The subdomain used with domain_id for private HTTP full-domain routing.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"full_domain": schema.StringAttribute{
				Description: "The computed full domain for private HTTP mode.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"scheme": schema.StringAttribute{
				Description: "The destination scheme for private HTTP mode: 'http' or 'https'.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("http", "https"),
				},
			},
			"ssl": schema.BoolAttribute{
				Description: "Whether Pangolin should use SSL for the private HTTP resource.",
				Optional:    true,
				Computed:    true,
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether the private resource is enabled. Defaults to true.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"tcp_port_range": schema.StringAttribute{
				Description: "TCP port range string. '*' for all, '' for none, or specific ports/ranges (e.g. '80,443,8080-8090'). Ignored for private HTTP mode.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("*"),
			},
			"udp_port_range": schema.StringAttribute{
				Description: "UDP port range string. '*' for all, '' for none, or specific ports/ranges. Ignored for private HTTP mode.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"disable_icmp": schema.BoolAttribute{
				Description: "Whether to disable ICMP. Defaults to false.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"auth_daemon_mode": schema.StringAttribute{
				Description: "Auth daemon mode: 'site' or 'remote'. Defaults to 'site'.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("site"),
			},
			"auth_daemon_port": schema.Int64Attribute{
				Description: "The auth daemon port (computed by the API).",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *SitePrivateResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *SitePrivateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SitePrivateResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	siteRes, err := r.client.CreateSiteResource(ctx, buildSiteResourceCreateRequest(plan))
	if err != nil {
		resp.Diagnostics.AddError("Failed to create site resource", err.Error())
		return
	}

	applySiteResourceResponse(&plan, siteRes)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SitePrivateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SitePrivateResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	siteRes, err := r.client.GetSiteResource(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read site resource", err.Error())
		return
	}

	applySiteResourceResponse(&state, siteRes)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SitePrivateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan SitePrivateResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	siteRes, err := r.client.UpdateSiteResource(ctx, int(plan.ID.ValueInt64()), buildSiteResourceUpdateRequest(plan))
	if err != nil {
		resp.Diagnostics.AddError("Failed to update site resource", err.Error())
		return
	}

	applySiteResourceResponse(&plan, siteRes)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SitePrivateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SitePrivateResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteSiteResource(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to delete site resource", err.Error())
		return
	}
}

func (r *SitePrivateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ID", fmt.Sprintf("Cannot parse site resource ID %q as integer", req.ID))
		return
	}

	siteRes, err := r.client.GetSiteResource(ctx, int(id))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import site resource", err.Error())
		return
	}

	state := SitePrivateResourceModel{
		ID: types.Int64Value(int64(siteRes.SiteResourceID)),
	}
	applySiteResourceResponse(&state, siteRes)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func buildSiteResourceCreateRequest(plan SitePrivateResourceModel) *client.CreateSiteResourceRequest {
	return &client.CreateSiteResourceRequest{
		Name:            plan.Name.ValueString(),
		SiteID:          int(plan.SiteID.ValueInt64()),
		Mode:            plan.Mode.ValueString(),
		Destination:     plan.Destination.ValueString(),
		DestinationPort: optionalIntPointer(plan.DestinationPort),
		Alias:           optionalStringValue(plan.Alias),
		DomainID:        optionalStringPointer(plan.DomainID),
		Subdomain:       optionalStringPointer(plan.Subdomain),
		Scheme:          optionalStringPointer(plan.Scheme),
		SSL:             optionalBoolPointer(plan.SSL),
		Enabled:         optionalBoolPointer(plan.Enabled),
		TCPPortRange:    privatePortRangeForRequest(plan.Mode, plan.TCPPortRange),
		UDPPortRange:    privatePortRangeForRequest(plan.Mode, plan.UDPPortRange),
		DisableICMP:     plan.DisableICMP.ValueBool(),
		AuthDaemonMode:  plan.AuthDaemonMode.ValueString(),
		RoleIDs:         []int{},
		UserIDs:         []string{},
		ClientIDs:       []int{},
	}
}

func buildSiteResourceUpdateRequest(plan SitePrivateResourceModel) *client.UpdateSiteResourceRequest {
	return &client.UpdateSiteResourceRequest{
		Name:            plan.Name.ValueString(),
		SiteID:          int(plan.SiteID.ValueInt64()),
		Destination:     plan.Destination.ValueString(),
		DestinationPort: optionalIntPointer(plan.DestinationPort),
		Alias:           optionalStringValue(plan.Alias),
		DomainID:        optionalStringPointer(plan.DomainID),
		Subdomain:       optionalStringPointer(plan.Subdomain),
		Scheme:          optionalStringPointer(plan.Scheme),
		SSL:             optionalBoolPointer(plan.SSL),
		Enabled:         optionalBoolPointer(plan.Enabled),
		TCPPortRange:    privatePortRangeForRequest(plan.Mode, plan.TCPPortRange),
		UDPPortRange:    privatePortRangeForRequest(plan.Mode, plan.UDPPortRange),
		DisableICMP:     plan.DisableICMP.ValueBool(),
		AuthDaemonMode:  plan.AuthDaemonMode.ValueString(),
		RoleIDs:         []int{},
		UserIDs:         []string{},
		ClientIDs:       []int{},
	}
}

func applySiteResourceResponse(model *SitePrivateResourceModel, siteRes *client.SiteResource) {
	model.ID = types.Int64Value(int64(siteRes.SiteResourceID))
	model.NiceID = stringValueOrCurrent(siteRes.NiceID, model.NiceID)
	model.SiteID = types.Int64Value(int64(siteRes.SiteID))
	model.Name = types.StringValue(siteRes.Name)
	model.Mode = types.StringValue(siteRes.Mode)
	model.Destination = types.StringValue(siteRes.Destination)
	model.DestinationPort = int64ValueOrCurrent(siteRes.DestinationPort, model.DestinationPort)
	model.Alias = stringValueOrCurrent(siteRes.Alias, model.Alias)
	model.DomainID = stringValueOrCurrent(siteRes.DomainID, model.DomainID)
	model.Subdomain = stringValueOrCurrent(siteRes.Subdomain, model.Subdomain)
	model.FullDomain = stringValueOrCurrent(siteRes.FullDomain, model.FullDomain)
	model.Scheme = stringValueOrCurrent(siteRes.Scheme, model.Scheme)
	model.SSL = types.BoolValue(siteRes.SSL)
	model.Enabled = types.BoolValue(siteRes.Enabled)
	model.TCPPortRange = stringValueOrCurrent(siteRes.TCPPortRange, model.TCPPortRange)
	model.UDPPortRange = stringValueOrCurrent(siteRes.UDPPortRange, model.UDPPortRange)
	model.DisableICMP = types.BoolValue(siteRes.DisableICMP)
	model.AuthDaemonMode = stringValueOrCurrent(siteRes.AuthDaemonMode, model.AuthDaemonMode)
	model.AuthDaemonPort = int64ValueOrCurrent(siteRes.AuthDaemonPort, model.AuthDaemonPort)
}

func privatePortRangeForRequest(mode types.String, value types.String) string {
	if !mode.IsUnknown() && !mode.IsNull() && mode.ValueString() == "http" {
		return ""
	}
	return optionalStringValue(value)
}

func optionalStringPointer(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	v := value.ValueString()
	return &v
}

func optionalStringValue(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return ""
	}
	return value.ValueString()
}

func optionalIntPointer(value types.Int64) *int {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	v := int(value.ValueInt64())
	return &v
}

func optionalBoolPointer(value types.Bool) *bool {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	v := value.ValueBool()
	return &v
}

func stringValueOrCurrent(value string, current types.String) types.String {
	if value != "" {
		return types.StringValue(value)
	}
	if !current.IsNull() && !current.IsUnknown() {
		return current
	}
	return types.StringNull()
}

func int64ValueOrCurrent(value int, current types.Int64) types.Int64 {
	if value != 0 {
		return types.Int64Value(int64(value))
	}
	if !current.IsNull() && !current.IsUnknown() {
		return current
	}
	return types.Int64Null()
}
