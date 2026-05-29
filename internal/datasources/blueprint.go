package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &BlueprintDataSource{}

// BlueprintDataSource looks up a single blueprint audit record by ID,
// exposing the apply message and the raw decoded contents on top of
// the slim fields available from the list endpoint.
type BlueprintDataSource struct {
	client *client.Client
}

// BlueprintDataSourceModel mirrors [client.BlueprintDetail].
type BlueprintDataSourceModel struct {
	ID        types.Int64  `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Source    types.String `tfsdk:"source"`
	Succeeded types.Bool   `tfsdk:"succeeded"`
	OrgID     types.String `tfsdk:"org_id"`
	CreatedAt types.Int64  `tfsdk:"created_at"`
	Message   types.String `tfsdk:"message"`
	Contents  types.String `tfsdk:"contents"`
}

// NewBlueprintDataSource returns a new data source factory.
func NewBlueprintDataSource() datasource.DataSource {
	return &BlueprintDataSource{}
}

func (d *BlueprintDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_blueprint"
}

func (d *BlueprintDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up a single blueprint audit record by ID. Returns the same " +
			"slim fields as `pangolin_blueprints` plus the apply `message` and the " +
			"raw decoded `contents` (the JSON document that was applied, after " +
			"base64-decoding).\n\n" +
			"Use this alongside `pangolin_blueprints` for inspection — e.g. dump the " +
			"latest blueprint's `contents` to a `local_file` for diff review.\n\n" +
			"> **Note:** `created_at` is epoch **seconds** (distinct from the ms used " +
			"elsewhere in the Pangolin API).",
		Attributes: map[string]schema.Attribute{
			"id":         schema.Int64Attribute{Description: "The numeric blueprint ID to look up.", Required: true},
			"name":       schema.StringAttribute{Description: "Auto-generated pet-name identifier.", Computed: true},
			"source":     schema.StringAttribute{Description: "Where the apply came from. Observed: `API`, `UI`.", Computed: true},
			"succeeded":  schema.BoolAttribute{Description: "Whether the apply succeeded.", Computed: true},
			"org_id":     schema.StringAttribute{Description: "The organization ID.", Computed: true},
			"created_at": schema.Int64Attribute{Description: "Creation timestamp (epoch **seconds**).", Computed: true},
			"message":    schema.StringAttribute{Description: "The server's apply outcome message.", Computed: true},
			"contents":   schema.StringAttribute{Description: "The decoded JSON contents that were applied.", Computed: true},
		},
	}
}

func (d *BlueprintDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *BlueprintDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config BlueprintDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bp, err := d.client.GetBlueprint(ctx, int(config.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to read blueprint", err.Error())
		return
	}

	state := BlueprintDataSourceModel{
		ID:        types.Int64Value(int64(bp.BlueprintID)),
		Name:      types.StringValue(bp.Name),
		Source:    types.StringValue(bp.Source),
		Succeeded: types.BoolValue(bp.Succeeded),
		OrgID:     types.StringValue(bp.OrgID),
		CreatedAt: types.Int64Value(bp.CreatedAt),
		Message:   types.StringValue(bp.Message),
		Contents:  types.StringValue(bp.Contents),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
