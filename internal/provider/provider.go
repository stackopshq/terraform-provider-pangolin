package provider

import (
	"context"
	"crypto/x509"
	"errors"
	"os"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/datasources"
	"github.com/stackopshq/terraform-provider-pangolin/internal/resources"
)

var _ provider.Provider = &PangolinProvider{}

// PangolinProvider defines the provider implementation.
type PangolinProvider struct {
	version string
}

// PangolinProviderModel describes the provider data model.
type PangolinProviderModel struct {
	URL         types.String `tfsdk:"url"`
	APIKey      types.String `tfsdk:"api_key"`
	OrgID       types.String `tfsdk:"org_id"`
	CACertPEM   types.String `tfsdk:"ca_cert_pem"`
	TLSInsecure types.Bool   `tfsdk:"tls_insecure"`
}

// New returns a new provider factory function.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &PangolinProvider{
			version: version,
		}
	}
}

func (p *PangolinProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "pangolin"
	resp.Version = p.version
}

func (p *PangolinProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Terraform provider for managing Pangolin resources (sites, resources, targets, roles).",
		Attributes: map[string]schema.Attribute{
			"url": schema.StringAttribute{
				Description: "The base URL of the Pangolin API (e.g. `https://pangolin.example.com`). Can be set via `PANGOLIN_URL` env var.",
				Optional:    true,
			},
			"api_key": schema.StringAttribute{
				Description: "The API key for authentication. Can be set via `PANGOLIN_API_KEY` env var.",
				Optional:    true,
				Sensitive:   true,
			},
			"org_id": schema.StringAttribute{
				Description: "The organization ID. Can be set via `PANGOLIN_ORG_ID` env var.",
				Optional:    true,
			},
			"ca_cert_pem": schema.StringAttribute{
				Description: "PEM-encoded CA certificate(s) used to verify the Pangolin server's TLS certificate. Set this when the Pangolin instance is served by a private or self-signed CA. Multiple certificates may be concatenated. Can be set via `PANGOLIN_CA_CERT_PEM` env var.",
				Optional:    true,
			},
			"tls_insecure": schema.BoolAttribute{
				Description: "Skip TLS certificate verification entirely. Intended for local debugging only - never use against production. Can be set via `PANGOLIN_TLS_INSECURE` env var.",
				Optional:    true,
			},
		},
	}
}

func (p *PangolinProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config PangolinProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Resolve values from config or environment variables
	url := resolveString(config.URL, "PANGOLIN_URL")
	apiKey := resolveString(config.APIKey, "PANGOLIN_API_KEY")
	orgID := resolveString(config.OrgID, "PANGOLIN_ORG_ID")
	caCertPEM := resolveString(config.CACertPEM, "PANGOLIN_CA_CERT_PEM")
	tlsInsecure := resolveBool(config.TLSInsecure, "PANGOLIN_TLS_INSECURE")

	if url == "" {
		resp.Diagnostics.AddError("Missing URL", "The Pangolin API URL must be set via the 'url' attribute or PANGOLIN_URL environment variable.")
		return
	}
	if apiKey == "" {
		resp.Diagnostics.AddError("Missing API Key", "The Pangolin API key must be set via the 'api_key' attribute or PANGOLIN_API_KEY environment variable.")
		return
	}
	if orgID == "" {
		resp.Diagnostics.AddError("Missing Org ID", "The Pangolin organization ID must be set via the 'org_id' attribute or PANGOLIN_ORG_ID environment variable.")
		return
	}

	var opts []client.Option
	if caCertPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(caCertPEM)) {
			resp.Diagnostics.AddError(
				"Invalid CA certificate",
				"The 'ca_cert_pem' attribute (or PANGOLIN_CA_CERT_PEM env var) does not contain any valid PEM-encoded certificate.",
			)
			return
		}
		opts = append(opts, client.WithCAPool(pool))
	}
	if tlsInsecure {
		resp.Diagnostics.AddWarning(
			"TLS verification disabled",
			"'tls_insecure' is enabled - TLS certificate verification is being skipped for every Pangolin API call. Do not use this against a production instance.",
		)
		opts = append(opts, client.WithInsecureTLS())
	}

	c := client.NewClient(url, apiKey, orgID, opts...)

	// Bootstrap probe: hit the org-scoped endpoint once so a bad URL,
	// unreachable server, wrong API key, or missing org fails fast at
	// plan/apply configure time instead of during the first resource
	// operation. Auth/permission/org-lookup errors are fatal (nothing
	// downstream will work). Transient errors (transport, 5xx) surface
	// as warnings so a Pangolin blip doesn't block "terraform plan".
	// The probe can be disabled by setting PANGOLIN_SKIP_HEALTH_PROBE=1
	// (useful in CI or offline planning).
	if os.Getenv("PANGOLIN_SKIP_HEALTH_PROBE") != "1" {
		if _, err := c.GetOrg(ctx, orgID); err != nil {
			switch {
			case errors.Is(err, client.ErrUnauthorized):
				resp.Diagnostics.AddError(
					"Pangolin authentication failed",
					"The provider's API key was rejected by Pangolin (401). Check `api_key` / PANGOLIN_API_KEY.\n\nUnderlying error: "+err.Error(),
				)
				return
			case errors.Is(err, client.ErrForbidden):
				resp.Diagnostics.AddError(
					"Pangolin API key lacks access",
					"The provider's API key was accepted but is not authorized on organization "+orgID+" (403). Check `org_id` / PANGOLIN_ORG_ID and the API key's org scope.\n\nUnderlying error: "+err.Error(),
				)
				return
			case errors.Is(err, client.ErrNotFound):
				resp.Diagnostics.AddError(
					"Pangolin organization not found",
					"Organization `"+orgID+"` was not found on the target Pangolin instance (404). Check `org_id` / PANGOLIN_ORG_ID.\n\nUnderlying error: "+err.Error(),
				)
				return
			default:
				resp.Diagnostics.AddWarning(
					"Pangolin health probe failed",
					"Could not reach the Pangolin API during provider configuration. Plans will still be built but apply may fail if the server stays unreachable. Set PANGOLIN_SKIP_HEALTH_PROBE=1 to silence this probe.\n\nUnderlying error: "+err.Error(),
				)
			}
		}
	}

	resp.DataSourceData = c
	resp.ResourceData = c
}

func (p *PangolinProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		resources.NewOrgResource,
		resources.NewUserResource,
		resources.NewSiteResource,
		resources.NewHTTPResource,
		resources.NewTargetResource,
		resources.NewSitePrivateResource,
		resources.NewRoleResource,
		resources.NewAPIKeyResource,
		resources.NewOLMClientResource,
		resources.NewResourceRoleResource,
		resources.NewResourceUserResource,
		resources.NewResourceWhitelistResource,
		resources.NewSiteResourceRoleResource,
		resources.NewSiteResourceUserResource,
		resources.NewSiteResourceClientResource,
		resources.NewIDPResource,
		resources.NewIDPOrgResource,
		resources.NewRoleUserResource,
		resources.NewResourceRuleResource,
		resources.NewResourcePasswordResource,
		resources.NewResourcePincodeResource,
		resources.NewResourceHeaderAuthResource,
		resources.NewInvitationResource,
		resources.NewUserRoleResource,
		resources.NewResourceAccessTokenResource,
		resources.NewAPIKeyActionsResource,
		resources.NewOrgIDPResource,
	}
}

func (p *PangolinProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		datasources.NewDomainsDataSource,
		datasources.NewRolesDataSource,
		datasources.NewUsersDataSource,
		datasources.NewSitesDataSource,
		datasources.NewHTTPResourcesDataSource,
		datasources.NewSiteResourcesDataSource,
		datasources.NewAPIKeysDataSource,
		datasources.NewAccessTokensDataSource,
		datasources.NewIDPsDataSource,
		datasources.NewRequestLogsDataSource,
		datasources.NewLogsAnalyticsDataSource,
		datasources.NewResourceTargetsDataSource,
		datasources.NewResourceRolesDataSource,
		datasources.NewDomainDataSource,
		datasources.NewDomainDNSRecordsDataSource,
		datasources.NewSiteDataSource,
		datasources.NewUserDataSource,
		datasources.NewUserByIDDataSource,
		datasources.NewOrgsDataSource,
		datasources.NewUserDevicesDataSource,
		datasources.NewBlueprintsDataSource,
		datasources.NewBlueprintDataSource,
		datasources.NewAccessLogsDataSource,
		datasources.NewActionLogsDataSource,
		datasources.NewConnectionLogsDataSource,
	}
}

// resolveString returns the config value if set, otherwise falls back to the environment variable.
func resolveString(val types.String, envKey string) string {
	if !val.IsNull() && !val.IsUnknown() {
		return val.ValueString()
	}
	return os.Getenv(envKey)
}

// resolveBool returns the config value if set, otherwise parses the named
// environment variable. Unparseable env values are treated as false so a
// stray "yes" or empty string never silently disables TLS verification.
func resolveBool(val types.Bool, envKey string) bool {
	if !val.IsNull() && !val.IsUnknown() {
		return val.ValueBool()
	}
	raw := os.Getenv(envKey)
	if raw == "" {
		return false
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return parsed
}
