package resources

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &InvitationResource{}
	_ resource.ResourceWithImportState = &InvitationResource{}
)

// InvitationResource manages a pending Pangolin organization invitation.
type InvitationResource struct {
	client *client.Client
}

// InvitationResourceModel describes the resource data model.
type InvitationResourceModel struct {
	ID         types.String `tfsdk:"id"`
	Email      types.String `tfsdk:"email"`
	RoleID     types.Int64  `tfsdk:"role_id"`
	ValidHours types.Int64  `tfsdk:"valid_hours"`
	SendEmail  types.Bool   `tfsdk:"send_email"`
	Regenerate types.Bool   `tfsdk:"regenerate"`
	InviteLink types.String `tfsdk:"invite_link"`
	ExpiresAt  types.Int64  `tfsdk:"expires_at"`
	Roles      types.List   `tfsdk:"roles"`
}

// NewInvitationResource returns a new resource factory.
func NewInvitationResource() resource.Resource {
	return &InvitationResource{}
}

func (r *InvitationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_invitation"
}

func (r *InvitationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a pending invitation for a user to join the configured organization. " +
			"Invitations are immutable once created - changing any attribute triggers a destroy + create. " +
			"Use `terraform destroy` (or removing the resource block) to cancel an open invitation.\n\n" +
			"> **Note:** `invite_link` carries a single-use token in clear and is stored in state. Treat the state as a secret.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The invitation ID assigned by Pangolin (the first segment of the invite token).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"email": schema.StringAttribute{
				Description: "Email address of the invitee.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(3),
				},
			},
			"role_id": schema.Int64Attribute{
				Description: "Numeric ID of the role to assign once the invitation is accepted.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			"valid_hours": schema.Int64Attribute{
				Description: "How long the invite remains valid, in hours. Server default applies when omitted.",
				Optional:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			"send_email": schema.BoolAttribute{
				Description: "When `true`, Pangolin sends an invitation email to the address. Defaults to `false` (use the returned `invite_link` directly).",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"regenerate": schema.BoolAttribute{
				Description: "When `true`, regenerates an existing invitation for the same email instead of erroring on conflict. Defaults to `false`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"invite_link": schema.StringAttribute{
				Description: "Full invitation URL including the single-use token. The recipient opens this to accept.",
				Computed:    true,
				Sensitive:   true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"expires_at": schema.Int64Attribute{
				Description: "Expiry timestamp as milliseconds since Unix epoch.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"roles": schema.ListAttribute{
				Description: "Numeric IDs of the roles attached to this invitation. Mirrors `role_id` today but the API supports multiple in principle.",
				ElementType: types.Int64Type,
				Computed:    true,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *InvitationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *InvitationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan InvitationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq := &client.CreateInviteRequest{
		Email:      plan.Email.ValueString(),
		RoleID:     int(plan.RoleID.ValueInt64()),
		SendEmail:  plan.SendEmail.ValueBool(),
		Regenerate: plan.Regenerate.ValueBool(),
	}
	if !plan.ValidHours.IsNull() && !plan.ValidHours.IsUnknown() {
		createReq.ValidHours = int(plan.ValidHours.ValueInt64())
	}

	created, err := r.client.CreateInvite(ctx, r.client.OrgID, createReq)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create invitation", err.Error())
		return
	}

	// The create response does not echo inviteId. Look it up by email.
	invite, err := r.client.FindInvitationByEmail(ctx, r.client.OrgID, plan.Email.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Created invitation but could not look it up",
			"The create succeeded (invite link issued) but the follow-up list did not return an entry matching the email: "+err.Error(),
		)
		return
	}

	plan.ID = types.StringValue(invite.InviteID)
	plan.InviteLink = types.StringValue(created.InviteLink)
	plan.ExpiresAt = types.Int64Value(created.ExpiresAt)
	plan.Roles = invitationRoleIDsToList(ctx, invite.Roles, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *InvitationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state InvitationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	invite, err := r.client.GetInvitation(ctx, r.client.OrgID, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			// Accepted, expired, or canceled out of band.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read invitation", err.Error())
		return
	}

	state.Email = types.StringValue(invite.Email)
	state.ExpiresAt = types.Int64Value(invite.ExpiresAt)
	state.Roles = invitationRoleIDsToList(ctx, invite.Roles, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *InvitationResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	// All settable attributes are RequiresReplace, so no in-place
	// update is reachable. This stub keeps the interface satisfied.
	resp.Diagnostics.AddError(
		"Invitation update not supported",
		"Invitations are immutable in Pangolin. Changing any attribute should trigger a destroy + create - file a bug if you see this error.",
	)
}

func (r *InvitationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state InvitationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteInvitation(ctx, r.client.OrgID, state.ID.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			// Already gone - treat as success.
			return
		}
		resp.Diagnostics.AddError("Failed to delete invitation", err.Error())
		return
	}
}

func (r *InvitationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	invite, err := r.client.GetInvitation(ctx, r.client.OrgID, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import invitation", err.Error())
		return
	}

	roleID := int64(0)
	if len(invite.Roles) > 0 {
		roleID = int64(invite.Roles[0].RoleID)
	}

	state := InvitationResourceModel{
		ID:         types.StringValue(invite.InviteID),
		Email:      types.StringValue(invite.Email),
		RoleID:     types.Int64Value(roleID),
		ValidHours: types.Int64Null(),
		SendEmail:  types.BoolValue(false),
		Regenerate: types.BoolValue(false),
		// invite_link is not returned by GET - left null on import; the
		// user will not be able to re-send the link from this state.
		InviteLink: types.StringNull(),
		ExpiresAt:  types.Int64Value(invite.ExpiresAt),
		Roles:      invitationRoleIDsToList(ctx, invite.Roles, &resp.Diagnostics),
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// invitationRoleIDsToList projects the nested role bindings down to a
// flat list of role IDs, which is what the resource exposes.
func invitationRoleIDsToList(ctx context.Context, roles []client.InviteRole, diags *diag.Diagnostics) types.List {
	ids := make([]int64, len(roles))
	for i, r := range roles {
		ids[i] = int64(r.RoleID)
	}
	list, d := types.ListValueFrom(ctx, types.Int64Type, ids)
	diags.Append(d...)
	return list
}
