package resources

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &IDPResource{}
	_ resource.ResourceWithImportState = &IDPResource{}
)

// IDPResource manages a Pangolin OIDC Identity Provider.
type IDPResource struct {
	client *client.Client
}

// IDPResourceModel describes the resource data model.
type IDPResourceModel struct {
	ID             types.Int64  `tfsdk:"id"`
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
	RedirectURL    types.String `tfsdk:"redirect_url"`
}

// NewIDPResource returns a new resource factory.
func NewIDPResource() resource.Resource {
	return &IDPResource{}
}

func (r *IDPResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_idp"
}

func (r *IDPResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Pangolin OIDC Identity Provider.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "The numeric IDP ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The display name of the IDP.",
				Required:    true,
			},
			"client_id": schema.StringAttribute{
				Description: "The OIDC client ID.",
				Required:    true,
			},
			"client_secret": schema.StringAttribute{
				Description: "The OIDC client secret.",
				Required:    true,
				Sensitive:   true,
			},
			"auth_url": schema.StringAttribute{
				Description: "The OIDC authorization URL.",
				Required:    true,
			},
			"token_url": schema.StringAttribute{
				Description: "The OIDC token URL.",
				Required:    true,
			},
			"identifier_path": schema.StringAttribute{
				Description: "The path in the ID token to use as the user identifier (e.g. 'sub').",
				Required:    true,
			},
			"scopes": schema.StringAttribute{
				Description: "Space-separated OIDC scopes (e.g. 'openid email profile').",
				Required:    true,
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
				Description: "Whether to auto-provision users on first login. Defaults to false.",
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
			"redirect_url": schema.StringAttribute{
				Description: "The OAuth callback URL to configure in your OIDC provider (computed).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *IDPResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *IDPResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan IDPResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	result, err := r.client.CreateIDP(&client.CreateIDPRequest{
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
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to create IDP", err.Error())
		return
	}

	plan.ID = types.Int64Value(int64(result.IDPId))
	plan.RedirectURL = types.StringValue(result.RedirectURL)

	// Populate all computed fields from the API response.
	idp, oidcCfg, err := r.client.GetIDP(result.IDPId)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read IDP after create", err.Error())
		return
	}
	plan.Name = types.StringValue(idp.Name)
	plan.AutoProvision = types.BoolValue(idp.AutoProvision)
	plan.Tags = types.StringValue(idp.Tags)
	plan.EmailPath = types.StringValue(oidcCfg.EmailPath)
	plan.NamePath = types.StringValue(oidcCfg.NamePath)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *IDPResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state IDPResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	idp, oidcCfg, err := r.client.GetIDP(int(state.ID.ValueInt64()))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read IDP", err.Error())
		return
	}

	state.Name = types.StringValue(idp.Name)
	state.AutoProvision = types.BoolValue(idp.AutoProvision)
	state.Tags = types.StringValue(idp.Tags)
	state.ClientID = types.StringValue(oidcCfg.ClientID)
	// ClientSecret is not returned masked from API; preserve existing state value.
	state.AuthURL = types.StringValue(oidcCfg.AuthURL)
	state.TokenURL = types.StringValue(oidcCfg.TokenURL)
	state.IdentifierPath = types.StringValue(oidcCfg.IdentifierPath)
	state.EmailPath = types.StringValue(oidcCfg.EmailPath)
	state.NamePath = types.StringValue(oidcCfg.NamePath)
	state.Scopes = types.StringValue(oidcCfg.Scopes)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *IDPResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan IDPResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.UpdateIDP(int(plan.ID.ValueInt64()), &client.UpdateIDPRequest{
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
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to update IDP", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *IDPResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state IDPResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteIDP(int(state.ID.ValueInt64()))
	if err != nil {
		// DELETE /idp/{id} is not available on the Integration API — only on the internal admin API.
		// Emit a warning and remove from state so Terraform does not block the user.
		resp.Diagnostics.AddWarning(
			"IDP deletion not supported via Integration API",
			fmt.Sprintf("The Pangolin Integration API does not expose DELETE /idp/{id}. "+
				"The IDP (id=%d) has been removed from Terraform state but may still exist on the server. "+
				"Delete it manually via the Pangolin admin interface. Error: %s", state.ID.ValueInt64(), err.Error()),
		)
	}
}

func (r *IDPResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	idpID, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ID", fmt.Sprintf("Cannot parse IDP ID %q as integer", req.ID))
		return
	}

	idp, oidcCfg, err := r.client.GetIDP(int(idpID))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import IDP", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &IDPResourceModel{
		ID:             types.Int64Value(int64(idp.IDPId)),
		Name:           types.StringValue(idp.Name),
		AutoProvision:  types.BoolValue(idp.AutoProvision),
		Tags:           types.StringValue(idp.Tags),
		ClientID:       types.StringValue(oidcCfg.ClientID),
		ClientSecret:   types.StringValue(""), // not recoverable after import
		AuthURL:        types.StringValue(oidcCfg.AuthURL),
		TokenURL:       types.StringValue(oidcCfg.TokenURL),
		IdentifierPath: types.StringValue(oidcCfg.IdentifierPath),
		EmailPath:      types.StringValue(oidcCfg.EmailPath),
		NamePath:       types.StringValue(oidcCfg.NamePath),
		Scopes:         types.StringValue(oidcCfg.Scopes),
		RedirectURL:    types.StringValue(""), // not returned by GET
	})...)
}
