package datasources

import (
	"context"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
)

var _ datasource.DataSource = &RequestLogsDataSource{}

// RequestLogsDataSource exposes the request audit log of an organization.
type RequestLogsDataSource struct {
	client *client.Client
}

// RequestLogEntryModel describes one audit log entry in the data source.
//
// Field types match what the Pangolin API actually returns (observed
// against the enterprise self-host): timestamp / reason / resource_id
// are integers, action is a bool (true = allowed, false = denied), and
// nullable string fields (actor, actor_type, actor_id, user_agent) come
// back as Null when the server omits them.
//
// metadata / headers / query land on the wire as JSON objects of
// unknown shape; they are surfaced as JSON-string attributes so users
// can `jsondecode()` what they need. raw_json carries the full server
// payload as a fallback for fields not modeled here.
type RequestLogEntryModel struct {
	ID                 types.Int64  `tfsdk:"id"`
	Timestamp          types.Int64  `tfsdk:"timestamp"`
	OrgID              types.String `tfsdk:"org_id"`
	Action             types.Bool   `tfsdk:"action"`
	Reason             types.Int64  `tfsdk:"reason"`
	ActorType          types.String `tfsdk:"actor_type"`
	Actor              types.String `tfsdk:"actor"`
	ActorID            types.String `tfsdk:"actor_id"`
	ResourceID         types.Int64  `tfsdk:"resource_id"`
	SiteResourceID     types.Int64  `tfsdk:"site_resource_id"`
	IP                 types.String `tfsdk:"ip"`
	Location           types.String `tfsdk:"location"`
	UserAgent          types.String `tfsdk:"user_agent"`
	Metadata           types.String `tfsdk:"metadata"`
	Headers            types.String `tfsdk:"headers"`
	Query              types.String `tfsdk:"query"`
	OriginalRequestURL types.String `tfsdk:"original_request_url"`
	Scheme             types.String `tfsdk:"scheme"`
	Host               types.String `tfsdk:"host"`
	Path               types.String `tfsdk:"path"`
	Method             types.String `tfsdk:"method"`
	TLS                types.Bool   `tfsdk:"tls"`
	ResourceName       types.String `tfsdk:"resource_name"`
	ResourceNiceID     types.String `tfsdk:"resource_nice_id"`
	RawJSON            types.String `tfsdk:"raw_json"`
}

// RequestLogsFilterAttributesModel mirrors the API's filterAttributes block:
// distinct values observed in the result set for each filterable dimension.
//
// Resources are returned by the API as {id, name} objects rather than
// bare IDs, so they are surfaced as a nested attribute here.
type RequestLogsFilterAttributesModel struct {
	Actors    []types.String               `tfsdk:"actors"`
	Resources []RequestLogResourceRefModel `tfsdk:"resources"`
	Locations []types.String               `tfsdk:"locations"`
	Hosts     []types.String               `tfsdk:"hosts"`
	Paths     []types.String               `tfsdk:"paths"`
}

// RequestLogResourceRefModel is the {id, name} pair exposed under
// filter_attributes.resources.
type RequestLogResourceRefModel struct {
	ID   types.Int64  `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
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
			"> **Note:** Field types match what the Pangolin API actually returns. `timestamp`, " +
			"`reason`, `resource_id` and the IDs are integers; `action` is a boolean " +
			"(`true` = request was allowed, `false` = denied). `metadata`, `headers` and `query` " +
			"come back as JSON strings; use `jsondecode()` to access their fields. `raw_json` " +
			"on each entry preserves the full server payload as a fallback.",
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
				Description: "Filter by request action. The API accepts either a boolean string (`true` / `false`) or a textual reason variant.",
				Optional:    true,
			},
			"method": schema.StringAttribute{
				Description: "Filter by HTTP method. One of `GET`, `POST`, `PUT`, `DELETE`, `PATCH`.",
				Optional:    true,
			},
			"reason": schema.StringAttribute{
				Description: "Filter by the access decision reason code emitted by Pangolin.",
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
			"entries": schema.ListNestedAttribute{
				Description: "The matching log entries.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                   schema.Int64Attribute{Description: "Unique entry ID assigned by Pangolin.", Computed: true},
						"timestamp":            schema.Int64Attribute{Description: "Unix epoch seconds when the request was processed.", Computed: true},
						"org_id":               schema.StringAttribute{Description: "Organization that received the request.", Computed: true},
						"action":               schema.BoolAttribute{Description: "`true` if the request was allowed, `false` if denied.", Computed: true},
						"reason":               schema.Int64Attribute{Description: "Numeric reason code for the access decision.", Computed: true},
						"actor_type":           schema.StringAttribute{Description: "Kind of actor (user, api_key, anonymous, …). Null when the request was unauthenticated.", Computed: true},
						"actor":                schema.StringAttribute{Description: "Human-readable identifier of the actor (e.g. email). Null when unauthenticated.", Computed: true},
						"actor_id":             schema.StringAttribute{Description: "Pangolin's internal identifier for the actor. Null when unauthenticated.", Computed: true},
						"resource_id":          schema.Int64Attribute{Description: "Numeric ID of the resource that received the request.", Computed: true},
						"site_resource_id":     schema.Int64Attribute{Description: "Numeric ID of the site-scoped resource that handled the request, if any.", Computed: true},
						"ip":                   schema.StringAttribute{Description: "Source IP of the requester.", Computed: true},
						"location":             schema.StringAttribute{Description: "Geolocation code (e.g. `FR`) resolved from the source IP.", Computed: true},
						"user_agent":           schema.StringAttribute{Description: "`User-Agent` header of the request. Null when not sent.", Computed: true},
						"metadata":             schema.StringAttribute{Description: "Free-form metadata, serialized as JSON. Null when empty.", Computed: true},
						"headers":              schema.StringAttribute{Description: "Captured request headers, serialized as JSON. Null when not captured.", Computed: true},
						"query":                schema.StringAttribute{Description: "Captured query string parameters, serialized as JSON. Null when not captured.", Computed: true},
						"original_request_url": schema.StringAttribute{Description: "Original URL of the incoming request.", Computed: true},
						"scheme":               schema.StringAttribute{Description: "URL scheme (often empty).", Computed: true},
						"host":                 schema.StringAttribute{Description: "Host header of the request.", Computed: true},
						"path":                 schema.StringAttribute{Description: "URL path of the request.", Computed: true},
						"method":               schema.StringAttribute{Description: "HTTP method.", Computed: true},
						"tls":                  schema.BoolAttribute{Description: "`true` when the request was received over TLS.", Computed: true},
						"resource_name":        schema.StringAttribute{Description: "Display name of the Pangolin resource that received the request.", Computed: true},
						"resource_nice_id":     schema.StringAttribute{Description: "Human-readable nice ID of the Pangolin resource.", Computed: true},
						"raw_json":             schema.StringAttribute{Description: "Full server payload for this entry, serialized as JSON. Use `jsondecode()` to access fields not modeled above.", Computed: true},
					},
				},
			},
			"filter_attributes": schema.SingleNestedAttribute{
				Description: "Distinct values observed in the result set for each filterable dimension. Useful to populate UIs with allowed values to refine the query.",
				Computed:    true,
				Attributes: map[string]schema.Attribute{
					"actors": schema.ListAttribute{ElementType: types.StringType, Computed: true, Description: "Distinct actors seen."},
					"resources": schema.ListNestedAttribute{
						Description: "Distinct resources seen, as `{id, name}` pairs.",
						Computed:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"id":   schema.Int64Attribute{Description: "Resource ID.", Computed: true},
								"name": schema.StringAttribute{Description: "Resource display name.", Computed: true},
							},
						},
					},
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
		entry := RequestLogEntryModel{
			ID:                 types.Int64Value(e.ID),
			Timestamp:          types.Int64Value(e.Timestamp),
			OrgID:              types.StringValue(e.OrgID),
			Action:             types.BoolValue(e.Action),
			Reason:             types.Int64Value(e.Reason),
			ActorType:          tfconv.StringFromPtr(e.ActorType),
			Actor:              tfconv.StringFromPtr(e.Actor),
			ActorID:            tfconv.StringFromPtr(e.ActorID),
			ResourceID:         types.Int64Value(e.ResourceID),
			SiteResourceID:     tfconv.Int64FromInt64Ptr(e.SiteResourceID),
			IP:                 types.StringValue(e.IP),
			Location:           types.StringValue(e.Location),
			UserAgent:          tfconv.StringFromPtr(e.UserAgent),
			Metadata:           rawJSONToString(e.Metadata),
			Headers:            rawJSONToString(e.Headers),
			Query:              rawJSONToString(e.Query),
			OriginalRequestURL: types.StringValue(e.OriginalRequestURL),
			Scheme:             types.StringValue(e.Scheme),
			Host:               types.StringValue(e.Host),
			Path:               types.StringValue(e.Path),
			Method:             types.StringValue(e.Method),
			TLS:                types.BoolValue(e.TLS),
			ResourceName:       types.StringValue(e.ResourceName),
			ResourceNiceID:     types.StringValue(e.ResourceNiceID),
			RawJSON:            types.StringValue(string(e.Raw)),
		}
		cfg.Entries = append(cfg.Entries, entry)
	}
	resources := make([]RequestLogResourceRefModel, len(res.FilterAttributes.Resources))
	for i, r := range res.FilterAttributes.Resources {
		resources[i] = RequestLogResourceRefModel{
			ID:   types.Int64Value(r.ID),
			Name: types.StringValue(r.Name),
		}
	}
	cfg.FilterAttributes = &RequestLogsFilterAttributesModel{
		Actors:    toStringSlice(res.FilterAttributes.Actors),
		Resources: resources,
		Locations: toStringSlice(res.FilterAttributes.Locations),
		Hosts:     toStringSlice(res.FilterAttributes.Hosts),
		Paths:     toStringSlice(res.FilterAttributes.Paths),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// rawJSONToString surfaces a nullable JSON sub-object as a string for
// consumers to jsondecode(). An empty / null sub-object becomes a
// types.StringNull() so plans show the absent state explicitly.
func rawJSONToString(raw []byte) types.String {
	if len(raw) == 0 || string(raw) == "null" {
		return types.StringNull()
	}
	return types.StringValue(string(raw))
}

func toStringSlice(in []string) []types.String {
	out := make([]types.String, len(in))
	for i, s := range in {
		out[i] = types.StringValue(s)
	}
	return out
}
