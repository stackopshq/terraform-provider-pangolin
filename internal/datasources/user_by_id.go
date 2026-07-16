package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
)

var _ datasource.DataSource = &UserByIDDataSource{}

// UserByIDDataSource looks up a Pangolin user by their cross-org
// `user_id`. Distinct from the existing `pangolin_user` data source,
// which keys on `username + idp_id` within an org - this one queries
// the root-only `GET /user/{userId}` endpoint and surfaces the extra
// fields it carries (server_admin, two_factor_setup_requested,
// email_verified, date_created, idp_name).
type UserByIDDataSource struct {
	client *client.Client
}

// UserByIDDataSourceModel mirrors [client.RootUserDetail] shape.
// Nullable upstream fields use TF null where the wire emits null.
type UserByIDDataSourceModel struct {
	UserID                  types.String `tfsdk:"user_id"`
	Email                   types.String `tfsdk:"email"`
	Username                types.String `tfsdk:"username"`
	Name                    types.String `tfsdk:"name"`
	Type                    types.String `tfsdk:"type"`
	TwoFactorEnabled        types.Bool   `tfsdk:"two_factor_enabled"`
	TwoFactorSetupRequested types.Bool   `tfsdk:"two_factor_setup_requested"`
	EmailVerified           types.Bool   `tfsdk:"email_verified"`
	ServerAdmin             types.Bool   `tfsdk:"server_admin"`
	IDPName                 types.String `tfsdk:"idp_name"`
	IDPID                   types.Int64  `tfsdk:"idp_id"`
	DateCreated             types.String `tfsdk:"date_created"`
}

// NewUserByIDDataSource returns a new data source factory.
func NewUserByIDDataSource() datasource.DataSource {
	return &UserByIDDataSource{}
}

func (d *UserByIDDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_by_id"
}

func (d *UserByIDDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up a Pangolin user by their cross-org `user_id`.\n\n" +
			"Distinct from `pangolin_user` (keyed by `username + idp_id` within an org). " +
			"This data source queries the root-only `GET /user/{userId}` endpoint and " +
			"surfaces extra fields not exposed by the org-scoped variant: `server_admin`, " +
			"`two_factor_setup_requested`, `email_verified`, `date_created`, `idp_name`.\n\n" +
			"> **Note:** root-only - fails with HTTP 403 when the provider's API key is not " +
			"server-admin scoped.",
		Attributes: map[string]schema.Attribute{
			"user_id": schema.StringAttribute{
				Description: "The user ID to look up.",
				Required:    true,
			},
			"email":                      schema.StringAttribute{Description: "The user's email address.", Computed: true},
			"username":                   schema.StringAttribute{Description: "The user's username (IDP-issued or local).", Computed: true},
			"name":                       schema.StringAttribute{Description: "The user's display name. Null when unset.", Computed: true},
			"type":                       schema.StringAttribute{Description: "Account type - `internal` for local accounts, otherwise the IDP variant name.", Computed: true},
			"two_factor_enabled":         schema.BoolAttribute{Description: "Whether the user has 2FA enrolled and active.", Computed: true},
			"two_factor_setup_requested": schema.BoolAttribute{Description: "Whether the user has been asked to set up 2FA on next login.", Computed: true},
			"email_verified":             schema.BoolAttribute{Description: "Whether the user's email address has been verified.", Computed: true},
			"server_admin":               schema.BoolAttribute{Description: "Whether the user holds server-admin (root) scope.", Computed: true},
			"idp_name":                   schema.StringAttribute{Description: "The display name of the IDP that provisioned this user. Null for internal users.", Computed: true},
			"idp_id":                     schema.Int64Attribute{Description: "The numeric ID of the IDP that provisioned this user. Null for internal users.", Computed: true},
			"date_created":               schema.StringAttribute{Description: "ISO-8601 timestamp of when the user was created.", Computed: true},
		},
	}
}

func (d *UserByIDDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *UserByIDDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config UserByIDDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	user, err := d.client.GetUserByID(ctx, config.UserID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read user", err.Error())
		return
	}

	state := UserByIDDataSourceModel{
		UserID:                  types.StringValue(user.UserID),
		Email:                   types.StringValue(user.Email),
		Username:                types.StringValue(user.Username),
		Name:                    tfconv.StringFromPtr(user.Name),
		Type:                    types.StringValue(user.Type),
		TwoFactorEnabled:        types.BoolValue(user.TwoFactorEnabled),
		TwoFactorSetupRequested: types.BoolValue(user.TwoFactorSetupRequested),
		EmailVerified:           types.BoolValue(user.EmailVerified),
		ServerAdmin:             types.BoolValue(user.ServerAdmin),
		IDPName:                 tfconv.StringFromPtr(user.IDPName),
		IDPID:                   tfconv.Int64FromInt64Ptr(user.IDPID),
		DateCreated:             types.StringValue(user.DateCreated),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
