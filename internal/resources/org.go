package resources

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &OrgResource{}
	_ resource.ResourceWithImportState = &OrgResource{}
)

// OrgResource manages a Pangolin organization.
type OrgResource struct {
	client *client.Client
}

// OrgResourceModel describes the resource data model.
type OrgResourceModel struct {
	OrgID         types.String `tfsdk:"org_id"`
	Name          types.String `tfsdk:"name"`
	Subnet        types.String `tfsdk:"subnet"`
	UtilitySubnet types.String `tfsdk:"utility_subnet"`

	// Security policies (settable, nullable)
	RequireTwoFactor      types.Bool  `tfsdk:"require_two_factor"`
	MaxSessionLengthHours types.Int64 `tfsdk:"max_session_length_hours"`
	PasswordExpiryDays    types.Int64 `tfsdk:"password_expiry_days"`

	// Log retention (settable, integer days, 0 = disabled)
	LogRetentionDaysRequest    types.Int64 `tfsdk:"log_retention_days_request"`
	LogRetentionDaysAccess     types.Int64 `tfsdk:"log_retention_days_access"`
	LogRetentionDaysAction     types.Int64 `tfsdk:"log_retention_days_action"`
	LogRetentionDaysConnection types.Int64 `tfsdk:"log_retention_days_connection"`

	// Read-only enterprise / billing fields
	CreatedAt       types.String `tfsdk:"created_at"`
	SSHCaPublicKey  types.String `tfsdk:"ssh_ca_public_key"`
	SSHCaPrivateKey types.String `tfsdk:"ssh_ca_private_key"`
	IsBillingOrg    types.Bool   `tfsdk:"is_billing_org"`
	BillingOrgID    types.String `tfsdk:"billing_org_id"`
}

// NewOrgResource returns a new resource factory.
func NewOrgResource() resource.Resource {
	return &OrgResource{}
}

func (r *OrgResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_org"
}

func (r *OrgResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Pangolin organization. Available on self-hosted (OSS / Enterprise) deployments and on Pangolin Cloud. " +
			"Security policies (`require_two_factor`, `max_session_length_hours`, `password_expiry_days`) and log retention settings are returned by the API as nullable values; leaving them unset lets the server default apply.\n\n" +
			"> **Note:** `ssh_ca_private_key` is returned in clear by the Pangolin API and stored in Terraform state. Treat the state file as a secret accordingly.",
		Attributes: map[string]schema.Attribute{
			"org_id": schema.StringAttribute{
				Description: "The organization ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"name": schema.StringAttribute{
				Description: "The display name of the organization.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"subnet": schema.StringAttribute{
				Description: "The CIDR subnet allocated to the organization (e.g. `100.90.0.0/24`). Set at creation; not modifiable afterwards.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"utility_subnet": schema.StringAttribute{
				Description: "The utility CIDR subnet allocated to the organization (e.g. `100.96.0.0/24`). Set at creation; not modifiable afterwards.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			// Security policies
			"require_two_factor": schema.BoolAttribute{
				Description: "Force two-factor authentication for all members of the organization. Unset means inherit the server default.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"max_session_length_hours": schema.Int64Attribute{
				Description: "Maximum session length in hours before users must re-authenticate. Unset means no enforced cap (server default).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			"password_expiry_days": schema.Int64Attribute{
				Description: "Force users to rotate their password after this many days. Unset means no enforced rotation.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			// Log retention (number of days; 0 disables that stream)
			"log_retention_days_request": schema.Int64Attribute{
				Description: "Retention in days for the request audit log (proxy traffic). `0` disables retention.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(0),
				},
			},
			"log_retention_days_access": schema.Int64Attribute{
				Description: "Retention in days for the access audit log. `0` disables retention. Requires an active enterprise subscription on Pangolin Cloud.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(0),
				},
			},
			"log_retention_days_action": schema.Int64Attribute{
				Description: "Retention in days for the action audit log. `0` disables retention. Requires an active enterprise subscription on Pangolin Cloud.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(0),
				},
			},
			"log_retention_days_connection": schema.Int64Attribute{
				Description: "Retention in days for the connection audit log. `0` disables retention. Requires an active enterprise subscription on Pangolin Cloud.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(0),
				},
			},
			// Read-only fields
			"created_at": schema.StringAttribute{
				Description: "ISO 8601 / RFC 3339 timestamp when the organization was created.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"ssh_ca_public_key": schema.StringAttribute{
				Description: "Public key of the SSH certificate authority used to sign user certificates for the Pangolin SSH bastion feature. Distribute to host `TrustedUserCAKeys` config.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"ssh_ca_private_key": schema.StringAttribute{
				Description: "Private key of the SSH certificate authority. Returned in clear by the API; stored in Terraform state. Treat the state as a secret accordingly.",
				Computed:    true,
				Sensitive:   true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"is_billing_org": schema.BoolAttribute{
				Description: "Whether this organization carries its own billing account (`true`) or piggybacks on another (`false`).",
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"billing_org_id": schema.StringAttribute{
				Description: "ID of the organization that carries the billing account for this org. Usually the org's own ID when `is_billing_org` is true.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *OrgResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", "Expected *client.Client")
		return
	}
	r.client = c
}

func (r *OrgResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan OrgResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.CreateOrg(ctx, &client.CreateOrgRequest{
		OrgID:         plan.OrgID.ValueString(),
		Name:          plan.Name.ValueString(),
		Subnet:        plan.Subnet.ValueString(),
		UtilitySubnet: plan.UtilitySubnet.ValueString(),
	}); err != nil {
		resp.Diagnostics.AddError("Failed to create org", err.Error())
		return
	}

	// Create only accepts name + subnets. If the plan sets any policies
	// or retention, apply them via an Update before the final Read.
	if hasOrgPolicyChanges(plan) {
		if _, err := r.client.UpdateOrg(ctx, plan.OrgID.ValueString(), buildUpdateOrgRequest(plan)); err != nil {
			resp.Diagnostics.AddError("Failed to apply org policies after create", err.Error())
			return
		}
	}

	got, err := r.client.GetOrg(ctx, plan.OrgID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read org after create", err.Error())
		return
	}

	state := orgToModel(got, plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *OrgResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state OrgResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	org, err := r.client.GetOrg(ctx, state.OrgID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read org", err.Error())
		return
	}

	next := orgToModel(org, state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *OrgResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan OrgResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.UpdateOrg(ctx, plan.OrgID.ValueString(), buildUpdateOrgRequest(plan)); err != nil {
		resp.Diagnostics.AddError("Failed to update org", err.Error())
		return
	}

	got, err := r.client.GetOrg(ctx, plan.OrgID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read org after update", err.Error())
		return
	}

	state := orgToModel(got, plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *OrgResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state OrgResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteOrg(ctx, state.OrgID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to delete org", err.Error())
		return
	}
}

func (r *OrgResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	org, err := r.client.GetOrg(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import org", err.Error())
		return
	}

	state := orgToModel(org, OrgResourceModel{})
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// buildUpdateOrgRequest extracts the settable fields from a plan into
// the client's UpdateOrgRequest. Unset (null) attributes stay nil so
// they are omitted from the JSON body via omitempty.
func buildUpdateOrgRequest(plan OrgResourceModel) *client.UpdateOrgRequest {
	req := &client.UpdateOrgRequest{Name: plan.Name.ValueString()}
	if !plan.RequireTwoFactor.IsNull() && !plan.RequireTwoFactor.IsUnknown() {
		v := plan.RequireTwoFactor.ValueBool()
		req.RequireTwoFactor = &v
	}
	if !plan.MaxSessionLengthHours.IsNull() && !plan.MaxSessionLengthHours.IsUnknown() {
		v := int(plan.MaxSessionLengthHours.ValueInt64())
		req.MaxSessionLengthHours = &v
	}
	if !plan.PasswordExpiryDays.IsNull() && !plan.PasswordExpiryDays.IsUnknown() {
		v := int(plan.PasswordExpiryDays.ValueInt64())
		req.PasswordExpiryDays = &v
	}
	if !plan.LogRetentionDaysRequest.IsNull() && !plan.LogRetentionDaysRequest.IsUnknown() {
		v := int(plan.LogRetentionDaysRequest.ValueInt64())
		req.SettingsLogRetentionDaysRequest = &v
	}
	if !plan.LogRetentionDaysAccess.IsNull() && !plan.LogRetentionDaysAccess.IsUnknown() {
		v := int(plan.LogRetentionDaysAccess.ValueInt64())
		req.SettingsLogRetentionDaysAccess = &v
	}
	if !plan.LogRetentionDaysAction.IsNull() && !plan.LogRetentionDaysAction.IsUnknown() {
		v := int(plan.LogRetentionDaysAction.ValueInt64())
		req.SettingsLogRetentionDaysAction = &v
	}
	if !plan.LogRetentionDaysConnection.IsNull() && !plan.LogRetentionDaysConnection.IsUnknown() {
		v := int(plan.LogRetentionDaysConnection.ValueInt64())
		req.SettingsLogRetentionDaysConnection = &v
	}
	return req
}

// hasOrgPolicyChanges reports whether the plan sets any of the
// policy / retention attributes - used to decide whether Create
// should be followed by an immediate Update.
func hasOrgPolicyChanges(plan OrgResourceModel) bool {
	for _, v := range []interface{ IsNull() bool }{
		plan.RequireTwoFactor,
		plan.MaxSessionLengthHours,
		plan.PasswordExpiryDays,
		plan.LogRetentionDaysRequest,
		plan.LogRetentionDaysAccess,
		plan.LogRetentionDaysAction,
		plan.LogRetentionDaysConnection,
	} {
		if !v.IsNull() {
			return true
		}
	}
	return false
}

// orgToModel maps a *client.Org onto an OrgResourceModel. Subnet and
// utility_subnet fall back to the prior plan/state when the server
// returns blanks (UpdateOrg does not always echo network fields).
func orgToModel(org *client.Org, prior OrgResourceModel) OrgResourceModel {
	m := OrgResourceModel{
		OrgID:                      types.StringValue(org.OrgID),
		Name:                       types.StringValue(org.Name),
		Subnet:                     types.StringValue(org.Subnet),
		UtilitySubnet:              types.StringValue(org.UtilitySubnet),
		CreatedAt:                  types.StringValue(org.CreatedAt),
		SSHCaPublicKey:             types.StringValue(org.SSHCaPublicKey),
		SSHCaPrivateKey:            types.StringValue(org.SSHCaPrivateKey),
		IsBillingOrg:               types.BoolValue(org.IsBillingOrg),
		BillingOrgID:               types.StringValue(org.BillingOrgID),
		LogRetentionDaysRequest:    types.Int64Value(int64(org.SettingsLogRetentionDaysRequest)),
		LogRetentionDaysAccess:     types.Int64Value(int64(org.SettingsLogRetentionDaysAccess)),
		LogRetentionDaysAction:     types.Int64Value(int64(org.SettingsLogRetentionDaysAction)),
		LogRetentionDaysConnection: types.Int64Value(int64(org.SettingsLogRetentionDaysConnection)),
	}
	if org.RequireTwoFactor != nil {
		m.RequireTwoFactor = types.BoolValue(*org.RequireTwoFactor)
	} else {
		m.RequireTwoFactor = types.BoolNull()
	}
	if org.MaxSessionLengthHours != nil {
		m.MaxSessionLengthHours = types.Int64Value(int64(*org.MaxSessionLengthHours))
	} else {
		m.MaxSessionLengthHours = types.Int64Null()
	}
	if org.PasswordExpiryDays != nil {
		m.PasswordExpiryDays = types.Int64Value(int64(*org.PasswordExpiryDays))
	} else {
		m.PasswordExpiryDays = types.Int64Null()
	}
	if m.Subnet.ValueString() == "" && !prior.Subnet.IsNull() {
		m.Subnet = prior.Subnet
	}
	if m.UtilitySubnet.ValueString() == "" && !prior.UtilitySubnet.IsNull() {
		m.UtilitySubnet = prior.UtilitySubnet
	}
	return m
}
