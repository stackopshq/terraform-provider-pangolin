package resources

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &ResourcePasswordResource{}
	_ resource.ResourceWithImportState = &ResourcePasswordResource{}
)

// ResourcePasswordResource manages password authentication for a Pangolin HTTP resource.
type ResourcePasswordResource struct {
	client *client.Client
}

// ResourcePasswordModel describes the resource data model.
type ResourcePasswordModel struct {
	ResourceID types.Int64  `tfsdk:"resource_id"`
	Password   types.String `tfsdk:"password"`
}

// NewResourcePasswordResource returns a new resource factory.
func NewResourcePasswordResource() resource.Resource {
	return &ResourcePasswordResource{}
}

func (r *ResourcePasswordResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource_password"
}

func (r *ResourcePasswordResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Sets a password for accessing a Pangolin HTTP resource. Destroying this resource removes the password.",
		Attributes: map[string]schema.Attribute{
			"resource_id": schema.Int64Attribute{
				Description: "The ID of the resource to protect with a password.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"password": schema.StringAttribute{
				Description: "The password required to access the resource.",
				Required:    true,
				Sensitive:   true,
			},
		},
	}
}

func (r *ResourcePasswordResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ResourcePasswordResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ResourcePasswordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pw := plan.Password.ValueString()
	if err := r.client.SetResourcePassword(int(plan.ResourceID.ValueInt64()), &pw); err != nil {
		resp.Diagnostics.AddError("Failed to set resource password", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ResourcePasswordResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ResourcePasswordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	authState, err := r.client.GetResourceAuthState(int(state.ResourceID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to read resource auth state", err.Error())
		return
	}

	if authState.PasswordID == nil {
		// Password was removed externally — remove from state.
		resp.State.RemoveResource(ctx)
		return
	}

	// Credentials cannot be read back from the API; preserve existing state.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ResourcePasswordResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ResourcePasswordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pw := plan.Password.ValueString()
	if err := r.client.SetResourcePassword(int(plan.ResourceID.ValueInt64()), &pw); err != nil {
		resp.Diagnostics.AddError("Failed to update resource password", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ResourcePasswordResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ResourcePasswordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.SetResourcePassword(int(state.ResourceID.ValueInt64()), nil); err != nil {
		resp.Diagnostics.AddError("Failed to remove resource password", err.Error())
		return
	}
}

func (r *ResourcePasswordResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resourceID, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ID", fmt.Sprintf("Cannot parse resource ID %q as integer", req.ID))
		return
	}

	authState, err := r.client.GetResourceAuthState(int(resourceID))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import resource password", err.Error())
		return
	}

	if authState.PasswordID == nil {
		resp.Diagnostics.AddError("No password set", fmt.Sprintf("Resource %d does not have a password configured", resourceID))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &ResourcePasswordModel{
		ResourceID: types.Int64Value(resourceID),
		Password:   types.StringValue(""), // not recoverable after import
	})...)
}
