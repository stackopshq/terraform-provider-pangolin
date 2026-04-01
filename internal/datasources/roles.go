package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &RolesDataSource{}

// RolesDataSource defines the data source implementation.
type RolesDataSource struct {
	client *client.Client
}

// RoleModel describes a single role in the data source.
type RoleModel struct {
	RoleID      types.Int64  `tfsdk:"role_id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
}

// RolesDataSourceModel describes the data source data model.
type RolesDataSourceModel struct {
	Roles []RoleModel `tfsdk:"roles"`
}

// NewRolesDataSource returns a new data source factory.
func NewRolesDataSource() datasource.DataSource {
	return &RolesDataSource{}
}

func (d *RolesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_roles"
}

func (d *RolesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves the list of roles for the organization.",
		Attributes: map[string]schema.Attribute{
			"roles": schema.ListNestedAttribute{
				Description: "List of roles.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"role_id": schema.Int64Attribute{
							Description: "The role ID.",
							Computed:    true,
						},
						"name": schema.StringAttribute{
							Description: "The role name.",
							Computed:    true,
						},
						"description": schema.StringAttribute{
							Description: "The role description.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *RolesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *RolesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	roles, err := d.client.ListRoles()
	if err != nil {
		resp.Diagnostics.AddError("Failed to list roles", err.Error())
		return
	}

	var state RolesDataSourceModel
	for _, role := range roles {
		state.Roles = append(state.Roles, RoleModel{
			RoleID:      types.Int64Value(int64(role.RoleID)),
			Name:        types.StringValue(role.Name),
			Description: types.StringValue(role.Description),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
