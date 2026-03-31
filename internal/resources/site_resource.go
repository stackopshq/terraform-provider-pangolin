package resources

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
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
	ID             types.Int64  `tfsdk:"id"`
	NiceID         types.String `tfsdk:"nice_id"`
	SiteID         types.Int64  `tfsdk:"site_id"`
	Name           types.String `tfsdk:"name"`
	Mode           types.String `tfsdk:"mode"`
	Destination    types.String `tfsdk:"destination"`
	Alias          types.String `tfsdk:"alias"`
	TCPPortRange   types.String `tfsdk:"tcp_port_range"`
	UDPPortRange   types.String `tfsdk:"udp_port_range"`
	DisableICMP    types.Bool   `tfsdk:"disable_icmp"`
	AuthDaemonMode types.String `tfsdk:"auth_daemon_mode"`
	AuthDaemonPort types.Int64  `tfsdk:"auth_daemon_port"`
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
			},
			"mode": schema.StringAttribute{
				Description: "The mode: 'host' or 'cidr'.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"destination": schema.StringAttribute{
				Description: "The destination (hostname for 'host' mode, CIDR for 'cidr' mode).",
				Required:    true,
			},
			"alias": schema.StringAttribute{
				Description: "The internal DNS alias (e.g. 'myservice.internal').",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
			},
			"tcp_port_range": schema.StringAttribute{
				Description: "TCP port range string. '*' for all, '' for none, or specific ports/ranges (e.g. '80,443,8080-8090').",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("*"),
			},
			"udp_port_range": schema.StringAttribute{
				Description: "UDP port range string. '*' for all, '' for none, or specific ports/ranges.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
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

	siteRes, err := r.client.CreateSiteResource(&client.CreateSiteResourceRequest{
		Name:           plan.Name.ValueString(),
		SiteID:         int(plan.SiteID.ValueInt64()),
		Mode:           plan.Mode.ValueString(),
		Destination:    plan.Destination.ValueString(),
		Alias:          plan.Alias.ValueString(),
		TCPPortRange:   plan.TCPPortRange.ValueString(),
		UDPPortRange:   plan.UDPPortRange.ValueString(),
		DisableICMP:    plan.DisableICMP.ValueBool(),
		AuthDaemonMode: plan.AuthDaemonMode.ValueString(),
		RoleIDs:        []int{},
		UserIDs:        []string{},
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to create site resource", err.Error())
		return
	}

	plan.ID = types.Int64Value(int64(siteRes.SiteResourceID))
	plan.NiceID = types.StringValue(siteRes.NiceID)
	plan.AuthDaemonPort = types.Int64Value(int64(siteRes.AuthDaemonPort))

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SitePrivateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SitePrivateResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	siteRes, err := r.client.GetSiteResource(int(state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to read site resource", err.Error())
		return
	}

	state.NiceID = types.StringValue(siteRes.NiceID)
	state.SiteID = types.Int64Value(int64(siteRes.SiteID))
	state.Name = types.StringValue(siteRes.Name)
	state.Mode = types.StringValue(siteRes.Mode)
	state.Destination = types.StringValue(siteRes.Destination)
	state.Alias = types.StringValue(siteRes.Alias)
	state.TCPPortRange = types.StringValue(siteRes.TCPPortRange)
	state.UDPPortRange = types.StringValue(siteRes.UDPPortRange)
	state.DisableICMP = types.BoolValue(siteRes.DisableICMP)
	state.AuthDaemonMode = types.StringValue(siteRes.AuthDaemonMode)
	state.AuthDaemonPort = types.Int64Value(int64(siteRes.AuthDaemonPort))

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SitePrivateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "Site resources cannot be updated in-place. Please recreate the resource.")
}

func (r *SitePrivateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SitePrivateResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteSiteResource(int(state.ID.ValueInt64()))
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

	siteRes, err := r.client.GetSiteResource(int(id))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import site resource", err.Error())
		return
	}

	state := SitePrivateResourceModel{
		ID:             types.Int64Value(int64(siteRes.SiteResourceID)),
		NiceID:         types.StringValue(siteRes.NiceID),
		SiteID:         types.Int64Value(int64(siteRes.SiteID)),
		Name:           types.StringValue(siteRes.Name),
		Mode:           types.StringValue(siteRes.Mode),
		Destination:    types.StringValue(siteRes.Destination),
		Alias:          types.StringValue(siteRes.Alias),
		TCPPortRange:   types.StringValue(siteRes.TCPPortRange),
		UDPPortRange:   types.StringValue(siteRes.UDPPortRange),
		DisableICMP:    types.BoolValue(siteRes.DisableICMP),
		AuthDaemonMode: types.StringValue(siteRes.AuthDaemonMode),
		AuthDaemonPort: types.Int64Value(int64(siteRes.AuthDaemonPort)),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
