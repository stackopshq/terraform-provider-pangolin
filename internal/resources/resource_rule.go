package resources

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &ResourceRuleResource{}
	_ resource.ResourceWithImportState = &ResourceRuleResource{}
)

// ResourceRuleResource manages an access control rule for a Pangolin HTTP resource.
type ResourceRuleResource struct {
	client *client.Client
}

// ResourceRuleModel describes the resource data model.
type ResourceRuleModel struct {
	ID         types.Int64  `tfsdk:"id"`
	ResourceID types.Int64  `tfsdk:"resource_id"`
	Action     types.String `tfsdk:"action"`
	Match      types.String `tfsdk:"match"`
	Value      types.String `tfsdk:"value"`
	Priority   types.Int64  `tfsdk:"priority"`
	Enabled    types.Bool   `tfsdk:"enabled"`
}

// NewResourceRuleResource returns a new resource factory.
func NewResourceRuleResource() resource.Resource {
	return &ResourceRuleResource{}
}

func (r *ResourceRuleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource_rule"
}

func (r *ResourceRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an access control rule for a Pangolin HTTP resource.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "The numeric rule ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"resource_id": schema.Int64Attribute{
				Description: "The ID of the resource this rule belongs to.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"action": schema.StringAttribute{
				Description: "The rule action: ACCEPT, DROP, or PASS.",
				Required:    true,
			},
			"match": schema.StringAttribute{
				Description: "The match type: CIDR, IP, PATH, COUNTRY, or ASN.",
				Required:    true,
			},
			"value": schema.StringAttribute{
				Description: "The value to match against (e.g. CIDR range, IP, path prefix, country code, ASN).",
				Required:    true,
			},
			"priority": schema.Int64Attribute{
				Description: "The rule priority (lower number = higher priority).",
				Required:    true,
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether the rule is enabled. Defaults to true.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
		},
	}
}

func (r *ResourceRuleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ResourceRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ResourceRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := r.client.CreateResourceRule(ctx, int(plan.ResourceID.ValueInt64()), &client.SetResourceRuleRequest{
		Action:   plan.Action.ValueString(),
		Match:    plan.Match.ValueString(),
		Value:    plan.Value.ValueString(),
		Priority: int(plan.Priority.ValueInt64()),
		Enabled:  plan.Enabled.ValueBool(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to create resource rule", err.Error())
		return
	}

	plan.ID = types.Int64Value(int64(rule.RuleID))
	plan.Action = types.StringValue(rule.Action)
	plan.Match = types.StringValue(rule.Match)
	plan.Value = types.StringValue(rule.Value)
	plan.Priority = types.Int64Value(int64(rule.Priority))
	plan.Enabled = types.BoolValue(rule.Enabled)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ResourceRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ResourceRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := r.client.GetResourceRule(ctx, int(state.ResourceID.ValueInt64()), int(state.ID.ValueInt64()))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read resource rule", err.Error())
		return
	}

	state.Action = types.StringValue(rule.Action)
	state.Match = types.StringValue(rule.Match)
	state.Value = types.StringValue(rule.Value)
	state.Priority = types.Int64Value(int64(rule.Priority))
	state.Enabled = types.BoolValue(rule.Enabled)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ResourceRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ResourceRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := r.client.UpdateResourceRule(ctx, int(plan.ResourceID.ValueInt64()), int(plan.ID.ValueInt64()), &client.SetResourceRuleRequest{
		Action:   plan.Action.ValueString(),
		Match:    plan.Match.ValueString(),
		Value:    plan.Value.ValueString(),
		Priority: int(plan.Priority.ValueInt64()),
		Enabled:  plan.Enabled.ValueBool(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to update resource rule", err.Error())
		return
	}

	plan.Action = types.StringValue(rule.Action)
	plan.Match = types.StringValue(rule.Match)
	plan.Value = types.StringValue(rule.Value)
	plan.Priority = types.Int64Value(int64(rule.Priority))
	plan.Enabled = types.BoolValue(rule.Enabled)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ResourceRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ResourceRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteResourceRule(ctx, int(state.ResourceID.ValueInt64()), int(state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to delete resource rule", err.Error())
		return
	}
}

func (r *ResourceRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: "{resource_id}/{rule_id}"
	idx := strings.Index(req.ID, "/")
	if idx < 0 {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected format: {resource_id}/{rule_id}, got: %q", req.ID))
		return
	}

	resourceIDStr := req.ID[:idx]
	ruleIDStr := req.ID[idx+1:]

	resourceID, err := strconv.ParseInt(resourceIDStr, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid resource ID", fmt.Sprintf("Cannot parse %q as integer", resourceIDStr))
		return
	}

	ruleID, err := strconv.ParseInt(ruleIDStr, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid rule ID", fmt.Sprintf("Cannot parse %q as integer", ruleIDStr))
		return
	}

	rule, err := r.client.GetResourceRule(ctx, int(resourceID), int(ruleID))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import resource rule", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &ResourceRuleModel{
		ID:         types.Int64Value(int64(rule.RuleID)),
		ResourceID: types.Int64Value(int64(rule.ResourceID)),
		Action:     types.StringValue(rule.Action),
		Match:      types.StringValue(rule.Match),
		Value:      types.StringValue(rule.Value),
		Priority:   types.Int64Value(int64(rule.Priority)),
		Enabled:    types.BoolValue(rule.Enabled),
	})...)
}
