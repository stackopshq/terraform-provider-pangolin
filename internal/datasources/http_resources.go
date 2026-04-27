package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &HTTPResourcesDataSource{}

// HTTPResourcesDataSource defines the data source implementation.
type HTTPResourcesDataSource struct {
	client *client.Client
}

// HTTPResourceItemModel describes a single HTTP resource in the data source.
type HTTPResourceItemModel struct {
	ID         types.Int64  `tfsdk:"id"`
	NiceID     types.String `tfsdk:"nice_id"`
	Name       types.String `tfsdk:"name"`
	Subdomain  types.String `tfsdk:"subdomain"`
	FullDomain types.String `tfsdk:"full_domain"`
	DomainID   types.String `tfsdk:"domain_id"`
}

// HTTPResourcesDataSourceModel describes the data source data model.
type HTTPResourcesDataSourceModel struct {
	Resources []HTTPResourceItemModel `tfsdk:"resources"`
}

// NewHTTPResourcesDataSource returns a new data source factory.
func NewHTTPResourcesDataSource() datasource.DataSource {
	return &HTTPResourcesDataSource{}
}

func (d *HTTPResourcesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resources"
}

func (d *HTTPResourcesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves the list of HTTP resources for the organization.",
		Attributes: map[string]schema.Attribute{
			"resources": schema.ListNestedAttribute{
				Description: "List of HTTP resources.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.Int64Attribute{
							Description: "The numeric resource ID.",
							Computed:    true,
						},
						"nice_id": schema.StringAttribute{
							Description: "The human-readable resource ID.",
							Computed:    true,
						},
						"name": schema.StringAttribute{
							Description: "The resource name.",
							Computed:    true,
						},
						"subdomain": schema.StringAttribute{
							Description: "The subdomain.",
							Computed:    true,
						},
						"full_domain": schema.StringAttribute{
							Description: "The full domain.",
							Computed:    true,
						},
						"domain_id": schema.StringAttribute{
							Description: "The domain ID.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *HTTPResourcesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *HTTPResourcesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	resources, err := d.client.ListResources(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list resources", err.Error())
		return
	}

	state := HTTPResourcesDataSourceModel{Resources: []HTTPResourceItemModel{}}
	for _, res := range resources {
		item := HTTPResourceItemModel{
			ID:         types.Int64Value(int64(res.ResourceID)),
			NiceID:     types.StringValue(res.NiceID),
			Name:       types.StringValue(res.Name),
			FullDomain: types.StringValue(res.FullDomain),
			DomainID:   types.StringValue(res.DomainID),
		}
		if res.Subdomain != "" {
			item.Subdomain = types.StringValue(res.Subdomain)
		} else {
			item.Subdomain = types.StringNull()
		}
		state.Resources = append(state.Resources, item)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
