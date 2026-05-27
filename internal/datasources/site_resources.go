package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &SiteResourcesDataSource{}

// SiteResourcesDataSource defines the data source implementation.
type SiteResourcesDataSource struct {
	client *client.Client
}

// SiteResourceItemModel describes a single site resource in the data source.
type SiteResourceItemModel struct {
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

// SiteResourcesDataSourceModel describes the data source data model.
type SiteResourcesDataSourceModel struct {
	SiteResources []SiteResourceItemModel `tfsdk:"site_resources"`
}

// NewSiteResourcesDataSource returns a new data source factory.
func NewSiteResourcesDataSource() datasource.DataSource {
	return &SiteResourcesDataSource{}
}

func (d *SiteResourcesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site_resources"
}

func (d *SiteResourcesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves the list of private site resources for the organization.",
		Attributes: map[string]schema.Attribute{
			"site_resources": schema.ListNestedAttribute{
				Description: "List of private site resources.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                 schema.Int64Attribute{Description: "The numeric site resource ID.", Computed: true},
						"nice_id":            schema.StringAttribute{Description: "The human-readable ID.", Computed: true},
						"site_id":            schema.Int64Attribute{Description: "The parent site ID (first entry of the upstream `siteIds` array).", Computed: true},
						"name":               schema.StringAttribute{Description: "The resource name.", Computed: true},
						"mode":               schema.StringAttribute{Description: "The mode (host or cidr).", Computed: true},
						"destination":        schema.StringAttribute{Description: "The destination.", Computed: true},
						"alias":              schema.StringAttribute{Description: "The internal DNS alias.", Computed: true},
						"tcp_port_range":     schema.StringAttribute{Description: "TCP port range.", Computed: true},
						"udp_port_range":     schema.StringAttribute{Description: "UDP port range.", Computed: true},
						"disable_icmp":       schema.BoolAttribute{Description: "Whether ICMP is disabled.", Computed: true},
						"auth_daemon_mode":   schema.StringAttribute{Description: "Auth daemon mode.", Computed: true},
						"auth_daemon_port":   schema.Int64Attribute{Description: "Auth daemon port.", Computed: true},
						"enabled":            schema.BoolAttribute{Description: "Whether the resource is enabled.", Computed: true},
						"ssl":                schema.BoolAttribute{Description: "Whether SSL is terminated by the proxy.", Computed: true},
						"network_id":         schema.Int64Attribute{Description: "The numeric network ID.", Computed: true},
						"default_network_id": schema.Int64Attribute{Description: "The default network ID. Null when unset.", Computed: true},
						"scheme":             schema.StringAttribute{Description: "The protocol scheme. HTTP-mode resources only; null otherwise.", Computed: true},
						"proxy_port":         schema.Int64Attribute{Description: "The proxy-facing port. HTTP-mode resources only; null otherwise.", Computed: true},
						"destination_port":   schema.Int64Attribute{Description: "The destination port. HTTP-mode resources only; null otherwise.", Computed: true},
						"alias_address":      schema.StringAttribute{Description: "The resolved alias address. Null when unset.", Computed: true},
						"domain_id":          schema.StringAttribute{Description: "The associated domain ID. Null when unset.", Computed: true},
						"subdomain":          schema.StringAttribute{Description: "The configured subdomain. Null when unset.", Computed: true},
						"full_domain":        schema.StringAttribute{Description: "The full FQDN of the resource. Null when unset.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *SiteResourcesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected DataSource Configure Type", "Expected *client.Client")
		return
	}
	d.client = c
}

func (d *SiteResourcesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	siteResources, err := d.client.ListSiteResources(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list site resources", err.Error())
		return
	}

	state := SiteResourcesDataSourceModel{SiteResources: []SiteResourceItemModel{}}
	for _, sr := range siteResources {
		item := SiteResourceItemModel{
			ID:               types.Int64Value(int64(sr.SiteResourceID)),
			NiceID:           types.StringValue(sr.NiceID),
			Name:             types.StringValue(sr.Name),
			Mode:             types.StringValue(sr.Mode),
			Destination:      types.StringValue(sr.Destination),
			Alias:            types.StringValue(sr.Alias),
			TCPPortRange:     types.StringValue(sr.TCPPortRange),
			UDPPortRange:     types.StringValue(sr.UDPPortRange),
			DisableICMP:      types.BoolValue(sr.DisableICMP),
			AuthDaemonMode:   types.StringValue(sr.AuthDaemonMode),
			AuthDaemonPort:   types.Int64Value(int64(sr.AuthDaemonPort)),
			Enabled:          types.BoolValue(sr.Enabled),
			SSL:              types.BoolValue(sr.SSL),
			NetworkID:        types.Int64Value(int64(sr.NetworkID)),
			DefaultNetworkID: nullInt64FromIntPtr(sr.DefaultNetworkID),
			Scheme:           nullStringFromPtr(sr.Scheme),
			ProxyPort:        nullInt64FromIntPtr(sr.ProxyPort),
			DestinationPort:  nullInt64FromIntPtr(sr.DestinationPort),
			AliasAddress:     nullStringFromPtr(sr.AliasAddress),
			DomainID:         nullStringFromPtr(sr.DomainID),
			Subdomain:        nullStringFromPtr(sr.Subdomain),
			FullDomain:       nullStringFromPtr(sr.FullDomain),
		}
		if len(sr.SiteIDs) > 0 {
			item.SiteID = types.Int64Value(int64(sr.SiteIDs[0]))
		} else {
			item.SiteID = types.Int64Null()
		}
		state.SiteResources = append(state.SiteResources, item)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func nullStringFromPtr(p *string) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue(*p)
}

func nullInt64FromIntPtr(p *int) types.Int64 {
	if p == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*p))
}
