package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &ResourceTargetsDataSource{}

// ResourceTargetsDataSource lists the backend targets of a single resource,
// including the health-check and routing details the list endpoint
// returns on top of the CRUD subset.
type ResourceTargetsDataSource struct {
	client *client.Client
}

// ResourceTargetItemModel mirrors the fields of one target in the list
// view. Health-check fields are nullable on the wire (no probe set =
// null); pointer types in the client struct land them as types.*Null()
// in the Terraform model.
type ResourceTargetItemModel struct {
	ID                  types.Int64  `tfsdk:"id"`
	ResourceID          types.Int64  `tfsdk:"resource_id"`
	SiteID              types.Int64  `tfsdk:"site_id"`
	IP                  types.String `tfsdk:"ip"`
	Method              types.String `tfsdk:"method"`
	Port                types.Int64  `tfsdk:"port"`
	Enabled             types.Bool   `tfsdk:"enabled"`
	SiteType            types.String `tfsdk:"site_type"`
	SiteName            types.String `tfsdk:"site_name"`
	HCEnabled           types.Bool   `tfsdk:"hc_enabled"`
	HCPath              types.String `tfsdk:"hc_path"`
	HCScheme            types.String `tfsdk:"hc_scheme"`
	HCMode              types.String `tfsdk:"hc_mode"`
	HCHostname          types.String `tfsdk:"hc_hostname"`
	HCPort              types.Int64  `tfsdk:"hc_port"`
	HCInterval          types.Int64  `tfsdk:"hc_interval"`
	HCUnhealthyInterval types.Int64  `tfsdk:"hc_unhealthy_interval"`
	HCTimeout           types.Int64  `tfsdk:"hc_timeout"`
	HCHeaders           types.String `tfsdk:"hc_headers"`
	HCFollowRedirects   types.Bool   `tfsdk:"hc_follow_redirects"`
	HCMethod            types.String `tfsdk:"hc_method"`
	HCStatus            types.String `tfsdk:"hc_status"`
	HCHealth            types.String `tfsdk:"hc_health"`
	HCTLSServerName     types.String `tfsdk:"hc_tls_server_name"`
	Path                types.String `tfsdk:"path"`
	PathMatchType       types.String `tfsdk:"path_match_type"`
	RewritePath         types.String `tfsdk:"rewrite_path"`
	RewritePathType     types.String `tfsdk:"rewrite_path_type"`
	Priority            types.Int64  `tfsdk:"priority"`
}

// ResourceTargetsDataSourceModel describes the data source data model.
type ResourceTargetsDataSourceModel struct {
	ResourceID types.Int64               `tfsdk:"resource_id"`
	Targets    []ResourceTargetItemModel `tfsdk:"targets"`
}

// NewResourceTargetsDataSource returns a new data source factory.
func NewResourceTargetsDataSource() datasource.DataSource {
	return &ResourceTargetsDataSource{}
}

func (d *ResourceTargetsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource_targets"
}

func (d *ResourceTargetsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the backend targets of a Pangolin HTTP resource, " +
			"with the full list-view detail (site labels, health-check configuration, path routing) " +
			"that the per-id `GET /target/{id}` endpoint does not echo.\n\n" +
			"> **Note:** Health-check fields (`hc_*`) and routing fields (`path`, `rewrite_*`, etc.) come " +
			"back as `null` when no probe / no routing is configured — surfaced as Terraform `null` in those cases.",
		Attributes: map[string]schema.Attribute{
			"resource_id": schema.Int64Attribute{
				Description: "Numeric ID of the HTTP resource whose targets to list.",
				Required:    true,
			},
			"targets": schema.ListNestedAttribute{
				Description: "The targets backing the resource.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                    schema.Int64Attribute{Description: "Numeric ID of the target.", Computed: true},
						"resource_id":           schema.Int64Attribute{Description: "ID of the resource this target belongs to.", Computed: true},
						"site_id":               schema.Int64Attribute{Description: "ID of the site that serves this target.", Computed: true},
						"ip":                    schema.StringAttribute{Description: "Target IP or hostname.", Computed: true},
						"method":                schema.StringAttribute{Description: "Scheme used to reach the target (`http` or `https`).", Computed: true},
						"port":                  schema.Int64Attribute{Description: "Target port.", Computed: true},
						"enabled":               schema.BoolAttribute{Description: "Whether the target is currently enabled.", Computed: true},
						"site_type":             schema.StringAttribute{Description: "Site type (e.g. `newt`).", Computed: true},
						"site_name":             schema.StringAttribute{Description: "Site display name.", Computed: true},
						"hc_enabled":            schema.BoolAttribute{Description: "Whether the active health check is enabled.", Computed: true},
						"hc_path":               schema.StringAttribute{Description: "Health-check URL path.", Computed: true},
						"hc_scheme":             schema.StringAttribute{Description: "Health-check scheme (`http` / `https`).", Computed: true},
						"hc_mode":               schema.StringAttribute{Description: "Health-check mode (e.g. `http`, `tcp`).", Computed: true},
						"hc_hostname":           schema.StringAttribute{Description: "Hostname used by the health-check probe.", Computed: true},
						"hc_port":               schema.Int64Attribute{Description: "Port used by the health-check probe.", Computed: true},
						"hc_interval":           schema.Int64Attribute{Description: "Probe interval in seconds when the target is healthy.", Computed: true},
						"hc_unhealthy_interval": schema.Int64Attribute{Description: "Probe interval in seconds when the target is unhealthy.", Computed: true},
						"hc_timeout":            schema.Int64Attribute{Description: "Probe timeout in seconds.", Computed: true},
						"hc_headers":            schema.StringAttribute{Description: "Probe request headers as a JSON-string.", Computed: true},
						"hc_follow_redirects":   schema.BoolAttribute{Description: "Whether the probe follows HTTP redirects.", Computed: true},
						"hc_method":             schema.StringAttribute{Description: "HTTP method of the probe (e.g. `GET`).", Computed: true},
						"hc_status":             schema.StringAttribute{Description: "Status string returned by the probe (e.g. `200`).", Computed: true},
						"hc_health":             schema.StringAttribute{Description: "Current health summary (`unknown`, `healthy`, `unhealthy`).", Computed: true},
						"hc_tls_server_name":    schema.StringAttribute{Description: "TLS SNI used when probing over HTTPS.", Computed: true},
						"path":                  schema.StringAttribute{Description: "URL path prefix this target serves.", Computed: true},
						"path_match_type":       schema.StringAttribute{Description: "How `path` is matched (e.g. `prefix`, `exact`, `regex`).", Computed: true},
						"rewrite_path":          schema.StringAttribute{Description: "Path the request is rewritten to before being forwarded.", Computed: true},
						"rewrite_path_type":     schema.StringAttribute{Description: "Rewrite mode.", Computed: true},
						"priority":              schema.Int64Attribute{Description: "Routing priority — lower numbers win.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *ResourceTargetsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ResourceTargetsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg ResourceTargetsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	targets, err := d.client.ListResourceTargets(ctx, int(cfg.ResourceID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to list resource targets", err.Error())
		return
	}

	cfg.Targets = make([]ResourceTargetItemModel, len(targets))
	for i, t := range targets {
		cfg.Targets[i] = ResourceTargetItemModel{
			ID:                  types.Int64Value(int64(t.TargetID)),
			ResourceID:          types.Int64Value(int64(t.ResourceID)),
			SiteID:              types.Int64Value(int64(t.SiteID)),
			IP:                  types.StringValue(t.IP),
			Method:              types.StringValue(t.Method),
			Port:                types.Int64Value(int64(t.Port)),
			Enabled:             types.BoolValue(t.Enabled),
			SiteType:            types.StringValue(t.SiteType),
			SiteName:            types.StringValue(t.SiteName),
			HCEnabled:           types.BoolValue(t.HCEnabled),
			HCPath:              nullableString(t.HCPath),
			HCScheme:            nullableString(t.HCScheme),
			HCMode:              nullableString(t.HCMode),
			HCHostname:          nullableString(t.HCHostname),
			HCPort:              nullableIntFromIntPtr(t.HCPort),
			HCInterval:          nullableIntFromIntPtr(t.HCInterval),
			HCUnhealthyInterval: nullableIntFromIntPtr(t.HCUnhealthyInterval),
			HCTimeout:           nullableIntFromIntPtr(t.HCTimeout),
			HCHeaders:           nullableString(t.HCHeaders),
			HCFollowRedirects:   nullableBool(t.HCFollowRedirects),
			HCMethod:            nullableString(t.HCMethod),
			HCStatus:            nullableString(t.HCStatus),
			HCHealth:            types.StringValue(t.HCHealth),
			HCTLSServerName:     nullableString(t.HCTLSServerName),
			Path:                nullableString(t.Path),
			PathMatchType:       nullableString(t.PathMatchType),
			RewritePath:         nullableString(t.RewritePath),
			RewritePathType:     nullableString(t.RewritePathType),
			Priority:            nullableIntFromIntPtr(t.Priority),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

func nullableBool(p *bool) types.Bool {
	if p == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*p)
}

// nullableIntFromIntPtr handles a *int (not *int64) returned by the API.
func nullableIntFromIntPtr(p *int) types.Int64 {
	if p == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*p))
}
