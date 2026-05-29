package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
)

var _ datasource.DataSource = &UserDataSource{}

// UserDataSource looks up a single user by their username + IDP ID.
// Usernames are unique only within an IDP, so both inputs are
// required — that mirrors the underlying API contract.
type UserDataSource struct {
	client *client.Client
}

// UserDataSourceModel describes the data source data model.
type UserDataSourceModel struct {
	Username         types.String `tfsdk:"username"`
	IdpID            types.Int64  `tfsdk:"idp_id"`
	ID               types.String `tfsdk:"id"`
	OrgID            types.String `tfsdk:"org_id"`
	Email            types.String `tfsdk:"email"`
	Name             types.String `tfsdk:"name"`
	Type             types.String `tfsdk:"type"`
	IsOwner          types.Bool   `tfsdk:"is_owner"`
	TwoFactorEnabled types.Bool   `tfsdk:"two_factor_enabled"`
	Roles            types.List   `tfsdk:"roles"`
}

// NewUserDataSource returns a new data source factory.
func NewUserDataSource() datasource.DataSource {
	return &UserDataSource{}
}

func (d *UserDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *UserDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up a Pangolin user by their `username` within an IDP. Usernames are unique only within an IDP, " +
			"so the `idp_id` input is required — matching the underlying `GET /org/{org}/user-by-username` contract.\n\n" +
			"> **Note:** This data source returns `null` for `email` and `name` when the user has not yet logged in or did not " +
			"share these claims with Pangolin. Treat them as eventually-consistent.",
		Attributes: map[string]schema.Attribute{
			"username":           schema.StringAttribute{Description: "Username to look up (the value Pangolin received from the IDP).", Required: true},
			"idp_id":             schema.Int64Attribute{Description: "Numeric ID of the IDP that issued this username. Use `pangolin_idps` to discover available IDPs.", Required: true},
			"id":                 schema.StringAttribute{Description: "Pangolin's internal user ID (UUID-like string).", Computed: true},
			"org_id":             schema.StringAttribute{Description: "Organization the user belongs to.", Computed: true},
			"email":              schema.StringAttribute{Description: "Email address claimed by the IDP. Null when none was provided.", Computed: true},
			"name":               schema.StringAttribute{Description: "Display name claimed by the IDP. Null when none was provided.", Computed: true},
			"type":               schema.StringAttribute{Description: "User type (`oidc`, `internal`, …).", Computed: true},
			"is_owner":           schema.BoolAttribute{Description: "Whether the user owns the organization.", Computed: true},
			"two_factor_enabled": schema.BoolAttribute{Description: "Whether two-factor authentication is enabled for this user.", Computed: true},
			"roles": schema.ListNestedAttribute{
				Description: "Roles currently assigned to the user, as `{role_id, role_name}` pairs.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"role_id":   schema.Int64Attribute{Description: "Numeric role ID.", Computed: true},
						"role_name": schema.StringAttribute{Description: "Role display name.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *UserDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *UserDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg UserDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	user, err := d.client.GetUserByUsername(ctx, d.client.OrgID, cfg.Username.ValueString(), int(cfg.IdpID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to look up user", err.Error())
		return
	}

	cfg.ID = types.StringValue(user.UserID)
	cfg.OrgID = types.StringValue(user.OrgID)
	cfg.Email = tfconv.StringFromPtr(user.Email)
	cfg.Name = tfconv.StringFromPtr(user.Name)
	cfg.Type = types.StringValue(user.Type)
	cfg.IsOwner = types.BoolValue(user.IsOwner)
	cfg.TwoFactorEnabled = types.BoolValue(user.TwoFactorEnabled)

	roleObjType := types.ObjectType{AttrTypes: map[string]attr.Type{
		"role_id":   types.Int64Type,
		"role_name": types.StringType,
	}}
	if len(user.Roles) == 0 {
		cfg.Roles = types.ListValueMust(roleObjType, []attr.Value{})
	} else {
		elems := make([]attr.Value, len(user.Roles))
		for i, r := range user.Roles {
			obj, diag := types.ObjectValue(roleObjType.AttrTypes, map[string]attr.Value{
				"role_id":   types.Int64Value(int64(r.RoleID)),
				"role_name": types.StringValue(r.RoleName),
			})
			resp.Diagnostics.Append(diag...)
			elems[i] = obj
		}
		listVal, diag := types.ListValue(roleObjType, elems)
		resp.Diagnostics.Append(diag...)
		cfg.Roles = listVal
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
