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

var _ datasource.DataSource = &RedirectsDataSource{}

// RedirectsDataSource defines the data source implementation.
type RedirectsDataSource struct {
	client *client.Client
}

// RedirectItemModel describes a single redirect in the data source.
type RedirectItemModel struct {
	ID                 types.String `tfsdk:"id"`
	NiceID             types.String `tfsdk:"nice_id"`
	Name               types.String `tfsdk:"name"`
	ResourceID         types.Int64  `tfsdk:"resource_id"`
	DomainID           types.String `tfsdk:"domain_id"`
	Subdomain          types.String `tfsdk:"subdomain"`
	DestinationHost    types.String `tfsdk:"destination_host"`
	PathMatchType      types.String `tfsdk:"path_match_type"`
	MatchPath          types.String `tfsdk:"match_path"`
	RewritePath        types.String `tfsdk:"rewrite_path"`
	RewritePathType    types.String `tfsdk:"rewrite_path_type"`
	Priority           types.Int64  `tfsdk:"priority"`
	Permanent          types.Bool   `tfsdk:"permanent"`
	SSL                types.Bool   `tfsdk:"ssl"`
	Enabled            types.Bool   `tfsdk:"enabled"`
	ResourceName       types.String `tfsdk:"resource_name"`
	ResourceNiceID     types.String `tfsdk:"resource_nice_id"`
	ResourceFullDomain types.String `tfsdk:"resource_full_domain"`
	BaseDomain         types.String `tfsdk:"base_domain"`
}

// RedirectsDataSourceModel describes the data source data model.
type RedirectsDataSourceModel struct {
	ID         types.String        `tfsdk:"id"`
	ResourceID types.Int64         `tfsdk:"resource_id"`
	DomainID   types.String        `tfsdk:"domain_id"`
	Enabled    types.Bool          `tfsdk:"enabled"`
	Redirects  []RedirectItemModel `tfsdk:"redirects"`
}

// NewRedirectsDataSource returns a new data source factory.
func NewRedirectsDataSource() datasource.DataSource {
	return &RedirectsDataSource{}
}

func (d *RedirectsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_redirects"
}

func (d *RedirectsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves the list of redirects for the organization, optionally filtered. " +
			"All filters that are set must match.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier of the data source.",
				Computed:    true,
			},
			"resource_id": schema.Int64Attribute{
				Description: "Only return redirects attached to this HTTP resource ID.",
				Optional:    true,
			},
			"domain_id": schema.StringAttribute{
				Description: "Only return redirects attached to this domain ID.",
				Optional:    true,
			},
			"enabled": schema.BoolAttribute{
				Description: "Only return redirects whose enabled state matches.",
				Optional:    true,
			},
			"redirects": schema.ListNestedAttribute{
				Description: "List of redirects.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                   schema.StringAttribute{Description: "The numeric ID of the redirect.", Computed: true},
						"nice_id":              schema.StringAttribute{Description: "The human-readable ID.", Computed: true},
						"name":                 schema.StringAttribute{Description: "The name of the redirect.", Computed: true},
						"resource_id":          schema.Int64Attribute{Description: "The ID of the HTTP resource this redirect is attached to. Null when attached to a domain.", Computed: true},
						"domain_id":            schema.StringAttribute{Description: "The ID of the domain this redirect is attached to. Null when attached to a resource.", Computed: true},
						"subdomain":            schema.StringAttribute{Description: "The subdomain to redirect, combined with `domain_id`. Null when unset.", Computed: true},
						"destination_host":     schema.StringAttribute{Description: "The redirect target as `scheme://host[:port]`, with no path, query or fragment.", Computed: true},
						"path_match_type":      schema.StringAttribute{Description: "How `match_path` is matched: `exact`, `prefix` or `regex`.", Computed: true},
						"match_path":           schema.StringAttribute{Description: "The request path to match. Null when unset.", Computed: true},
						"rewrite_path":         schema.StringAttribute{Description: "The replacement path appended to `destination_host`. Null when unset.", Computed: true},
						"rewrite_path_type":    schema.StringAttribute{Description: "How the path is rewritten: `exact`, `prefix`, `regex` or `stripPrefix`. Null when unset.", Computed: true},
						"priority":             schema.Int64Attribute{Description: "The match priority, from 1 to 1000. Reported as 100 when the server returns none.", Computed: true},
						"permanent":            schema.BoolAttribute{Description: "Whether the redirect is permanent: `true` answers with HTTP 308, `false` with HTTP 307.", Computed: true},
						"ssl":                  schema.BoolAttribute{Description: "Whether TLS is enabled for a domain-attached redirect.", Computed: true},
						"enabled":              schema.BoolAttribute{Description: "Whether the redirect is enabled.", Computed: true},
						"resource_name":        schema.StringAttribute{Description: "The name of the attached resource. Null when attached to a domain.", Computed: true},
						"resource_nice_id":     schema.StringAttribute{Description: "The nice ID of the attached resource. Null when attached to a domain.", Computed: true},
						"resource_full_domain": schema.StringAttribute{Description: "The full domain of the attached resource. Null when attached to a domain.", Computed: true},
						"base_domain":          schema.StringAttribute{Description: "The base domain of the attached domain. Null when attached to a resource.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *RedirectsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

// buildRedirectItems filters redirects (AND of every set filter) and maps them
// to their data source representation. Null or unknown filters are ignored.
func buildRedirectItems(redirects []client.Redirect, resourceID types.Int64, domainID types.String, enabled types.Bool) []RedirectItemModel {
	items := []RedirectItemModel{}
	for _, r := range redirects {
		if !resourceID.IsNull() && !resourceID.IsUnknown() &&
			(r.ResourceID == nil || int64(*r.ResourceID) != resourceID.ValueInt64()) {
			continue
		}
		if !domainID.IsNull() && !domainID.IsUnknown() &&
			(r.DomainID == nil || *r.DomainID != domainID.ValueString()) {
			continue
		}
		if !enabled.IsNull() && !enabled.IsUnknown() && r.Enabled != enabled.ValueBool() {
			continue
		}

		items = append(items, RedirectItemModel{
			ID:                 types.StringValue(strconv.Itoa(r.RedirectID)),
			NiceID:             types.StringValue(r.NiceID),
			Name:               types.StringValue(r.Name),
			ResourceID:         tfconv.Int64FromIntPtr(r.ResourceID),
			DomainID:           tfconv.StringFromPtr(r.DomainID),
			Subdomain:          tfconv.StringFromPtr(r.Subdomain),
			DestinationHost:    types.StringValue(r.DestinationHost),
			PathMatchType:      types.StringValue(r.PathMatchType),
			MatchPath:          tfconv.StringFromPtr(r.MatchPath),
			RewritePath:        tfconv.StringFromPtr(r.RewritePath),
			RewritePathType:    tfconv.StringFromPtr(r.RewritePathType),
			Priority:           types.Int64Value(int64(r.PriorityOr(client.DefaultRedirectPriority))),
			Permanent:          types.BoolValue(r.Permanent),
			SSL:                types.BoolValue(r.SSL),
			Enabled:            types.BoolValue(r.Enabled),
			ResourceName:       tfconv.StringFromPtr(r.ResourceName),
			ResourceNiceID:     tfconv.StringFromPtr(r.ResourceNiceID),
			ResourceFullDomain: tfconv.StringFromPtr(r.ResourceFullDomain),
			BaseDomain:         tfconv.StringFromPtr(r.BaseDomain),
		})
	}
	return items
}

func (d *RedirectsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state RedirectsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	redirects, err := d.client.ListRedirects(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list redirects", err.Error())
		return
	}

	state.ID = types.StringValue("redirects")
	state.Redirects = buildRedirectItems(redirects, state.ResourceID, state.DomainID, state.Enabled)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
