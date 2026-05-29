package resources

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
)

var (
	_ resource.Resource                = &OrgIDPResource{}
	_ resource.ResourceWithImportState = &OrgIDPResource{}
)

// OrgIDPResource manages an OIDC Identity Provider scoped to a single
// Pangolin organization. Functionally equivalent to combining
// `pangolin_idp` + `pangolin_idp_org` in one declaration:
// the upstream `/org/{org}/idp/oidc` routes auto-bind the new IDP to
// the calling org, removing the separate `pangolin_idp_org` step.
type OrgIDPResource struct {
	client *client.Client
}

// OrgIDPResourceModel is the TF state for an org-scoped IDP.
type OrgIDPResourceModel struct {
	ID             types.Int64  `tfsdk:"id"`
	OrgID          types.String `tfsdk:"org_id"`
	Name           types.String `tfsdk:"name"`
	ClientID       types.String `tfsdk:"client_id"`
	ClientSecret   types.String `tfsdk:"client_secret"`
	AuthURL        types.String `tfsdk:"auth_url"`
	TokenURL       types.String `tfsdk:"token_url"`
	IdentifierPath types.String `tfsdk:"identifier_path"`
	EmailPath      types.String `tfsdk:"email_path"`
	NamePath       types.String `tfsdk:"name_path"`
	Scopes         types.String `tfsdk:"scopes"`
	AutoProvision  types.Bool   `tfsdk:"auto_provision"`
	Tags           types.String `tfsdk:"tags"`
	Variant        types.String `tfsdk:"variant"`
	RoleMapping    types.String `tfsdk:"role_mapping"`
	OrgMapping     types.String `tfsdk:"org_mapping"`
	RedirectURL    types.String `tfsdk:"redirect_url"`
}

// NewOrgIDPResource returns a new resource factory.
func NewOrgIDPResource() resource.Resource {
	return &OrgIDPResource{}
}

func (r *OrgIDPResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_org_idp"
}

func (r *OrgIDPResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an OIDC Identity Provider scoped to a single Pangolin organization.\n\n" +
			"This is the single-resource equivalent of combining `pangolin_idp` (system-wide " +
			"create) + `pangolin_idp_org` (bind to an org): the upstream `/org/{org}/idp/oidc` " +
			"endpoint auto-binds the new IDP to the calling org at creation time, removing the " +
			"need for a separate binding resource.\n\n" +
			"> **Note:** `client_secret` cannot be recovered after import and must be set " +
			"manually in config to avoid spurious diffs.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "The numeric IDP ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"org_id": schema.StringAttribute{
				Description: "The ID of the organization this IDP is bound to.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The display name of the IDP.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"client_id": schema.StringAttribute{
				Description: "The OIDC client ID.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"client_secret": schema.StringAttribute{
				Description: "The OIDC client secret.",
				Required:    true,
				Sensitive:   true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"auth_url": schema.StringAttribute{
				Description: "The OIDC authorization URL.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"token_url": schema.StringAttribute{
				Description: "The OIDC token URL.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"identifier_path": schema.StringAttribute{
				Description: "The path in the ID token to use as the user identifier (e.g. `sub`).",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"scopes": schema.StringAttribute{
				Description: "Space-separated OIDC scopes (e.g. `openid email profile`).",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"email_path": schema.StringAttribute{
				Description: "The path in the ID token for the user email.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name_path": schema.StringAttribute{
				Description: "The path in the ID token for the user display name.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"auto_provision": schema.BoolAttribute{
				Description: "Whether to auto-provision users on first login. Defaults to `false`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"tags": schema.StringAttribute{
				Description: "Optional tags associated with the IDP.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"variant": schema.StringAttribute{
				Description: "OIDC variant — refines `type = oidc` to a provider family. One of `oidc` (generic, default), `google`, `azure`.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("oidc", "google", "azure"),
				},
			},
			"role_mapping": schema.StringAttribute{
				Description: "Optional expression mapping a token claim to a Pangolin role for this org. Null when unset.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"org_mapping": schema.StringAttribute{
				Description: "Optional expression mapping a token claim to the org binding. Null when unset.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"redirect_url": schema.StringAttribute{
				Description: "The OAuth callback URL to configure in your OIDC provider. Only returned on create — empty after import.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *OrgIDPResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *OrgIDPResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan OrgIDPResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	result, err := r.client.CreateOrgIDP(ctx, plan.OrgID.ValueString(), &client.CreateIDPRequest{
		Name:           plan.Name.ValueString(),
		ClientID:       plan.ClientID.ValueString(),
		ClientSecret:   plan.ClientSecret.ValueString(),
		AuthURL:        plan.AuthURL.ValueString(),
		TokenURL:       plan.TokenURL.ValueString(),
		IdentifierPath: plan.IdentifierPath.ValueString(),
		EmailPath:      plan.EmailPath.ValueString(),
		NamePath:       plan.NamePath.ValueString(),
		Scopes:         plan.Scopes.ValueString(),
		AutoProvision:  plan.AutoProvision.ValueBool(),
		Tags:           plan.Tags.ValueString(),
		Variant:        plan.Variant.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to create org IDP", err.Error())
		return
	}

	plan.ID = types.Int64Value(int64(result.IDPId))
	plan.RedirectURL = types.StringValue(result.RedirectURL)

	// Populate computed fields from the upstream detail. role_mapping
	// and org_mapping default to null unless the user set them.
	detail, err := r.client.GetOrgIDP(ctx, plan.OrgID.ValueString(), result.IDPId)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read org IDP after create", err.Error())
		return
	}
	hydrateOrgIDPState(&plan, detail)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *OrgIDPResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state OrgIDPResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	detail, err := r.client.GetOrgIDP(ctx, state.OrgID.ValueString(), int(state.ID.ValueInt64()))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read org IDP", err.Error())
		return
	}

	hydrateOrgIDPState(&state, detail)
	// client_secret is not recoverable from the API — preserve existing.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *OrgIDPResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan OrgIDPResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.UpdateOrgIDP(ctx, plan.OrgID.ValueString(), int(plan.ID.ValueInt64()), &client.UpdateIDPRequest{
		Name:           plan.Name.ValueString(),
		ClientID:       plan.ClientID.ValueString(),
		ClientSecret:   plan.ClientSecret.ValueString(),
		AuthURL:        plan.AuthURL.ValueString(),
		TokenURL:       plan.TokenURL.ValueString(),
		IdentifierPath: plan.IdentifierPath.ValueString(),
		EmailPath:      plan.EmailPath.ValueString(),
		NamePath:       plan.NamePath.ValueString(),
		Scopes:         plan.Scopes.ValueString(),
		AutoProvision:  plan.AutoProvision.ValueBool(),
		Tags:           plan.Tags.ValueString(),
		Variant:        plan.Variant.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to update org IDP", err.Error())
		return
	}

	// The POST response is `{idpId}` only — refetch for the full payload.
	detail, err := r.client.GetOrgIDP(ctx, plan.OrgID.ValueString(), int(plan.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to read org IDP after update", err.Error())
		return
	}
	hydrateOrgIDPState(&plan, detail)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *OrgIDPResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state OrgIDPResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteOrgIDP(ctx, state.OrgID.ValueString(), int(state.ID.ValueInt64())); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return
		}
		resp.Diagnostics.AddError("Failed to delete org IDP", err.Error())
		return
	}
}

func (r *OrgIDPResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: "{org_id}/{idp_id}"
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected format: {org_id}/{idp_id}, got: %q", req.ID))
		return
	}
	orgID := parts[0]
	idpID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid IDP ID", fmt.Sprintf("Cannot parse %q as integer", parts[1]))
		return
	}

	detail, err := r.client.GetOrgIDP(ctx, orgID, int(idpID))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import org IDP", err.Error())
		return
	}

	state := OrgIDPResourceModel{
		ID:           types.Int64Value(int64(detail.IDP.IDPId)),
		OrgID:        types.StringValue(orgID),
		ClientSecret: types.StringValue(""), // not recoverable after import
		RedirectURL:  types.StringValue(""), // not returned by GET
	}
	hydrateOrgIDPState(&state, detail)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// hydrateOrgIDPState copies every server-owned field from a freshly-
// fetched [client.OrgIDPDetail] into the TF state model. client_secret
// and redirect_url are caller-managed and left untouched.
func hydrateOrgIDPState(state *OrgIDPResourceModel, detail *client.OrgIDPDetail) {
	state.Name = types.StringValue(detail.IDP.Name)
	state.AutoProvision = types.BoolValue(detail.IDP.AutoProvision)
	state.Tags = types.StringValue(detail.IDP.Tags)
	state.Variant = types.StringValue(detail.IDP.Variant)
	state.ClientID = types.StringValue(detail.IDPOidcConfig.ClientID)
	state.AuthURL = types.StringValue(detail.IDPOidcConfig.AuthURL)
	state.TokenURL = types.StringValue(detail.IDPOidcConfig.TokenURL)
	state.IdentifierPath = types.StringValue(detail.IDPOidcConfig.IdentifierPath)
	state.EmailPath = types.StringValue(detail.IDPOidcConfig.EmailPath)
	state.NamePath = types.StringValue(detail.IDPOidcConfig.NamePath)
	state.Scopes = types.StringValue(detail.IDPOidcConfig.Scopes)
	state.RoleMapping = tfconv.StringFromPtr(detail.IDPOrg.RoleMapping)
	state.OrgMapping = tfconv.StringFromPtr(detail.IDPOrg.OrgMapping)
}
