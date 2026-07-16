package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
)

var _ datasource.DataSource = &AccessTokensDataSource{}

// AccessTokensDataSource lists every resource access token in the
// organization. The bearer secrets are not exposed by the upstream
// list endpoint - only the SHA-256 token hash and the metadata.
type AccessTokensDataSource struct {
	client *client.Client
}

// AccessTokenItemModel describes a single access token entry in the
// data source. Nullable upstream fields are mapped to TF nulls; the
// optional `siteName` enrichment is only populated for tokens bound
// to site-level resources.
type AccessTokenItemModel struct {
	ID             types.String `tfsdk:"id"`
	ResourceID     types.Int64  `tfsdk:"resource_id"`
	ResourceName   types.String `tfsdk:"resource_name"`
	ResourceNiceID types.String `tfsdk:"resource_nice_id"`
	SiteName       types.String `tfsdk:"site_name"`
	Title          types.String `tfsdk:"title"`
	Description    types.String `tfsdk:"description"`
	SessionLength  types.Int64  `tfsdk:"session_length"`
	ExpiresAt      types.Int64  `tfsdk:"expires_at"`
	CreatedAt      types.Int64  `tfsdk:"created_at"`
	TokenHash      types.String `tfsdk:"token_hash"`
}

// AccessTokensDataSourceModel is the top-level data source shape.
type AccessTokensDataSourceModel struct {
	AccessTokens []AccessTokenItemModel `tfsdk:"access_tokens"`
}

// NewAccessTokensDataSource returns a new data source factory.
func NewAccessTokensDataSource() datasource.DataSource {
	return &AccessTokensDataSource{}
}

func (d *AccessTokensDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_access_tokens"
}

func (d *AccessTokensDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists every resource access token in the organization.\n\n" +
			"> **Note:** the bearer secret is never returned by this endpoint - " +
			"only the SHA-256 `token_hash` is exposed. Filter by `resource_id` " +
			"in HCL to scope to a single resource.",
		Attributes: map[string]schema.Attribute{
			"access_tokens": schema.ListNestedAttribute{
				Description: "List of access tokens.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":               schema.StringAttribute{Description: "The access token ID.", Computed: true},
						"resource_id":      schema.Int64Attribute{Description: "The HTTP resource this token authorizes.", Computed: true},
						"resource_name":    schema.StringAttribute{Description: "Display name of the parent resource.", Computed: true},
						"resource_nice_id": schema.StringAttribute{Description: "Human-readable ID of the parent resource.", Computed: true},
						"site_name":        schema.StringAttribute{Description: "Site name for site-level resources. Null otherwise.", Computed: true},
						"title":            schema.StringAttribute{Description: "Optional human-readable title. Null when unset.", Computed: true},
						"description":      schema.StringAttribute{Description: "Optional description. Null when unset.", Computed: true},
						"session_length":   schema.Int64Attribute{Description: "Lifetime in milliseconds.", Computed: true},
						"expires_at":       schema.Int64Attribute{Description: "Expiration timestamp (epoch ms). Null when the token never expires.", Computed: true},
						"created_at":       schema.Int64Attribute{Description: "Creation timestamp (epoch ms).", Computed: true},
						"token_hash":       schema.StringAttribute{Description: "SHA-256 hash of the bearer token.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *AccessTokensDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *AccessTokensDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	tokens, err := d.client.ListOrgAccessTokens(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list access tokens", err.Error())
		return
	}

	state := AccessTokensDataSourceModel{AccessTokens: []AccessTokenItemModel{}}
	for _, t := range tokens {
		state.AccessTokens = append(state.AccessTokens, AccessTokenItemModel{
			ID:             types.StringValue(t.AccessTokenID),
			ResourceID:     types.Int64Value(int64(t.ResourceID)),
			ResourceName:   types.StringValue(t.ResourceName),
			ResourceNiceID: types.StringValue(t.ResourceNiceID),
			SiteName:       tfconv.StringFromPtr(t.SiteName),
			Title:          tfconv.StringFromPtr(t.Title),
			Description:    tfconv.StringFromPtr(t.Description),
			SessionLength:  types.Int64Value(t.SessionLength),
			ExpiresAt:      tfconv.Int64FromInt64Ptr(t.ExpiresAt),
			CreatedAt:      types.Int64Value(t.CreatedAt),
			TokenHash:      types.StringValue(t.TokenHash),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
