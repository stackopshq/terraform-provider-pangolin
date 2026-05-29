package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
)

var _ datasource.DataSource = &DomainsDataSource{}

// DomainsDataSource defines the data source implementation.
type DomainsDataSource struct {
	client *client.Client
}

// DomainModel describes a single domain in the data source.
type DomainModel struct {
	DomainID           types.String `tfsdk:"domain_id"`
	BaseDomain         types.String `tfsdk:"base_domain"`
	Verified           types.Bool   `tfsdk:"verified"`
	Type               types.String `tfsdk:"type"`
	Failed             types.Bool   `tfsdk:"failed"`
	Tries              types.Int64  `tfsdk:"tries"`
	ConfigManaged      types.Bool   `tfsdk:"config_managed"`
	CertResolver       types.String `tfsdk:"cert_resolver"`
	CustomCertResolver types.String `tfsdk:"custom_cert_resolver"`
	PreferWildcardCert types.Bool   `tfsdk:"prefer_wildcard_cert"`
	ErrorMessage       types.String `tfsdk:"error_message"`
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
						"domain_id":            schema.StringAttribute{Description: "The domain ID.", Computed: true},
						"base_domain":          schema.StringAttribute{Description: "The base domain name.", Computed: true},
						"verified":             schema.BoolAttribute{Description: "Whether the domain has been verified.", Computed: true},
						"type":                 schema.StringAttribute{Description: "The domain type (`ns`, `cname`, `wildcard`).", Computed: true},
						"failed":               schema.BoolAttribute{Description: "Whether the last verification attempt failed.", Computed: true},
						"tries":                schema.Int64Attribute{Description: "Number of verification attempts performed by Pangolin.", Computed: true},
						"config_managed":       schema.BoolAttribute{Description: "Whether the domain was provisioned via static configuration (`true`) or via the API (`false`).", Computed: true},
						"cert_resolver":        schema.StringAttribute{Description: "Name of the cert resolver in use, or `null` when the default resolver applies.", Computed: true},
						"custom_cert_resolver": schema.StringAttribute{Description: "Custom cert resolver override, or `null` to use the default.", Computed: true},
						"prefer_wildcard_cert": schema.BoolAttribute{Description: "Whether to prefer issuing a wildcard certificate for this domain.", Computed: true},
						"error_message":        schema.StringAttribute{Description: "Last error reported during verification, or `null` when no error.", Computed: true},
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
	domains, err := d.client.ListDomains(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list domains", err.Error())
		return
	}

	state := DomainsDataSourceModel{Domains: []DomainModel{}}
	for _, domain := range domains {
		state.Domains = append(state.Domains, DomainModel{
			DomainID:           types.StringValue(domain.DomainID),
			BaseDomain:         types.StringValue(domain.BaseDomain),
			Verified:           types.BoolValue(domain.Verified),
			Type:               types.StringValue(domain.Type),
			Failed:             types.BoolValue(domain.Failed),
			Tries:              types.Int64Value(int64(domain.Tries)),
			ConfigManaged:      types.BoolValue(domain.ConfigManaged),
			CertResolver:       tfconv.StringFromPtr(domain.CertResolver),
			CustomCertResolver: tfconv.StringFromPtr(domain.CustomCertResolver),
			PreferWildcardCert: types.BoolValue(domain.PreferWildcardCert),
			ErrorMessage:       tfconv.StringFromPtr(domain.ErrorMessage),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
