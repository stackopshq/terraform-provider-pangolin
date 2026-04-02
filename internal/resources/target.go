package resources

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &TargetResource{}
	_ resource.ResourceWithImportState = &TargetResource{}
)

// TargetResource defines the resource implementation.
type TargetResource struct {
	client *client.Client
}

// TargetResourceModel describes the resource data model.
type TargetResourceModel struct {
	ID         types.Int64  `tfsdk:"id"`
	ResourceID types.Int64  `tfsdk:"resource_id"`
	SiteID     types.Int64  `tfsdk:"site_id"`
	IP         types.String `tfsdk:"ip"`
	Port       types.Int64  `tfsdk:"port"`
	Method     types.String `tfsdk:"method"`
	Enabled    types.Bool   `tfsdk:"enabled"`
}

// NewTargetResource returns a new resource factory.
func NewTargetResource() resource.Resource {
	return &TargetResource{}
}

func (r *TargetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_target"
}

func (r *TargetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Pangolin target (backend endpoint for an HTTP resource).",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "The numeric ID of the target.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"resource_id": schema.Int64Attribute{
				Description: "The ID of the HTTP resource this target belongs to.",
				Required:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"site_id": schema.Int64Attribute{
				Description: "The ID of the site that serves this target.",
				Required:    true,
			},
			"ip": schema.StringAttribute{
				Description: "The IP address or hostname of the target (e.g. 'localhost', '10.0.0.1').",
				Required:    true,
			},
			"port": schema.Int64Attribute{
				Description: "The port of the target.",
				Required:    true,
			},
			"method": schema.StringAttribute{
				Description: "The method (http or https). Defaults to http.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("http"),
			},
			"enabled": schema.BoolAttribute{
				Description: "Enable or disable this target. Defaults to true.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
		},
	}
}

func (r *TargetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TargetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TargetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	target, err := r.client.CreateTarget(int(plan.ResourceID.ValueInt64()), &client.CreateTargetRequest{
		IP:     plan.IP.ValueString(),
		Port:   int(plan.Port.ValueInt64()),
		Method: plan.Method.ValueString(),
		SiteID: int(plan.SiteID.ValueInt64()),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to create target", err.Error())
		return
	}

	plan.ID = types.Int64Value(int64(target.TargetID))
	plan.Enabled = types.BoolValue(target.Enabled)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TargetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TargetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	target, err := r.client.GetTarget(int(state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to read target", err.Error())
		return
	}

	state.ResourceID = types.Int64Value(int64(target.ResourceID))
	state.SiteID = types.Int64Value(int64(target.SiteID))
	state.IP = types.StringValue(target.IP)
	state.Port = types.Int64Value(int64(target.Port))
	state.Method = types.StringValue(target.Method)
	state.Enabled = types.BoolValue(target.Enabled)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *TargetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TargetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	target, err := r.client.UpdateTarget(int(plan.ID.ValueInt64()), &client.UpdateTargetRequest{
		IP:      plan.IP.ValueString(),
		Port:    int(plan.Port.ValueInt64()),
		Method:  plan.Method.ValueString(),
		Enabled: plan.Enabled.ValueBool(),
		SiteID:  int(plan.SiteID.ValueInt64()),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to update target", err.Error())
		return
	}

	plan.SiteID = types.Int64Value(int64(target.SiteID))
	plan.IP = types.StringValue(target.IP)
	plan.Port = types.Int64Value(int64(target.Port))
	plan.Method = types.StringValue(target.Method)
	plan.Enabled = types.BoolValue(target.Enabled)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TargetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TargetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteTarget(int(state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to delete target", err.Error())
		return
	}
}

func (r *TargetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ID", fmt.Sprintf("Cannot parse target ID %q as integer", req.ID))
		return
	}

	target, err := r.client.GetTarget(int(id))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import target", err.Error())
		return
	}

	state := TargetResourceModel{
		ID:         types.Int64Value(int64(target.TargetID)),
		ResourceID: types.Int64Value(int64(target.ResourceID)),
		SiteID:     types.Int64Value(int64(target.SiteID)),
		IP:         types.StringValue(target.IP),
		Port:       types.Int64Value(int64(target.Port)),
		Method:     types.StringValue(target.Method),
		Enabled:    types.BoolValue(target.Enabled),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
