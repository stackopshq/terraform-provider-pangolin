package provider

import (
	"context"
	"os"

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
	URL    types.String `tfsdk:"url"`
	APIKey types.String `tfsdk:"api_key"`
	OrgID  types.String `tfsdk:"org_id"`
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
				Description: "The base URL of the Pangolin API (e.g. https://api.example.com). Can be set via PANGOLIN_URL env var.",
				Optional:    true,
			},
			"api_key": schema.StringAttribute{
				Description: "The API key for authentication. Can be set via PANGOLIN_API_KEY env var.",
				Optional:    true,
				Sensitive:   true,
			},
			"org_id": schema.StringAttribute{
				Description: "The organization ID. Can be set via PANGOLIN_ORG_ID env var.",
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

	c := client.NewClient(url, apiKey, orgID)

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
		datasources.NewIDPsDataSource,
	}
}

// resolveString returns the config value if set, otherwise falls back to the environment variable.
func resolveString(val types.String, envKey string) string {
	if !val.IsNull() && !val.IsUnknown() {
		return val.ValueString()
	}
	return os.Getenv(envKey)
}
