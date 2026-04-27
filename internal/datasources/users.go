package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &UsersDataSource{}

// UsersDataSource defines the data source implementation.
type UsersDataSource struct {
	client *client.Client
}

// UserModel describes a single user in the data source.
type UserModel struct {
	ID       types.String `tfsdk:"id"`
	Email    types.String `tfsdk:"email"`
	Username types.String `tfsdk:"username"`
}

// UsersDataSourceModel describes the data source data model.
type UsersDataSourceModel struct {
	Users []UserModel `tfsdk:"users"`
}

// NewUsersDataSource returns a new data source factory.
func NewUsersDataSource() datasource.DataSource {
	return &UsersDataSource{}
}

func (d *UsersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_users"
}

func (d *UsersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves the list of users for the organization.",
		Attributes: map[string]schema.Attribute{
			"users": schema.ListNestedAttribute{
				Description: "List of users.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Description: "The user ID.",
							Computed:    true,
						},
						"email": schema.StringAttribute{
							Description: "The user email address.",
							Computed:    true,
						},
						"username": schema.StringAttribute{
							Description: "The username.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *UsersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *UsersDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	users, err := d.client.ListUsers(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list users", err.Error())
		return
	}

	state := UsersDataSourceModel{Users: []UserModel{}}
	for _, user := range users {
		state.Users = append(state.Users, UserModel{
			ID:       types.StringValue(user.ID),
			Email:    types.StringValue(user.Email),
			Username: types.StringValue(user.Username),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
