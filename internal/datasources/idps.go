package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &IDPsDataSource{}

// IDPsDataSource defines the data source implementation.
type IDPsDataSource struct {
	client *client.Client
}

// IDPItemModel describes a single IDP in the data source.
type IDPItemModel struct {
	ID            types.Int64  `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Type          types.String `tfsdk:"type"`
	AutoProvision types.Bool   `tfsdk:"auto_provision"`
}

// IDPsDataSourceModel describes the data source data model.
type IDPsDataSourceModel struct {
	IDPs []IDPItemModel `tfsdk:"idps"`
}

// NewIDPsDataSource returns a new data source factory.
func NewIDPsDataSource() datasource.DataSource {
	return &IDPsDataSource{}
}

func (d *IDPsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_idps"
}

func (d *IDPsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves the list of Identity Providers configured in the system.",
		Attributes: map[string]schema.Attribute{
			"idps": schema.ListNestedAttribute{
				Description: "List of IDPs.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.Int64Attribute{
							Description: "The numeric IDP ID.",
							Computed:    true,
						},
						"name": schema.StringAttribute{
							Description: "The IDP display name.",
							Computed:    true,
						},
						"type": schema.StringAttribute{
							Description: "The IDP type (e.g. 'oidc').",
							Computed:    true,
						},
						"auto_provision": schema.BoolAttribute{
							Description: "Whether users are auto-provisioned on first login.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *IDPsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *IDPsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	idps, err := d.client.ListIDPs()
	if err != nil {
		resp.Diagnostics.AddError("Failed to list IDPs", err.Error())
		return
	}

	state := IDPsDataSourceModel{IDPs: []IDPItemModel{}}
	for _, idp := range idps {
		state.IDPs = append(state.IDPs, IDPItemModel{
			ID:            types.Int64Value(int64(idp.IDPId)),
			Name:          types.StringValue(idp.Name),
			Type:          types.StringValue(idp.Type),
			AutoProvision: types.BoolValue(idp.AutoProvision),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
