package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &SitesDataSource{}

// SitesDataSource defines the data source implementation.
type SitesDataSource struct {
	client *client.Client
}

// SiteItemModel describes a single site in the data source.
type SiteItemModel struct {
	ID      types.Int64  `tfsdk:"id"`
	NiceID  types.String `tfsdk:"nice_id"`
	Name    types.String `tfsdk:"name"`
	Type    types.String `tfsdk:"type"`
	Online  types.Bool   `tfsdk:"online"`
	Address types.String `tfsdk:"address"`
}

// SitesDataSourceModel describes the data source data model.
type SitesDataSourceModel struct {
	Sites []SiteItemModel `tfsdk:"sites"`
}

// NewSitesDataSource returns a new data source factory.
func NewSitesDataSource() datasource.DataSource {
	return &SitesDataSource{}
}

func (d *SitesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sites"
}

func (d *SitesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves the list of sites for the organization.",
		Attributes: map[string]schema.Attribute{
			"sites": schema.ListNestedAttribute{
				Description: "List of sites.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.Int64Attribute{
							Description: "The numeric site ID.",
							Computed:    true,
						},
						"nice_id": schema.StringAttribute{
							Description: "The human-readable site ID.",
							Computed:    true,
						},
						"name": schema.StringAttribute{
							Description: "The site name.",
							Computed:    true,
						},
						"type": schema.StringAttribute{
							Description: "The site type.",
							Computed:    true,
						},
						"online": schema.BoolAttribute{
							Description: "Whether the site is online.",
							Computed:    true,
						},
						"address": schema.StringAttribute{
							Description: "The WireGuard address.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *SitesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *SitesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	sites, err := d.client.ListSites()
	if err != nil {
		resp.Diagnostics.AddError("Failed to list sites", err.Error())
		return
	}

	state := SitesDataSourceModel{Sites: []SiteItemModel{}}
	for _, site := range sites {
		state.Sites = append(state.Sites, SiteItemModel{
			ID:      types.Int64Value(int64(site.SiteID)),
			NiceID:  types.StringValue(site.NiceID),
			Name:    types.StringValue(site.Name),
			Type:    types.StringValue(site.Type),
			Online:  types.BoolValue(site.Online),
			Address: types.StringValue(site.Address),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
