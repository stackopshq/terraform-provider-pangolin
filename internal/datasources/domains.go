package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &DomainsDataSource{}

// DomainsDataSource defines the data source implementation.
type DomainsDataSource struct {
	client *client.Client
}

// DomainModel describes a single domain in the data source.
type DomainModel struct {
	DomainID   types.String `tfsdk:"domain_id"`
	BaseDomain types.String `tfsdk:"base_domain"`
	Verified   types.Bool   `tfsdk:"verified"`
	Type       types.String `tfsdk:"type"`
}

// DomainsDataSourceModel describes the data source data model.
type DomainsDataSourceModel struct {
	Domains []DomainModel `tfsdk:"domains"`
}

// NewDomainsDataSource returns a new data source factory.
func NewDomainsDataSource() datasource.DataSource {
	return &DomainsDataSource{}
}

func (d *DomainsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domains"
}

func (d *DomainsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves the list of domains for the organization.",
		Attributes: map[string]schema.Attribute{
			"domains": schema.ListNestedAttribute{
				Description: "List of domains.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"domain_id": schema.StringAttribute{
							Description: "The domain ID.",
							Computed:    true,
						},
						"base_domain": schema.StringAttribute{
							Description: "The base domain name.",
							Computed:    true,
						},
						"verified": schema.BoolAttribute{
							Description: "Whether the domain is verified.",
							Computed:    true,
						},
						"type": schema.StringAttribute{
							Description: "The domain type (ns, cname, wildcard).",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *DomainsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *DomainsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	domains, err := d.client.ListDomains()
	if err != nil {
		resp.Diagnostics.AddError("Failed to list domains", err.Error())
		return
	}

	state := DomainsDataSourceModel{Domains: []DomainModel{}}
	for _, domain := range domains {
		state.Domains = append(state.Domains, DomainModel{
			DomainID:   types.StringValue(domain.DomainID),
			BaseDomain: types.StringValue(domain.BaseDomain),
			Verified:   types.BoolValue(domain.Verified),
			Type:       types.StringValue(domain.Type),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
