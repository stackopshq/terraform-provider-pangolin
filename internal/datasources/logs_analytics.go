package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &LogsAnalyticsDataSource{}

// LogsAnalyticsDataSource exposes the request analytics rollup of an
// organization (per-country, per-day, totals).
type LogsAnalyticsDataSource struct {
	client *client.Client
}

// LogsAnalyticsCountryModel is one row of the per-country breakdown.
type LogsAnalyticsCountryModel struct {
	Code  types.String `tfsdk:"code"`
	Count types.Int64  `tfsdk:"count"`
}

// LogsAnalyticsDayModel is one row of the per-day breakdown. Counts
// are normalized to int64 by the client (upstream emits them as
// strings on the wire).
type LogsAnalyticsDayModel struct {
	Day          types.String `tfsdk:"day"`
	AllowedCount types.Int64  `tfsdk:"allowed_count"`
	BlockedCount types.Int64  `tfsdk:"blocked_count"`
	TotalCount   types.Int64  `tfsdk:"total_count"`
}

// LogsAnalyticsDataSourceModel describes the data source data model.
type LogsAnalyticsDataSourceModel struct {
	// Inputs
	TimeStart  types.String `tfsdk:"time_start"`
	TimeEnd    types.String `tfsdk:"time_end"`
	ResourceID types.String `tfsdk:"resource_id"`

	// Outputs
	RequestsPerCountry []LogsAnalyticsCountryModel `tfsdk:"requests_per_country"`
	RequestsPerDay     []LogsAnalyticsDayModel     `tfsdk:"requests_per_day"`
	TotalBlocked       types.Int64                 `tfsdk:"total_blocked"`
	TotalRequests      types.Int64                 `tfsdk:"total_requests"`
}

// NewLogsAnalyticsDataSource returns a new data source factory.
func NewLogsAnalyticsDataSource() datasource.DataSource {
	return &LogsAnalyticsDataSource{}
}

func (d *LogsAnalyticsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_logs_analytics"
}

func (d *LogsAnalyticsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Returns the request analytics rollup of the configured organization - same view the Pangolin web UI " +
			"displays under \"Logs > Analytics\". Three breakdowns: by country, by day, and global totals.\n\n" +
			"> **Note:** Requires an active enterprise subscription on Pangolin Cloud. Always available on " +
			"self-hosted enterprise installs.",
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
			"resource_id": schema.StringAttribute{
				Description: "Narrow the analytics to a single resource (numeric ID, encoded as string for the API).",
				Optional:    true,
			},
			// Outputs
			"total_blocked": schema.Int64Attribute{
				Description: "Total number of blocked (denied) requests over the queried range.",
				Computed:    true,
			},
			"total_requests": schema.Int64Attribute{
				Description: "Total number of requests over the queried range (allowed + blocked).",
				Computed:    true,
			},
			"requests_per_country": schema.ListNestedAttribute{
				Description: "Request count broken down by ISO-3166 country code resolved from the client IP.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"code":  schema.StringAttribute{Description: "ISO-3166 alpha-2 country code (e.g. `FR`).", Computed: true},
						"count": schema.Int64Attribute{Description: "Number of requests originating from this country.", Computed: true},
					},
				},
			},
			"requests_per_day": schema.ListNestedAttribute{
				Description: "Request count broken down by day, with allowed / blocked / total triplets.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"day":           schema.StringAttribute{Description: "Day bucket (server-local time, e.g. `2026-05-18 00:00:00+00`).", Computed: true},
						"allowed_count": schema.Int64Attribute{Description: "Allowed requests on this day.", Computed: true},
						"blocked_count": schema.Int64Attribute{Description: "Blocked requests on this day.", Computed: true},
						"total_count":   schema.Int64Attribute{Description: "Total requests on this day.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *LogsAnalyticsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *LogsAnalyticsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg LogsAnalyticsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	q := client.LogsAnalyticsQuery{
		TimeStart:  cfg.TimeStart.ValueString(),
		TimeEnd:    cfg.TimeEnd.ValueString(),
		ResourceID: cfg.ResourceID.ValueString(),
	}
	res, err := d.client.GetLogsAnalytics(ctx, d.client.OrgID, q)
	if err != nil {
		resp.Diagnostics.AddError("Failed to query logs analytics", err.Error())
		return
	}

	cfg.TotalBlocked = types.Int64Value(res.TotalBlocked)
	cfg.TotalRequests = types.Int64Value(res.TotalRequests)

	cfg.RequestsPerCountry = make([]LogsAnalyticsCountryModel, len(res.RequestsPerCountry))
	for i, c := range res.RequestsPerCountry {
		cfg.RequestsPerCountry[i] = LogsAnalyticsCountryModel{
			Code:  types.StringValue(c.Code),
			Count: types.Int64Value(c.Count),
		}
	}

	cfg.RequestsPerDay = make([]LogsAnalyticsDayModel, len(res.RequestsPerDay))
	for i, day := range res.RequestsPerDay {
		cfg.RequestsPerDay[i] = LogsAnalyticsDayModel{
			Day:          types.StringValue(day.Day),
			AllowedCount: types.Int64Value(day.AllowedCount),
			BlockedCount: types.Int64Value(day.BlockedCount),
			TotalCount:   types.Int64Value(day.TotalCount),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
