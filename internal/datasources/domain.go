package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
)

var _ datasource.DataSource = &DomainDataSource{}

// DomainDataSource exposes a single Pangolin domain by ID — the
// per-id endpoint that GetDomain hits directly, no list-and-filter.
type DomainDataSource struct {
	client *client.Client
}

// DomainDataSourceModel describes the data source data model.
type DomainDataSourceModel struct {
	ID                 types.String `tfsdk:"id"`
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

// NewDomainDataSource returns a new data source factory.
func NewDomainDataSource() datasource.DataSource {
	return &DomainDataSource{}
}

func (d *DomainDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain"
}

func (d *DomainDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up a single Pangolin domain by ID. Use this when you already have the `domain_id` and want " +
			"a one-shot read; for browsing every domain in the org, use `pangolin_domains`.",
		Attributes: map[string]schema.Attribute{
			"id":                   schema.StringAttribute{Description: "The domain ID to look up.", Required: true},
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
	}
}

func (d *DomainDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *DomainDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg DomainDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	domain, err := d.client.GetDomain(ctx, d.client.OrgID, cfg.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read domain", err.Error())
		return
	}

	cfg.BaseDomain = types.StringValue(domain.BaseDomain)
	cfg.Verified = types.BoolValue(domain.Verified)
	cfg.Type = types.StringValue(domain.Type)
	cfg.Failed = types.BoolValue(domain.Failed)
	cfg.Tries = types.Int64Value(int64(domain.Tries))
	cfg.ConfigManaged = types.BoolValue(domain.ConfigManaged)
	cfg.CertResolver = tfconv.StringFromPtr(domain.CertResolver)
	cfg.CustomCertResolver = tfconv.StringFromPtr(domain.CustomCertResolver)
	cfg.PreferWildcardCert = types.BoolValue(domain.PreferWildcardCert)
	cfg.ErrorMessage = tfconv.StringFromPtr(domain.ErrorMessage)

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
