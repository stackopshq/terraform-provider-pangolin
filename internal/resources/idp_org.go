package resources

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &IDPOrgResource{}
	_ resource.ResourceWithImportState = &IDPOrgResource{}
)

// IDPOrgResource manages the association of an IDP with an organization.
type IDPOrgResource struct {
	client *client.Client
}

// IDPOrgModel describes the resource data model.
type IDPOrgModel struct {
	IDPId       types.Int64  `tfsdk:"idp_id"`
	OrgID       types.String `tfsdk:"org_id"`
	RoleMapping types.String `tfsdk:"role_mapping"`
	OrgMapping  types.String `tfsdk:"org_mapping"`
}

// NewIDPOrgResource returns a new resource factory.
func NewIDPOrgResource() resource.Resource {
	return &IDPOrgResource{}
}

func (r *IDPOrgResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_idp_org"
}

func (r *IDPOrgResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Associates a Pangolin IDP with an organization and defines the role/org mapping policy.",
		Attributes: map[string]schema.Attribute{
			"idp_id": schema.Int64Attribute{
				Description: "The ID of the IDP.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"org_id": schema.StringAttribute{
				Description: "The ID of the organization.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"role_mapping": schema.StringAttribute{
				Description: "Expression mapping IDP groups/claims to Pangolin roles.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"org_mapping": schema.StringAttribute{
				Description: "Expression mapping IDP claims to Pangolin org membership.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *IDPOrgResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *IDPOrgResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan IDPOrgModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.CreateIDPOrgPolicy(int(plan.IDPId.ValueInt64()), plan.OrgID.ValueString(), &client.SetIDPOrgPolicyRequest{
		RoleMapping: plan.RoleMapping.ValueString(),
		OrgMapping:  plan.OrgMapping.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to create IDP org policy", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *IDPOrgResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state IDPOrgModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policy, err := r.client.GetIDPOrgPolicy(int(state.IDPId.ValueInt64()), state.OrgID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read IDP org policy", err.Error())
		return
	}

	state.RoleMapping = types.StringValue(policy.RoleMapping)
	state.OrgMapping = types.StringValue(policy.OrgMapping)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *IDPOrgResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan IDPOrgModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.UpdateIDPOrgPolicy(int(plan.IDPId.ValueInt64()), plan.OrgID.ValueString(), &client.SetIDPOrgPolicyRequest{
		RoleMapping: plan.RoleMapping.ValueString(),
		OrgMapping:  plan.OrgMapping.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to update IDP org policy", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *IDPOrgResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state IDPOrgModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteIDPOrgPolicy(int(state.IDPId.ValueInt64()), state.OrgID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to delete IDP org policy", err.Error())
		return
	}
}

func (r *IDPOrgResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: "{idp_id}/{org_id}"
	idx := strings.Index(req.ID, "/")
	if idx < 0 {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected format: {idp_id}/{org_id}, got: %q", req.ID))
		return
	}

	idpIDStr := req.ID[:idx]
	orgID := req.ID[idx+1:]

	idpID, err := strconv.ParseInt(idpIDStr, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid IDP ID", fmt.Sprintf("Cannot parse %q as integer", idpIDStr))
		return
	}

	policy, err := r.client.GetIDPOrgPolicy(int(idpID), orgID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import IDP org policy", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &IDPOrgModel{
		IDPId:       types.Int64Value(int64(policy.IDPId)),
		OrgID:       types.StringValue(policy.OrgID),
		RoleMapping: types.StringValue(policy.RoleMapping),
		OrgMapping:  types.StringValue(policy.OrgMapping),
	})...)
}
