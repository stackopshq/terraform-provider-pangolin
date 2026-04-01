package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &APIKeysDataSource{}

// APIKeysDataSource defines the data source implementation.
type APIKeysDataSource struct {
	client *client.Client
}

// APIKeyItemModel describes a single API key in the data source.
type APIKeyItemModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

// APIKeysDataSourceModel describes the data source data model.
type APIKeysDataSourceModel struct {
	APIKeys []APIKeyItemModel `tfsdk:"api_keys"`
}

// NewAPIKeysDataSource returns a new data source factory.
func NewAPIKeysDataSource() datasource.DataSource {
	return &APIKeysDataSource{}
}

func (d *APIKeysDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_keys"
}

func (d *APIKeysDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves the list of API keys for the organization (secrets are not included).",
		Attributes: map[string]schema.Attribute{
			"api_keys": schema.ListNestedAttribute{
				Description: "List of API keys.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Description: "The API key ID.",
							Computed:    true,
						},
						"name": schema.StringAttribute{
							Description: "The API key name.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *APIKeysDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *APIKeysDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	apiKeys, err := d.client.ListAPIKeys()
	if err != nil {
		resp.Diagnostics.AddError("Failed to list API keys", err.Error())
		return
	}

	var state APIKeysDataSourceModel
	for _, key := range apiKeys {
		state.APIKeys = append(state.APIKeys, APIKeyItemModel{
			ID:   types.StringValue(key.APIKeyID),
			Name: types.StringValue(key.Name),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
