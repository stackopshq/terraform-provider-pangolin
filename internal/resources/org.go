package resources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
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
		Description: "Manages a Pangolin organization. Only available on self-hosted (OSS/Enterprise) deployments.",
		Attributes: map[string]schema.Attribute{
			"org_id": schema.StringAttribute{
				Description: "The organization ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The name of the organization.",
				Required:    true,
			},
			"subnet": schema.StringAttribute{
				Description: "The CIDR subnet allocated to the organization (e.g. '100.90.0.0/24').",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"utility_subnet": schema.StringAttribute{
				Description: "The utility CIDR subnet allocated to the organization (e.g. '100.96.0.0/24').",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
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

	org, err := r.client.CreateOrg(&client.CreateOrgRequest{
		OrgID:         plan.OrgID.ValueString(),
		Name:          plan.Name.ValueString(),
		Subnet:        plan.Subnet.ValueString(),
		UtilitySubnet: plan.UtilitySubnet.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to create org", err.Error())
		return
	}

	plan.OrgID = types.StringValue(org.OrgID)
	plan.Name = types.StringValue(org.Name)
	plan.Subnet = types.StringValue(org.Subnet)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *OrgResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state OrgResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	org, err := r.client.GetOrg(state.OrgID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read org", err.Error())
		return
	}

	state.Name = types.StringValue(org.Name)
	state.Subnet = types.StringValue(org.Subnet)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *OrgResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan OrgResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	org, err := r.client.UpdateOrg(plan.OrgID.ValueString(), &client.UpdateOrgRequest{
		Name: plan.Name.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to update org", err.Error())
		return
	}

	plan.Name = types.StringValue(org.Name)
	plan.Subnet = types.StringValue(org.Subnet)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *OrgResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state OrgResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteOrg(state.OrgID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to delete org", err.Error())
		return
	}
}

func (r *OrgResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	org, err := r.client.GetOrg(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import org", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &OrgResourceModel{
		OrgID:  types.StringValue(org.OrgID),
		Name:   types.StringValue(org.Name),
		Subnet: types.StringValue(org.Subnet),
	})...)
}
