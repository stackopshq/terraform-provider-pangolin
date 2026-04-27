package resources

import (
	"context"
	"errors"
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
	_ resource.Resource                = &ResourcePincodeResource{}
	_ resource.ResourceWithImportState = &ResourcePincodeResource{}
)

// ResourcePincodeResource manages PIN code authentication for a Pangolin HTTP resource.
type ResourcePincodeResource struct {
	client *client.Client
}

// ResourcePincodeModel describes the resource data model.
type ResourcePincodeModel struct {
	ResourceID types.Int64  `tfsdk:"resource_id"`
	Pincode    types.String `tfsdk:"pincode"`
}

// NewResourcePincodeResource returns a new resource factory.
func NewResourcePincodeResource() resource.Resource {
	return &ResourcePincodeResource{}
}

func (r *ResourcePincodeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource_pincode"
}

func (r *ResourcePincodeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Sets a PIN code for accessing a Pangolin HTTP resource. Destroying this resource removes the PIN code.",
		Attributes: map[string]schema.Attribute{
			"resource_id": schema.Int64Attribute{
				Description: "The ID of the resource to protect with a PIN code.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"pincode": schema.StringAttribute{
				Description: "The PIN code (numeric string, typically 6 digits) required to access the resource.",
				Required:    true,
				Sensitive:   true,
			},
		},
	}
}

func (r *ResourcePincodeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ResourcePincodeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ResourcePincodeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pin := plan.Pincode.ValueString()
	if err := r.client.SetResourcePincode(ctx, int(plan.ResourceID.ValueInt64()), &pin); err != nil {
		resp.Diagnostics.AddError("Failed to set resource PIN code", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ResourcePincodeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ResourcePincodeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	authState, err := r.client.GetResourceAuthState(ctx, int(state.ResourceID.ValueInt64()))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read resource auth state", err.Error())
		return
	}

	if authState.PincodeID == nil {
		// PIN code was removed externally — remove from state.
		resp.State.RemoveResource(ctx)
		return
	}

	// Credentials cannot be read back from the API; preserve existing state.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ResourcePincodeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ResourcePincodeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pin := plan.Pincode.ValueString()
	if err := r.client.SetResourcePincode(ctx, int(plan.ResourceID.ValueInt64()), &pin); err != nil {
		resp.Diagnostics.AddError("Failed to update resource PIN code", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ResourcePincodeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ResourcePincodeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.SetResourcePincode(ctx, int(state.ResourceID.ValueInt64()), nil); err != nil {
		resp.Diagnostics.AddError("Failed to remove resource PIN code", err.Error())
		return
	}
}

func (r *ResourcePincodeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resourceID, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ID", fmt.Sprintf("Cannot parse resource ID %q as integer", req.ID))
		return
	}

	authState, err := r.client.GetResourceAuthState(ctx, int(resourceID))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import resource PIN code", err.Error())
		return
	}

	if authState.PincodeID == nil {
		resp.Diagnostics.AddError("No PIN code set", fmt.Sprintf("Resource %d does not have a PIN code configured", resourceID))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &ResourcePincodeModel{
		ResourceID: types.Int64Value(resourceID),
		Pincode:    types.StringValue(""), // not recoverable after import
	})...)
}
