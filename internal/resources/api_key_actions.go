package resources

import (
	"context"
	"errors"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &APIKeyActionsResource{}
	_ resource.ResourceWithImportState = &APIKeyActionsResource{}
)

// APIKeyActionsResource manages the set of actions granted to a
// Pangolin API key. There is at most one instance per `api_key_id`;
// the resource owns the full set replacement semantics of the
// upstream `POST /org/{org}/api-key/{id}/actions` endpoint.
type APIKeyActionsResource struct {
	client *client.Client
}

// APIKeyActionsModel is the TF state for a per-key action set.
type APIKeyActionsModel struct {
	APIKeyID types.String `tfsdk:"api_key_id"`
	Actions  types.Set    `tfsdk:"actions"`
}

// NewAPIKeyActionsResource returns a new resource factory.
func NewAPIKeyActionsResource() resource.Resource {
	return &APIKeyActionsResource{}
}

func (r *APIKeyActionsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key_actions"
}

func (r *APIKeyActionsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Replaces the set of actions (permissions) granted to a Pangolin API key.\n\n" +
			"The set of valid action IDs is a closed, server-defined enum of camelCase " +
			"operation names (e.g. `getOrg`, `listSites`, `createResource`). The catalog " +
			"is not introspectable from the OpenAPI spec; supplying an unknown ID returns " +
			"a 400 from the upstream API.\n\n" +
			"> **Note:** the upstream rejects empty sets. To clear all actions, delete " +
			"the parent API key instead. `terraform destroy` on this resource only removes " +
			"it from state — the actions remain bound to the key until the key itself is " +
			"deleted (typically by the parent `pangolin_api_key` resource).",
		Attributes: map[string]schema.Attribute{
			"api_key_id": schema.StringAttribute{
				Description: "The ID of the API key whose actions are being managed.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"actions": schema.SetAttribute{
				Description: "The set of action IDs to grant. Must be non-empty (the upstream " +
					"rejects an empty set with a 400 validation error).",
				Required:    true,
				ElementType: types.StringType,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
				},
			},
		},
	}
}

func (r *APIKeyActionsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// setToStringSlice extracts the elements of a types.Set into a Go
// []string, sorted deterministically. Sorting keeps wire bodies
// stable in tests and avoids spurious diffs when the upstream
// reorders.
func setToStringSlice(ctx context.Context, s types.Set) ([]string, error) {
	var out []string
	if s.IsNull() || s.IsUnknown() {
		return out, nil
	}
	diags := s.ElementsAs(ctx, &out, false)
	if diags.HasError() {
		return nil, errors.New(diags.Errors()[0].Detail())
	}
	sort.Strings(out)
	return out, nil
}

func (r *APIKeyActionsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan APIKeyActionsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	actions, err := setToStringSlice(ctx, plan.Actions)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read actions set", err.Error())
		return
	}

	if err := r.client.SetAPIKeyActions(ctx, plan.APIKeyID.ValueString(), actions); err != nil {
		resp.Diagnostics.AddError("Failed to set API key actions", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *APIKeyActionsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state APIKeyActionsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	actions, err := r.client.ListAPIKeyActions(ctx, state.APIKeyID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read API key actions", err.Error())
		return
	}

	values := make([]attr.Value, 0, len(actions))
	for _, a := range actions {
		values = append(values, types.StringValue(a.ActionID))
	}
	set, diags := types.SetValue(types.StringType, values)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.Actions = set

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *APIKeyActionsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan APIKeyActionsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	actions, err := setToStringSlice(ctx, plan.Actions)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read actions set", err.Error())
		return
	}

	if err := r.client.SetAPIKeyActions(ctx, plan.APIKeyID.ValueString(), actions); err != nil {
		resp.Diagnostics.AddError("Failed to update API key actions", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the resource from state without touching the
// upstream. The empty-set rejection on the set endpoint makes a
// "clear" call impossible; the actions naturally die with the parent
// API key.
func (r *APIKeyActionsResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *APIKeyActionsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: "{api_key_id}". Read populates the actions set.
	actions, err := r.client.ListAPIKeyActions(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import API key actions", err.Error())
		return
	}

	values := make([]attr.Value, 0, len(actions))
	for _, a := range actions {
		values = append(values, types.StringValue(a.ActionID))
	}
	set, diags := types.SetValue(types.StringType, values)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &APIKeyActionsModel{
		APIKeyID: types.StringValue(req.ID),
		Actions:  set,
	})...)
}
