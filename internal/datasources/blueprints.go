package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &BlueprintsDataSource{}

// BlueprintsDataSource lists the blueprint audit records for the org.
// Blueprints are append-only "apply" snapshots - useful for audit
// reports and for picking the latest applied entry by ID.
type BlueprintsDataSource struct {
	client *client.Client
}

// BlueprintItemModel mirrors [client.Blueprint]. The `created_at`
// attribute is epoch seconds (NOT milliseconds - Pangolin is
// inconsistent here vs other list endpoints).
type BlueprintItemModel struct {
	ID        types.Int64  `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Source    types.String `tfsdk:"source"`
	Succeeded types.Bool   `tfsdk:"succeeded"`
	OrgID     types.String `tfsdk:"org_id"`
	CreatedAt types.Int64  `tfsdk:"created_at"`
}

// BlueprintsDataSourceModel is the top-level data source shape.
type BlueprintsDataSourceModel struct {
	Blueprints []BlueprintItemModel `tfsdk:"blueprints"`
}

// NewBlueprintsDataSource returns a new data source factory.
func NewBlueprintsDataSource() datasource.DataSource {
	return &BlueprintsDataSource{}
}

func (d *BlueprintsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_blueprints"
}

func (d *BlueprintsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists every blueprint audit record for the organization.\n\n" +
			"Blueprints are an append-only log of past `PUT /org/{org}/blueprint` " +
			"applies (auto-named with Pangolin's pet-name generator). There is no " +
			"DELETE endpoint - every record persists. Use this data source for audit " +
			"reporting or to fetch the latest entry by `id` for inspection via " +
			"`pangolin_blueprint`.\n\n" +
			"> **Note:** `created_at` is epoch **seconds** on this endpoint, distinct " +
			"from the milliseconds used elsewhere in the Pangolin API.",
		Attributes: map[string]schema.Attribute{
			"blueprints": schema.ListNestedAttribute{
				Description: "List of blueprint records.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":         schema.Int64Attribute{Description: "Numeric blueprint ID.", Computed: true},
						"name":       schema.StringAttribute{Description: "Auto-generated pet-name identifier.", Computed: true},
						"source":     schema.StringAttribute{Description: "Where the apply came from. Observed: `API`, `UI`.", Computed: true},
						"succeeded":  schema.BoolAttribute{Description: "Whether the apply succeeded.", Computed: true},
						"org_id":     schema.StringAttribute{Description: "The organization ID.", Computed: true},
						"created_at": schema.Int64Attribute{Description: "Creation timestamp (epoch **seconds**).", Computed: true},
					},
				},
			},
		},
	}
}

func (d *BlueprintsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *BlueprintsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	blueprints, err := d.client.ListBlueprints(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list blueprints", err.Error())
		return
	}

	state := BlueprintsDataSourceModel{Blueprints: []BlueprintItemModel{}}
	for _, b := range blueprints {
		state.Blueprints = append(state.Blueprints, BlueprintItemModel{
			ID:        types.Int64Value(int64(b.BlueprintID)),
			Name:      types.StringValue(b.Name),
			Source:    types.StringValue(b.Source),
			Succeeded: types.BoolValue(b.Succeeded),
			OrgID:     types.StringValue(b.OrgID),
			CreatedAt: types.Int64Value(b.CreatedAt),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
