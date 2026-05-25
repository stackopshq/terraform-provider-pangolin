package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &SiteDataSource{}

// SiteDataSource exposes a single Pangolin site looked up by nice ID
// — the human-readable identifier shown in the Pangolin UI. The
// per-niceId GET endpoint returns the richer site payload (WireGuard
// keys, traffic counters, status, endpoint), so this is the preferred
// way to discover a known site for downstream reference.
type SiteDataSource struct {
	client *client.Client
}

// SiteDataSourceModel describes the data source data model. nice_id is
// the input; everything else is read-only.
type SiteDataSourceModel struct {
	NiceID              types.String  `tfsdk:"nice_id"`
	ID                  types.Int64   `tfsdk:"id"`
	Name                types.String  `tfsdk:"name"`
	Type                types.String  `tfsdk:"type"`
	Online              types.Bool    `tfsdk:"online"`
	Address             types.String  `tfsdk:"address"`
	DockerSocketEnabled types.Bool    `tfsdk:"docker_socket_enabled"`
	ExitNodeID          types.Int64   `tfsdk:"exit_node_id"`
	PubKey              types.String  `tfsdk:"pub_key"`
	Subnet              types.String  `tfsdk:"subnet"`
	MegabytesIn         types.Float64 `tfsdk:"megabytes_in"`
	MegabytesOut        types.Float64 `tfsdk:"megabytes_out"`
	LastBandwidthUpdate types.String  `tfsdk:"last_bandwidth_update"`
	LastPing            types.Int64   `tfsdk:"last_ping"`
	Endpoint            types.String  `tfsdk:"endpoint"`
	PublicKey           types.String  `tfsdk:"public_key"`
	LastHolePunch       types.Int64   `tfsdk:"last_hole_punch"`
	ListenPort          types.Int64   `tfsdk:"listen_port"`
	Status              types.String  `tfsdk:"status"`
	NewtID              types.String  `tfsdk:"newt_id"`
}

// NewSiteDataSource returns a new data source factory.
func NewSiteDataSource() datasource.DataSource {
	return &SiteDataSource{}
}

func (d *SiteDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site"
}

func (d *SiteDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up a single Pangolin site by its nice ID (the human-readable identifier shown in the Pangolin UI). " +
			"Returns the full site payload — traffic counters, WireGuard public keys, last ping, status, etc. — that the list " +
			"endpoint does not expose.",
		Attributes: map[string]schema.Attribute{
			"nice_id":               schema.StringAttribute{Description: "Nice ID of the site to look up (e.g. `smart-marbled-salamander`).", Required: true},
			"id":                    schema.Int64Attribute{Description: "Numeric ID of the site.", Computed: true},
			"name":                  schema.StringAttribute{Description: "Display name of the site.", Computed: true},
			"type":                  schema.StringAttribute{Description: "Site type (e.g. `newt`).", Computed: true},
			"online":                schema.BoolAttribute{Description: "Whether the site is currently online.", Computed: true},
			"address":               schema.StringAttribute{Description: "WireGuard address (e.g. `100.90.128.0/24`).", Computed: true},
			"docker_socket_enabled": schema.BoolAttribute{Description: "Whether Docker socket access is enabled on the site.", Computed: true},
			"exit_node_id":          schema.Int64Attribute{Description: "ID of the exit node this site is bound to (null when none).", Computed: true},
			"pub_key":               schema.StringAttribute{Description: "WireGuard public key of the site connector (newt).", Computed: true},
			"subnet":                schema.StringAttribute{Description: "WireGuard subnet allocated to the site.", Computed: true},
			"megabytes_in":          schema.Float64Attribute{Description: "Total bytes received by the site, expressed in MB.", Computed: true},
			"megabytes_out":         schema.Float64Attribute{Description: "Total bytes sent by the site, expressed in MB.", Computed: true},
			"last_bandwidth_update": schema.StringAttribute{Description: "ISO 8601 / RFC 3339 timestamp of the last bandwidth counter update.", Computed: true},
			"last_ping":             schema.Int64Attribute{Description: "Unix epoch seconds of the last successful keep-alive from the connector.", Computed: true},
			"endpoint":              schema.StringAttribute{Description: "Current public WireGuard endpoint (`ip:port`) of the connector.", Computed: true},
			"public_key":            schema.StringAttribute{Description: "WireGuard public key of the central exit / hub side of the tunnel.", Computed: true},
			"last_hole_punch":       schema.Int64Attribute{Description: "Unix epoch seconds of the last successful NAT hole punch.", Computed: true},
			"listen_port":           schema.Int64Attribute{Description: "Listen port currently used by the connector behind NAT.", Computed: true},
			"status":                schema.StringAttribute{Description: "Lifecycle status of the site (e.g. `approved`).", Computed: true},
			"newt_id":               schema.StringAttribute{Description: "Newt connector identifier assigned to the site.", Computed: true},
		},
	}
}

func (d *SiteDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *SiteDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg SiteDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	site, err := d.client.GetSiteByNiceID(ctx, d.client.OrgID, cfg.NiceID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read site", err.Error())
		return
	}

	cfg.ID = types.Int64Value(int64(site.SiteID))
	cfg.Name = types.StringValue(site.Name)
	cfg.Type = types.StringValue(site.Type)
	cfg.Online = types.BoolValue(site.Online)
	cfg.Address = types.StringValue(site.Address)
	cfg.DockerSocketEnabled = types.BoolValue(site.DockerSocketEnabled)
	cfg.ExitNodeID = nullableIntFromIntPtr(site.ExitNodeID)
	cfg.PubKey = types.StringValue(site.PubKey)
	cfg.Subnet = types.StringValue(site.Subnet)
	cfg.MegabytesIn = types.Float64Value(site.MegabytesIn)
	cfg.MegabytesOut = types.Float64Value(site.MegabytesOut)
	cfg.LastBandwidthUpdate = types.StringValue(site.LastBandwidthUpdate)
	cfg.LastPing = types.Int64Value(site.LastPing)
	cfg.Endpoint = types.StringValue(site.Endpoint)
	cfg.PublicKey = types.StringValue(site.PublicKey)
	cfg.LastHolePunch = types.Int64Value(site.LastHolePunch)
	cfg.ListenPort = types.Int64Value(int64(site.ListenPort))
	cfg.Status = types.StringValue(site.Status)
	cfg.NewtID = types.StringValue(site.NewtID)

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
