package datasources

import (
	"context"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &AuditLogsDataSource{}

// AuditLogsDataSource backs the pangolin_access_logs, pangolin_action_logs
// and pangolin_connection_logs data sources. All three share the same
// wire wrapper ({log, pagination, filterAttributes}) but each stream
// carries a different per-entry schema and a different set of
// filterAttributes dimensions. Since the entry shape is not stable
// across streams (and empty on the tested tenant), entries are
// surfaced as raw JSON strings and filter_attributes is a
// map(string, string) whose values are JSON-encoded arrays.
type AuditLogsDataSource struct {
	client *client.Client
	kind   client.AuditLogKind
	name   string // used for both TypeName suffix and description
}

// NewAccessLogsDataSource returns the pangolin_access_logs data source.
func NewAccessLogsDataSource() datasource.DataSource {
	return &AuditLogsDataSource{kind: client.AuditLogAccess, name: "access"}
}

// NewActionLogsDataSource returns the pangolin_action_logs data source.
func NewActionLogsDataSource() datasource.DataSource {
	return &AuditLogsDataSource{kind: client.AuditLogAction, name: "action"}
}

// NewConnectionLogsDataSource returns the pangolin_connection_logs data source.
func NewConnectionLogsDataSource() datasource.DataSource {
	return &AuditLogsDataSource{kind: client.AuditLogConnection, name: "connection"}
}

// AuditLogsDataSourceModel is the shared model for the three audit log
// streams.
type AuditLogsDataSourceModel struct {
	// Inputs (optional filters)
	TimeStart  types.String `tfsdk:"time_start"`
	TimeEnd    types.String `tfsdk:"time_end"`
	Action     types.String `tfsdk:"action"`
	Actor      types.String `tfsdk:"actor"`
	ResourceID types.String `tfsdk:"resource_id"`
	Limit      types.Int64  `tfsdk:"limit"`
	Offset     types.Int64  `tfsdk:"offset"`

	// Outputs
	Entries          []types.String    `tfsdk:"entries"`
	Total            types.Int64       `tfsdk:"total"`
	FilterAttributes map[string]string `tfsdk:"filter_attributes"`
}

func (d *AuditLogsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.name + "_logs"
}

func (d *AuditLogsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	humanKind := map[string]string{
		"access":     "per-request access decisions on protected HTTP resources",
		"action":     "administrative/mutation actions performed via the Integration API or web UI",
		"connection": "VPN and tunnel connection lifecycle events from OLM clients and site connectors",
	}[d.name]

	resp.Schema = schema.Schema{
		Description: "Queries the `" + d.name + "` audit log of the configured organization. Covers " +
			humanKind + ".\n\n" +
			"> **Note:** Per-entry field shapes vary per Pangolin build and are not statically modeled here. " +
			"Each entry is surfaced as a raw JSON string in `entries`; use `jsondecode()` to access individual fields. " +
			"`filter_attributes` is a map whose values are JSON-encoded arrays (one per dimension: `actors`, " +
			"`resources`, `locations`, `actions`, `protocols`, ...) - which keys are present depends on the log kind.\n\n" +
			"> Requires an active Pangolin Cloud subscription (or enterprise license on self-host).",
		Attributes: map[string]schema.Attribute{
			// Inputs
			"time_start": schema.StringAttribute{
				Description: "Start of the time range as an ISO 8601 / RFC 3339 timestamp. Defaults to 7 days ago when omitted.",
				Optional:    true,
			},
			"time_end": schema.StringAttribute{
				Description: "End of the time range as an ISO 8601 / RFC 3339 timestamp. Defaults to the current time when omitted.",
				Optional:    true,
			},
			"action": schema.StringAttribute{
				Description: "Filter by action value (semantics depend on the log kind).",
				Optional:    true,
			},
			"actor": schema.StringAttribute{
				Description: "Filter by actor identifier (user, API key, or anonymous).",
				Optional:    true,
			},
			"resource_id": schema.StringAttribute{
				Description: "Filter by the numeric ID of the resource involved in the event.",
				Optional:    true,
			},
			"limit": schema.Int64Attribute{
				Description: "Maximum number of entries to return. The API enforces its own ceiling.",
				Optional:    true,
			},
			"offset": schema.Int64Attribute{
				Description: "Skip the first N entries in the result set. Use with `limit` to paginate.",
				Optional:    true,
			},
			// Outputs
			"total": schema.Int64Attribute{
				Description: "Total number of entries matching the filter, ignoring pagination.",
				Computed:    true,
			},
			"entries": schema.ListAttribute{
				Description: "Matching entries, each as a JSON-encoded string. Use `jsondecode()` per entry to access fields.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"filter_attributes": schema.MapAttribute{
				Description: "Distinct values seen in the result set for each dimension. Keys vary per log kind; each value is a JSON-encoded array (use `jsondecode()`).",
				Computed:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (d *AuditLogsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *AuditLogsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg AuditLogsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	q := client.AuditLogQuery{
		TimeStart:  cfg.TimeStart.ValueString(),
		TimeEnd:    cfg.TimeEnd.ValueString(),
		Action:     cfg.Action.ValueString(),
		Actor:      cfg.Actor.ValueString(),
		ResourceID: cfg.ResourceID.ValueString(),
	}
	if !cfg.Limit.IsNull() && !cfg.Limit.IsUnknown() {
		q.Limit = strconv.FormatInt(cfg.Limit.ValueInt64(), 10)
	}
	if !cfg.Offset.IsNull() && !cfg.Offset.IsUnknown() {
		q.Offset = strconv.FormatInt(cfg.Offset.ValueInt64(), 10)
	}

	res, err := d.client.ListAuditLogs(ctx, d.client.OrgID, d.kind, q)
	if err != nil {
		resp.Diagnostics.AddError("Failed to query "+d.name+" audit logs", err.Error())
		return
	}

	cfg.Total = types.Int64Value(int64(res.Pagination.Total))
	cfg.Entries = make([]types.String, 0, len(res.Log))
	for _, raw := range res.Log {
		cfg.Entries = append(cfg.Entries, types.StringValue(string(raw)))
	}
	cfg.FilterAttributes = make(map[string]string, len(res.FilterAttributes))
	for k, raw := range res.FilterAttributes {
		if len(raw) == 0 {
			cfg.FilterAttributes[k] = "null"
			continue
		}
		cfg.FilterAttributes[k] = string(raw)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
