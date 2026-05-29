package resources

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
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
	ID               types.Int64  `tfsdk:"id"`
	NiceID           types.String `tfsdk:"nice_id"`
	SiteID           types.Int64  `tfsdk:"site_id"`
	Name             types.String `tfsdk:"name"`
	Mode             types.String `tfsdk:"mode"`
	Destination      types.String `tfsdk:"destination"`
	Alias            types.String `tfsdk:"alias"`
	TCPPortRange     types.String `tfsdk:"tcp_port_range"`
	UDPPortRange     types.String `tfsdk:"udp_port_range"`
	DisableICMP      types.Bool   `tfsdk:"disable_icmp"`
	AuthDaemonMode   types.String `tfsdk:"auth_daemon_mode"`
	AuthDaemonPort   types.Int64  `tfsdk:"auth_daemon_port"`
	Enabled          types.Bool   `tfsdk:"enabled"`
	SSL              types.Bool   `tfsdk:"ssl"`
	NetworkID        types.Int64  `tfsdk:"network_id"`
	DefaultNetworkID types.Int64  `tfsdk:"default_network_id"`
	Scheme           types.String `tfsdk:"scheme"`
	ProxyPort        types.Int64  `tfsdk:"proxy_port"`
	DestinationPort  types.Int64  `tfsdk:"destination_port"`
	AliasAddress     types.String `tfsdk:"alias_address"`
	DomainID         types.String `tfsdk:"domain_id"`
	Subdomain        types.String `tfsdk:"subdomain"`
	FullDomain       types.String `tfsdk:"full_domain"`
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
		Description: "Manages a Pangolin private site resource (VPN-accessible endpoint).",
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
				Description: "The mode: `host`, `cidr`, or `http`.\n\n" +
					"`cidr` / `host` modes are L4 tunnels: the resource requires `alias` " +
					"and uses `tcp_port_range` / `udp_port_range` for traffic shaping.\n\n" +
					"`http` mode is an L7 HTTP proxy: the resource requires `domain_id`, " +
					"`subdomain`, `scheme` (`http` / `https`) and `destination_port`. The " +
					"server auto-assigns `tcp_port_range = \"443,80\"`, sets `disable_icmp = " +
					"true`, and leaves `alias` null.",
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("host", "cidr", "http"),
				},
			},
			"destination": schema.StringAttribute{
				Description: "The destination: hostname (`host` mode), CIDR (`cidr` mode), or backend IP / hostname (`http` mode).",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"alias": schema.StringAttribute{
				Description: "The internal DNS alias (e.g. `myservice.internal`). Required for `cidr` / `host` modes; ignored / null for `http` mode.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"tcp_port_range": schema.StringAttribute{
				Description: "TCP port range string. `*` for all, `` for none, or specific ports/ranges (e.g. `80,443,8080-8090`). For `mode = http` the server auto-assigns `\"443,80\"` and ignores any value set here.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"udp_port_range": schema.StringAttribute{
				Description: "UDP port range string. '*' for all, '' for none, or specific ports/ranges.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"disable_icmp": schema.BoolAttribute{
				Description: "Whether to disable ICMP. The server defaults to `false` for `cidr` / `host` modes and `true` for `mode = http`; leaving this unset lets the server pick.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
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
			"enabled": schema.BoolAttribute{
				Description: "Whether the resource is enabled. Read-only here; the API returns `true` on creation. Use a future toggle endpoint to disable.",
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"ssl": schema.BoolAttribute{
				Description: "Whether SSL is terminated by the proxy in front of this resource.",
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"network_id": schema.Int64Attribute{
				Description: "The numeric network ID the resource is bound to.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"default_network_id": schema.Int64Attribute{
				Description: "The default network ID, when set by the org configuration. Null when unset.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"scheme": schema.StringAttribute{
				Description: "Protocol scheme of the upstream backend. Required for `mode = http`; ignored for `cidr` / `host`. One of `http`, `https`.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("http", "https"),
				},
			},
			"proxy_port": schema.Int64Attribute{
				Description: "The proxy-facing port (server-assigned, read-only). The Pangolin API does NOT accept this as a create input — passing it returns `Validation error: Unrecognized key: \"proxyPort\"`.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"destination_port": schema.Int64Attribute{
				Description: "Backend destination port. Required for `mode = http`; ignored for `cidr` / `host`.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"alias_address": schema.StringAttribute{
				Description: "The resolved alias address, when applicable. Null otherwise.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"domain_id": schema.StringAttribute{
				Description: "ID of the parent domain (use the `pangolin_domains` data source to discover). Required for `mode = http`; ignored for `cidr` / `host`.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"subdomain": schema.StringAttribute{
				Description: "Subdomain to host the proxy at — combined with `domain_id` to produce `full_domain`. Required for `mode = http`; ignored for `cidr` / `host`.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"full_domain": schema.StringAttribute{
				Description: "The full FQDN of the resource. Null otherwise.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
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

	siteRes, err := r.client.CreateSiteResource(ctx, &client.CreateSiteResourceRequest{
		Name:            plan.Name.ValueString(),
		SiteID:          int(plan.SiteID.ValueInt64()),
		Mode:            plan.Mode.ValueString(),
		Destination:     plan.Destination.ValueString(),
		Alias:           plan.Alias.ValueString(),
		TCPPortRange:    plan.TCPPortRange.ValueString(),
		UDPPortRange:    plan.UDPPortRange.ValueString(),
		DisableICMP:     plan.DisableICMP.ValueBool(),
		AuthDaemonMode:  plan.AuthDaemonMode.ValueString(),
		DomainID:        plan.DomainID.ValueString(),
		Subdomain:       plan.Subdomain.ValueString(),
		Scheme:          plan.Scheme.ValueString(),
		DestinationPort: int(plan.DestinationPort.ValueInt64()),
		RoleIDs:         []int{},
		UserIDs:         []string{},
		ClientIDs:       []int{},
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to create site resource", err.Error())
		return
	}

	plan.ID = types.Int64Value(int64(siteRes.SiteResourceID))
	hydrateSiteResourceState(&plan, siteRes)

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

	hydrateSiteResourceState(&state, siteRes)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SitePrivateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan SitePrivateResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	siteRes, err := r.client.UpdateSiteResource(ctx, int(plan.ID.ValueInt64()), &client.UpdateSiteResourceRequest{
		Name:            plan.Name.ValueString(),
		SiteID:          int(plan.SiteID.ValueInt64()),
		Destination:     plan.Destination.ValueString(),
		Alias:           plan.Alias.ValueString(),
		TCPPortRange:    plan.TCPPortRange.ValueString(),
		UDPPortRange:    plan.UDPPortRange.ValueString(),
		DisableICMP:     plan.DisableICMP.ValueBool(),
		AuthDaemonMode:  plan.AuthDaemonMode.ValueString(),
		DomainID:        plan.DomainID.ValueString(),
		Subdomain:       plan.Subdomain.ValueString(),
		Scheme:          plan.Scheme.ValueString(),
		DestinationPort: int(plan.DestinationPort.ValueInt64()),
		RoleIDs:         []int{},
		UserIDs:         []string{},
		ClientIDs:       []int{},
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to update site resource", err.Error())
		return
	}

	hydrateSiteResourceState(&plan, siteRes)

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

	state := SitePrivateResourceModel{ID: types.Int64Value(int64(siteRes.SiteResourceID))}
	hydrateSiteResourceState(&state, siteRes)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// hydrateSiteResourceState copies every server-owned field from a
// freshly-fetched SiteResource into the TF state model. The caller
// must populate state.ID separately (it's the import-time key and
// not always present on the wire — e.g. on Create the response does
// carry siteResourceId, but we set it explicitly to keep the codepaths
// symmetric).
//
// site_id is derived from SiteIDs[0] when the slice is populated
// (LIST/Import codepath). On Create/Update the upstream response
// omits siteIds entirely, so the existing state value (the user's
// plan input) is preserved.
func hydrateSiteResourceState(state *SitePrivateResourceModel, siteRes *client.SiteResource) {
	state.NiceID = types.StringValue(siteRes.NiceID)
	state.Name = types.StringValue(siteRes.Name)
	state.Mode = types.StringValue(siteRes.Mode)
	state.Destination = types.StringValue(siteRes.Destination)
	state.Alias = types.StringValue(siteRes.Alias)
	state.TCPPortRange = types.StringValue(siteRes.TCPPortRange)
	state.UDPPortRange = types.StringValue(siteRes.UDPPortRange)
	state.DisableICMP = types.BoolValue(siteRes.DisableICMP)
	state.AuthDaemonMode = types.StringValue(siteRes.AuthDaemonMode)
	state.AuthDaemonPort = types.Int64Value(int64(siteRes.AuthDaemonPort))
	state.Enabled = types.BoolValue(siteRes.Enabled)
	state.SSL = types.BoolValue(siteRes.SSL)
	state.NetworkID = types.Int64Value(int64(siteRes.NetworkID))

	if len(siteRes.SiteIDs) > 0 {
		state.SiteID = types.Int64Value(int64(siteRes.SiteIDs[0]))
	}

	state.Scheme = tfconv.StringFromPtr(siteRes.Scheme)
	state.AliasAddress = tfconv.StringFromPtr(siteRes.AliasAddress)
	state.DomainID = tfconv.StringFromPtr(siteRes.DomainID)
	state.Subdomain = tfconv.StringFromPtr(siteRes.Subdomain)
	state.FullDomain = tfconv.StringFromPtr(siteRes.FullDomain)
	state.ProxyPort = tfconv.Int64FromIntPtr(siteRes.ProxyPort)
	state.DestinationPort = tfconv.Int64FromIntPtr(siteRes.DestinationPort)
	state.DefaultNetworkID = tfconv.Int64FromIntPtr(siteRes.DefaultNetworkID)
}
