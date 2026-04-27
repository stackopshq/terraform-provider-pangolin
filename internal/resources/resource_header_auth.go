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
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &ResourceHeaderAuthResource{}
	_ resource.ResourceWithImportState = &ResourceHeaderAuthResource{}
)

// ResourceHeaderAuthResource manages header-based authentication for a Pangolin HTTP resource.
type ResourceHeaderAuthResource struct {
	client *client.Client
}

// ResourceHeaderAuthModel describes the resource data model.
type ResourceHeaderAuthModel struct {
	ResourceID            types.Int64  `tfsdk:"resource_id"`
	Password              types.String `tfsdk:"password"`
	User                  types.String `tfsdk:"user"`
	ExtendedCompatibility types.Bool   `tfsdk:"extended_compatibility"`
}

// NewResourceHeaderAuthResource returns a new resource factory.
func NewResourceHeaderAuthResource() resource.Resource {
	return &ResourceHeaderAuthResource{}
}

func (r *ResourceHeaderAuthResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource_header_auth"
}

func (r *ResourceHeaderAuthResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Sets header-based authentication for a Pangolin HTTP resource. Destroying this resource removes the header auth.",
		Attributes: map[string]schema.Attribute{
			"resource_id": schema.Int64Attribute{
				Description: "The ID of the resource to protect with header authentication.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"password": schema.StringAttribute{
				Description: "The password for header authentication.",
				Required:    true,
				Sensitive:   true,
			},
			"user": schema.StringAttribute{
				Description: "The username for header authentication.",
				Required:    true,
			},
			"extended_compatibility": schema.BoolAttribute{
				Description: "Whether to enable extended compatibility mode. Defaults to false.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
		},
	}
}

func (r *ResourceHeaderAuthResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ResourceHeaderAuthResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ResourceHeaderAuthModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pw := plan.Password.ValueString()
	user := plan.User.ValueString()
	if err := r.client.SetResourceHeaderAuth(int(plan.ResourceID.ValueInt64()), &client.SetResourceHeaderAuthRequest{
		Password:              &pw,
		User:                  &user,
		ExtendedCompatibility: plan.ExtendedCompatibility.ValueBool(),
	}); err != nil {
		resp.Diagnostics.AddError("Failed to set resource header auth", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ResourceHeaderAuthResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ResourceHeaderAuthModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	authState, err := r.client.GetResourceAuthState(int(state.ResourceID.ValueInt64()))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read resource auth state", err.Error())
		return
	}

	if authState.HeaderAuthID == nil {
		// Header auth was removed externally — remove from state.
		resp.State.RemoveResource(ctx)
		return
	}

	// Credentials cannot be read back from the API; preserve existing state.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ResourceHeaderAuthResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ResourceHeaderAuthModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pw := plan.Password.ValueString()
	user := plan.User.ValueString()
	if err := r.client.SetResourceHeaderAuth(int(plan.ResourceID.ValueInt64()), &client.SetResourceHeaderAuthRequest{
		Password:              &pw,
		User:                  &user,
		ExtendedCompatibility: plan.ExtendedCompatibility.ValueBool(),
	}); err != nil {
		resp.Diagnostics.AddError("Failed to update resource header auth", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ResourceHeaderAuthResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ResourceHeaderAuthModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.SetResourceHeaderAuth(int(state.ResourceID.ValueInt64()), &client.SetResourceHeaderAuthRequest{
		Password:              nil,
		User:                  nil,
		ExtendedCompatibility: false,
	}); err != nil {
		resp.Diagnostics.AddError("Failed to remove resource header auth", err.Error())
		return
	}
}

func (r *ResourceHeaderAuthResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resourceID, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ID", fmt.Sprintf("Cannot parse resource ID %q as integer", req.ID))
		return
	}

	authState, err := r.client.GetResourceAuthState(int(resourceID))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import resource header auth", err.Error())
		return
	}

	if authState.HeaderAuthID == nil {
		resp.Diagnostics.AddError("No header auth set", fmt.Sprintf("Resource %d does not have header authentication configured", resourceID))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &ResourceHeaderAuthModel{
		ResourceID:            types.Int64Value(resourceID),
		Password:              types.StringValue(""), // not recoverable after import
		User:                  types.StringValue(""), // not recoverable after import
		ExtendedCompatibility: types.BoolValue(false),
	})...)
}
