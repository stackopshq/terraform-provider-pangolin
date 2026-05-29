package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
)

var _ datasource.DataSource = &OrgsDataSource{}

// OrgsDataSource lists every organization visible to the calling key.
// Root-only — fails with HTTP 403 when the provider's API key is not
// server-admin scoped. Mirrors [client.ListOrgs].
type OrgsDataSource struct {
	client *client.Client
}

// OrgItemModel describes a single org. SSH CA strings are surfaced
// here even though `ssh_ca_private_key` is server-issued — callers
// who don't need it should mark this data source's outputs as
// sensitive or filter it out in HCL.
type OrgItemModel struct {
	OrgID                              types.String `tfsdk:"org_id"`
	Name                               types.String `tfsdk:"name"`
	Subnet                             types.String `tfsdk:"subnet"`
	UtilitySubnet                      types.String `tfsdk:"utility_subnet"`
	CreatedAt                          types.String `tfsdk:"created_at"`
	RequireTwoFactor                   types.Bool   `tfsdk:"require_two_factor"`
	MaxSessionLengthHours              types.Int64  `tfsdk:"max_session_length_hours"`
	PasswordExpiryDays                 types.Int64  `tfsdk:"password_expiry_days"`
	SettingsLogRetentionDaysRequest    types.Int64  `tfsdk:"settings_log_retention_days_request"`
	SettingsLogRetentionDaysAccess     types.Int64  `tfsdk:"settings_log_retention_days_access"`
	SettingsLogRetentionDaysAction     types.Int64  `tfsdk:"settings_log_retention_days_action"`
	SettingsLogRetentionDaysConnection types.Int64  `tfsdk:"settings_log_retention_days_connection"`
	SSHCaPrivateKey                    types.String `tfsdk:"ssh_ca_private_key"`
	SSHCaPublicKey                     types.String `tfsdk:"ssh_ca_public_key"`
	IsBillingOrg                       types.Bool   `tfsdk:"is_billing_org"`
	BillingOrgID                       types.String `tfsdk:"billing_org_id"`
}

// OrgsDataSourceModel is the top-level data source shape.
type OrgsDataSourceModel struct {
	Orgs []OrgItemModel `tfsdk:"orgs"`
}

// NewOrgsDataSource returns a new data source factory.
func NewOrgsDataSource() datasource.DataSource {
	return &OrgsDataSource{}
}

func (d *OrgsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_orgs"
}

func (d *OrgsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists every organization visible to the calling API key. " +
			"Useful for multi-org admin workflows and audit reporting.\n\n" +
			"> **Note:** root-only — fails with HTTP 403 when the provider's API key " +
			"is not server-admin scoped. The `ssh_ca_private_key` attribute carries " +
			"the org's SSH CA private key in clear; treat it as a credential.",
		Attributes: map[string]schema.Attribute{
			"orgs": schema.ListNestedAttribute{
				Description: "List of organizations.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"org_id":                                 schema.StringAttribute{Description: "The organization ID.", Computed: true},
						"name":                                   schema.StringAttribute{Description: "Display name of the organization.", Computed: true},
						"subnet":                                 schema.StringAttribute{Description: "The org's primary subnet (e.g. `100.90.128.0/24`).", Computed: true},
						"utility_subnet":                         schema.StringAttribute{Description: "The org's utility subnet.", Computed: true},
						"created_at":                             schema.StringAttribute{Description: "ISO-8601 timestamp of when the organization was created.", Computed: true},
						"require_two_factor":                     schema.BoolAttribute{Description: "Whether 2FA is required for users. Null when the org inherits the default.", Computed: true},
						"max_session_length_hours":               schema.Int64Attribute{Description: "Maximum session length in hours. Null when inheriting.", Computed: true},
						"password_expiry_days":                   schema.Int64Attribute{Description: "Password expiry policy in days. Null when inheriting.", Computed: true},
						"settings_log_retention_days_request":    schema.Int64Attribute{Description: "Request audit log retention in days. 0 means disabled.", Computed: true},
						"settings_log_retention_days_access":     schema.Int64Attribute{Description: "Access audit log retention in days. 0 means disabled.", Computed: true},
						"settings_log_retention_days_action":     schema.Int64Attribute{Description: "Action audit log retention in days. 0 means disabled.", Computed: true},
						"settings_log_retention_days_connection": schema.Int64Attribute{Description: "Connection audit log retention in days. 0 means disabled.", Computed: true},
						"ssh_ca_private_key":                     schema.StringAttribute{Description: "The org's SSH CA private key. Treat as a credential.", Computed: true, Sensitive: true},
						"ssh_ca_public_key":                      schema.StringAttribute{Description: "The org's SSH CA public key.", Computed: true},
						"is_billing_org":                         schema.BoolAttribute{Description: "Whether this org carries the billing account. Null in self-host.", Computed: true},
						"billing_org_id":                         schema.StringAttribute{Description: "The billing org reference. Null in self-host.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *OrgsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *OrgsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	orgs, err := d.client.ListOrgs(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list orgs", err.Error())
		return
	}

	state := OrgsDataSourceModel{Orgs: []OrgItemModel{}}
	for _, o := range orgs {
		state.Orgs = append(state.Orgs, OrgItemModel{
			OrgID:                              types.StringValue(o.OrgID),
			Name:                               types.StringValue(o.Name),
			Subnet:                             types.StringValue(o.Subnet),
			UtilitySubnet:                      types.StringValue(o.UtilitySubnet),
			CreatedAt:                          types.StringValue(o.CreatedAt),
			RequireTwoFactor:                   tfconv.BoolFromPtr(o.RequireTwoFactor),
			MaxSessionLengthHours:              tfconv.Int64FromIntPtr(o.MaxSessionLengthHours),
			PasswordExpiryDays:                 tfconv.Int64FromIntPtr(o.PasswordExpiryDays),
			SettingsLogRetentionDaysRequest:    types.Int64Value(int64(o.SettingsLogRetentionDaysRequest)),
			SettingsLogRetentionDaysAccess:     types.Int64Value(int64(o.SettingsLogRetentionDaysAccess)),
			SettingsLogRetentionDaysAction:     types.Int64Value(int64(o.SettingsLogRetentionDaysAction)),
			SettingsLogRetentionDaysConnection: types.Int64Value(int64(o.SettingsLogRetentionDaysConnection)),
			SSHCaPrivateKey:                    types.StringValue(o.SSHCaPrivateKey),
			SSHCaPublicKey:                     types.StringValue(o.SSHCaPublicKey),
			IsBillingOrg:                       types.BoolValue(o.IsBillingOrg),
			BillingOrgID:                       types.StringValue(o.BillingOrgID),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
