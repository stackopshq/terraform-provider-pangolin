package datasources

import (
	"context"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &RequestLogsDataSource{}

// RequestLogsDataSource exposes the request audit log of an organization.
type RequestLogsDataSource struct {
	client *client.Client
}

// RequestLogEntryModel describes one audit log entry in the data source.
// raw_json carries the full untruncated server payload so consumers can
// reach fields we did not model (the Pangolin OpenAPI spec does not
// publish a response schema for these entries).
type RequestLogEntryModel struct {
	Timestamp  types.String `tfsdk:"timestamp"`
	Actor      types.String `tfsdk:"actor"`
	Method     types.String `tfsdk:"method"`
	Reason     types.String `tfsdk:"reason"`
	ResourceID types.String `tfsdk:"resource_id"`
	Location   types.String `tfsdk:"location"`
	Host       types.String `tfsdk:"host"`
	Path       types.String `tfsdk:"path"`
	RawJSON    types.String `tfsdk:"raw_json"`
}

// RequestLogsFilterAttributesModel mirrors the API's filterAttributes block:
// distinct values observed in the result set for each filterable dimension.
type RequestLogsFilterAttributesModel struct {
	Actors    []types.String `tfsdk:"actors"`
	Resources []types.String `tfsdk:"resources"`
	Locations []types.String `tfsdk:"locations"`
	Hosts     []types.String `tfsdk:"hosts"`
	Paths     []types.String `tfsdk:"paths"`
}

// RequestLogsDataSourceModel describes the data source data model.
type RequestLogsDataSourceModel struct {
	// Inputs (Optional filters)
	TimeStart  types.String `tfsdk:"time_start"`
	TimeEnd    types.String `tfsdk:"time_end"`
	Action     types.String `tfsdk:"action"`
	Method     types.String `tfsdk:"method"`
	Reason     types.String `tfsdk:"reason"`
	ResourceID types.String `tfsdk:"resource_id"`
	Actor      types.String `tfsdk:"actor"`
	Location   types.String `tfsdk:"location"`
	Host       types.String `tfsdk:"host"`
	Path       types.String `tfsdk:"path"`
	Limit      types.Int64  `tfsdk:"limit"`
	Offset     types.Int64  `tfsdk:"offset"`

	// Outputs
	Entries          []RequestLogEntryModel            `tfsdk:"entries"`
	Total            types.Int64                       `tfsdk:"total"`
	FilterAttributes *RequestLogsFilterAttributesModel `tfsdk:"filter_attributes"`
}

// NewRequestLogsDataSource returns a new data source factory.
func NewRequestLogsDataSource() datasource.DataSource {
	return &RequestLogsDataSource{}
}

func (d *RequestLogsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_request_logs"
}

func (d *RequestLogsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Queries the request audit log of the configured organization. " +
			"This is the same log the Pangolin web UI displays under \"Logs > Requests\". " +
			"All filter attributes are optional; the API defaults to the last 7 days when " +
			"`time_start` is omitted.\n\n" +
			"> **Note:** Each entry exposes the fields that map to the documented query " +
			"dimensions (`timestamp`, `actor`, `method`, `reason`, `resource_id`, " +
			"`location`, `host`, `path`). The full server payload for the entry is also " +
			"available as `raw_json` for consumers that need fields not modeled here.",
		Attributes: map[string]schema.Attribute{
			// Inputs
			"time_start": schema.StringAttribute{
				Description: "Start of the time range as an ISO 8601 / RFC 3339 timestamp (e.g. `2026-05-01T00:00:00Z`). Defaults to 7 days ago when omitted.",
				Optional:    true,
			},
			"time_end": schema.StringAttribute{
				Description: "End of the time range as an ISO 8601 / RFC 3339 timestamp. Defaults to the current time when omitted.",
				Optional:    true,
			},
			"action": schema.StringAttribute{
				Description: "Filter by request action (typically `accept` or `deny`, plus reason variants).",
				Optional:    true,
			},
			"method": schema.StringAttribute{
				Description: "Filter by HTTP method. One of `GET`, `POST`, `PUT`, `DELETE`, `PATCH`.",
				Optional:    true,
			},
			"reason": schema.StringAttribute{
				Description: "Filter by the access decision reason string emitted by Pangolin.",
				Optional:    true,
			},
			"resource_id": schema.StringAttribute{
				Description: "Filter by the numeric ID of the resource that received the request.",
				Optional:    true,
			},
			"actor": schema.StringAttribute{
				Description: "Filter by actor identifier (user, API key, or anonymous).",
				Optional:    true,
			},
			"location": schema.StringAttribute{
				Description: "Filter by client geolocation string as resolved by Pangolin.",
				Optional:    true,
			},
			"host": schema.StringAttribute{
				Description: "Filter by the `Host` header of the request.",
				Optional:    true,
			},
			"path": schema.StringAttribute{
				Description: "Filter by URL path of the request.",
				Optional:    true,
			},
			"limit": schema.Int64Attribute{
				Description: "Maximum number of entries to return. The API enforces its own ceiling (1000 in observed responses).",
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
			"entries": schema.ListNestedAttribute{
				Description: "The matching log entries.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"timestamp": schema.StringAttribute{
							Description: "ISO 8601 / RFC 3339 timestamp of the request.",
							Computed:    true,
						},
						"actor": schema.StringAttribute{
							Description: "The actor that made the request.",
							Computed:    true,
						},
						"method": schema.StringAttribute{
							Description: "HTTP method.",
							Computed:    true,
						},
						"reason": schema.StringAttribute{
							Description: "Access decision reason.",
							Computed:    true,
						},
						"resource_id": schema.StringAttribute{
							Description: "Resource ID that received the request.",
							Computed:    true,
						},
						"location": schema.StringAttribute{
							Description: "Client geolocation.",
							Computed:    true,
						},
						"host": schema.StringAttribute{
							Description: "`Host` header of the request.",
							Computed:    true,
						},
						"path": schema.StringAttribute{
							Description: "URL path of the request.",
							Computed:    true,
						},
						"raw_json": schema.StringAttribute{
							Description: "The full server payload for this entry, serialized as JSON. Use `jsondecode()` to access fields not modeled above.",
							Computed:    true,
						},
					},
				},
			},
			"filter_attributes": schema.SingleNestedAttribute{
				Description: "Distinct values observed in the result set for each filterable dimension. Useful to populate UIs with allowed values to refine the query.",
				Computed:    true,
				Attributes: map[string]schema.Attribute{
					"actors":    schema.ListAttribute{ElementType: types.StringType, Computed: true, Description: "Distinct actors seen."},
					"resources": schema.ListAttribute{ElementType: types.StringType, Computed: true, Description: "Distinct resource IDs seen."},
					"locations": schema.ListAttribute{ElementType: types.StringType, Computed: true, Description: "Distinct geolocations seen."},
					"hosts":     schema.ListAttribute{ElementType: types.StringType, Computed: true, Description: "Distinct hosts seen."},
					"paths":     schema.ListAttribute{ElementType: types.StringType, Computed: true, Description: "Distinct paths seen."},
				},
			},
		},
	}
}

func (d *RequestLogsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *RequestLogsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg RequestLogsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	q := client.RequestLogQuery{
		TimeStart:  cfg.TimeStart.ValueString(),
		TimeEnd:    cfg.TimeEnd.ValueString(),
		Action:     cfg.Action.ValueString(),
		Method:     cfg.Method.ValueString(),
		Reason:     cfg.Reason.ValueString(),
		ResourceID: cfg.ResourceID.ValueString(),
		Actor:      cfg.Actor.ValueString(),
		Location:   cfg.Location.ValueString(),
		Host:       cfg.Host.ValueString(),
		Path:       cfg.Path.ValueString(),
	}
	if !cfg.Limit.IsNull() && !cfg.Limit.IsUnknown() {
		q.Limit = strconv.FormatInt(cfg.Limit.ValueInt64(), 10)
	}
	if !cfg.Offset.IsNull() && !cfg.Offset.IsUnknown() {
		q.Offset = strconv.FormatInt(cfg.Offset.ValueInt64(), 10)
	}

	res, err := d.client.ListRequestLogs(ctx, d.client.OrgID, q)
	if err != nil {
		resp.Diagnostics.AddError("Failed to query request logs", err.Error())
		return
	}

	cfg.Total = types.Int64Value(int64(res.Pagination.Total))
	cfg.Entries = make([]RequestLogEntryModel, 0, len(res.Log))
	for _, e := range res.Log {
		cfg.Entries = append(cfg.Entries, RequestLogEntryModel{
			Timestamp:  types.StringValue(e.Timestamp),
			Actor:      types.StringValue(e.Actor),
			Method:     types.StringValue(e.Method),
			Reason:     types.StringValue(e.Reason),
			ResourceID: types.StringValue(e.ResourceID),
			Location:   types.StringValue(e.Location),
			Host:       types.StringValue(e.Host),
			Path:       types.StringValue(e.Path),
			RawJSON:    types.StringValue(string(e.Raw)),
		})
	}
	cfg.FilterAttributes = &RequestLogsFilterAttributesModel{
		Actors:    toStringSlice(res.FilterAttributes.Actors),
		Resources: toStringSlice(res.FilterAttributes.Resources),
		Locations: toStringSlice(res.FilterAttributes.Locations),
		Hosts:     toStringSlice(res.FilterAttributes.Hosts),
		Paths:     toStringSlice(res.FilterAttributes.Paths),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

func toStringSlice(in []string) []types.String {
	out := make([]types.String, len(in))
	for i, s := range in {
		out[i] = types.StringValue(s)
	}
	return out
}
