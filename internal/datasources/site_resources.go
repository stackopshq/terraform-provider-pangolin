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
						"id":               schema.Int64Attribute{Description: "The numeric site resource ID.", Computed: true},
						"nice_id":          schema.StringAttribute{Description: "The human-readable ID.", Computed: true},
						"site_id":          schema.Int64Attribute{Description: "The parent site ID.", Computed: true},
						"name":             schema.StringAttribute{Description: "The resource name.", Computed: true},
						"mode":             schema.StringAttribute{Description: "The mode (host, cidr, or http).", Computed: true},
						"destination":      schema.StringAttribute{Description: "The destination.", Computed: true},
						"destination_port": schema.Int64Attribute{Description: "The destination port for private HTTP mode.", Computed: true},
						"alias":            schema.StringAttribute{Description: "The internal DNS alias.", Computed: true},
						"domain_id":        schema.StringAttribute{Description: "The Pangolin domain ID for private HTTP mode.", Computed: true},
						"subdomain":        schema.StringAttribute{Description: "The subdomain for private HTTP mode.", Computed: true},
						"full_domain":      schema.StringAttribute{Description: "The computed full domain for private HTTP mode.", Computed: true},
						"scheme":           schema.StringAttribute{Description: "The destination scheme for private HTTP mode.", Computed: true},
						"ssl":              schema.BoolAttribute{Description: "Whether SSL is enabled for the private HTTP resource.", Computed: true},
						"enabled":          schema.BoolAttribute{Description: "Whether the private resource is enabled.", Computed: true},
						"tcp_port_range":   schema.StringAttribute{Description: "TCP port range.", Computed: true},
						"udp_port_range":   schema.StringAttribute{Description: "UDP port range.", Computed: true},
						"disable_icmp":     schema.BoolAttribute{Description: "Whether ICMP is disabled.", Computed: true},
						"auth_daemon_mode": schema.StringAttribute{Description: "Auth daemon mode.", Computed: true},
						"auth_daemon_port": schema.Int64Attribute{Description: "Auth daemon port.", Computed: true},
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
		state.SiteResources = append(state.SiteResources, SiteResourceItemModel{
			ID:              types.Int64Value(int64(sr.SiteResourceID)),
			NiceID:          nullableStringValue(sr.NiceID),
			SiteID:          types.Int64Value(int64(sr.SiteID)),
			Name:            types.StringValue(sr.Name),
			Mode:            types.StringValue(sr.Mode),
			Destination:     types.StringValue(sr.Destination),
			DestinationPort: nullableInt64Value(sr.DestinationPort),
			Alias:           nullableStringValue(sr.Alias),
			DomainID:        nullableStringValue(sr.DomainID),
			Subdomain:       nullableStringValue(sr.Subdomain),
			FullDomain:      nullableStringValue(sr.FullDomain),
			Scheme:          nullableStringValue(sr.Scheme),
			SSL:             types.BoolValue(sr.SSL),
			Enabled:         types.BoolValue(sr.Enabled),
			TCPPortRange:    nullableStringValue(sr.TCPPortRange),
			UDPPortRange:    nullableStringValue(sr.UDPPortRange),
			DisableICMP:     types.BoolValue(sr.DisableICMP),
			AuthDaemonMode:  nullableStringValue(sr.AuthDaemonMode),
			AuthDaemonPort:  nullableInt64Value(sr.AuthDaemonPort),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func nullableStringValue(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

func nullableInt64Value(value int) types.Int64 {
	if value == 0 {
		return types.Int64Null()
	}
	return types.Int64Value(int64(value))
}
