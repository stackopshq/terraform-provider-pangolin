package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &ResourceRolesDataSource{}

// ResourceRolesDataSource lists the roles assigned to a resource.
type ResourceRolesDataSource struct {
	client *client.Client
}

// ResourceRoleItemModel mirrors the slim shape the list endpoint
// returns - only id / name / description / isAdmin. The full Role
// struct is reachable via the pangolin_roles data source.
type ResourceRoleItemModel struct {
	ID          types.Int64  `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	IsAdmin     types.Bool   `tfsdk:"is_admin"`
}

// ResourceRolesDataSourceModel describes the data source data model.
type ResourceRolesDataSourceModel struct {
	ResourceID types.Int64             `tfsdk:"resource_id"`
	Roles      []ResourceRoleItemModel `tfsdk:"roles"`
}

// NewResourceRolesDataSource returns a new data source factory.
func NewResourceRolesDataSource() datasource.DataSource {
	return &ResourceRolesDataSource{}
}

func (d *ResourceRolesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource_roles"
}

func (d *ResourceRolesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the roles currently granted access to a Pangolin HTTP resource. " +
			"The full role definition (SSH bastion settings, etc.) is available on `pangolin_roles`; " +
			"this data source returns the slim summary the resource-scoped endpoint emits.",
		Attributes: map[string]schema.Attribute{
			"resource_id": schema.Int64Attribute{
				Description: "Numeric ID of the HTTP resource whose roles to list.",
				Required:    true,
			},
			"roles": schema.ListNestedAttribute{
				Description: "Roles granted access to the resource.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          schema.Int64Attribute{Description: "Numeric ID of the role.", Computed: true},
						"name":        schema.StringAttribute{Description: "Display name of the role.", Computed: true},
						"description": schema.StringAttribute{Description: "Role description.", Computed: true},
						"is_admin":    schema.BoolAttribute{Description: "Whether the role is the built-in admin role of its organization.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *ResourceRolesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ResourceRolesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg ResourceRolesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	roles, err := d.client.ListResourceRoles(ctx, int(cfg.ResourceID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to list resource roles", err.Error())
		return
	}

	cfg.Roles = make([]ResourceRoleItemModel, len(roles))
	for i, r := range roles {
		cfg.Roles[i] = ResourceRoleItemModel{
			ID:          types.Int64Value(int64(r.RoleID)),
			Name:        types.StringValue(r.Name),
			Description: types.StringValue(r.Description),
			IsAdmin:     types.BoolValue(r.IsAdmin),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
