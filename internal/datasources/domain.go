package datasources

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
)

var (
	_ datasource.DataSource                     = &DomainDataSource{}
	_ datasource.DataSourceWithConfigValidators = &DomainDataSource{}
)

// DomainDataSource exposes a single Pangolin domain, looked up by ID (the
// per-id endpoint) or by base_domain (list-and-match, then the per-id read).
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
		Description: "Looks up a single Pangolin domain by ID or by base domain name. Set exactly one of `id` or " +
			"`base_domain`; for browsing every domain in the org, use `pangolin_domains`.",
		Attributes: map[string]schema.Attribute{
			"id":                   schema.StringAttribute{Description: "The domain ID. Set either id or base_domain.", Optional: true, Computed: true},
			"base_domain":          schema.StringAttribute{Description: "The base domain name. Set either id or base_domain. Matched case-insensitively, ignoring a trailing dot.", Optional: true, Computed: true},
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

func (d *DomainDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(
			path.MatchRoot("id"),
			path.MatchRoot("base_domain"),
		),
	}
}

func normalizeBaseDomain(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

// findDomainByBaseDomain returns the ID of the single domain whose
// base_domain matches name (case-insensitive, trailing dot ignored).
func findDomainByBaseDomain(domains []client.Domain, name string) (string, error) {
	want := normalizeBaseDomain(name)
	var ids []string
	for _, dom := range domains {
		if normalizeBaseDomain(dom.BaseDomain) == want {
			ids = append(ids, dom.DomainID)
		}
	}
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("no domain with base_domain %q in org", name)
	case 1:
		return ids[0], nil
	default:
		return "", fmt.Errorf("multiple domains with base_domain %q in org: ids %s", name, strings.Join(ids, ", "))
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

	id := cfg.ID.ValueString()
	if cfg.ID.IsNull() || cfg.ID.IsUnknown() || id == "" {
		domains, err := d.client.ListDomains(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Failed to list domains", err.Error())
			return
		}
		id, err = findDomainByBaseDomain(domains, cfg.BaseDomain.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Failed to find domain", err.Error())
			return
		}
	}

	domain, err := d.client.GetDomain(ctx, d.client.OrgID, id)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read domain", err.Error())
		return
	}

	cfg.ID = types.StringValue(id)
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
