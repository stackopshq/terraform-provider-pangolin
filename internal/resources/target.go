package resources

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
)

var (
	_ resource.Resource                = &TargetResource{}
	_ resource.ResourceWithImportState = &TargetResource{}
)

// TargetResource defines the resource implementation.
type TargetResource struct {
	client *client.Client
}

// TargetHCHeaderModel mirrors one health-check header for HCL input.
type TargetHCHeaderModel struct {
	Name  types.String `tfsdk:"name"`
	Value types.String `tfsdk:"value"`
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

	// Health-check configuration (all optional + computed)
	HCEnabled            types.Bool            `tfsdk:"hc_enabled"`
	HCPath               types.String          `tfsdk:"hc_path"`
	HCScheme             types.String          `tfsdk:"hc_scheme"`
	HCMode               types.String          `tfsdk:"hc_mode"`
	HCHostname           types.String          `tfsdk:"hc_hostname"`
	HCPort               types.Int64           `tfsdk:"hc_port"`
	HCInterval           types.Int64           `tfsdk:"hc_interval"`
	HCUnhealthyInterval  types.Int64           `tfsdk:"hc_unhealthy_interval"`
	HCTimeout            types.Int64           `tfsdk:"hc_timeout"`
	HCHeaders            []TargetHCHeaderModel `tfsdk:"hc_headers"`
	HCFollowRedirects    types.Bool            `tfsdk:"hc_follow_redirects"`
	HCMethod             types.String          `tfsdk:"hc_method"`
	HCStatus             types.Int64           `tfsdk:"hc_status"`
	HCTLSServerName      types.String          `tfsdk:"hc_tls_server_name"`
	HCHealthyThreshold   types.Int64           `tfsdk:"hc_healthy_threshold"`
	HCUnhealthyThreshold types.Int64           `tfsdk:"hc_unhealthy_threshold"`

	// Routing extras
	Path            types.String `tfsdk:"path"`
	PathMatchType   types.String `tfsdk:"path_match_type"`
	RewritePath     types.String `tfsdk:"rewrite_path"`
	RewritePathType types.String `tfsdk:"rewrite_path_type"`
	Priority        types.Int64  `tfsdk:"priority"`

	// Read-only computed health summary
	HCHealth types.String `tfsdk:"hc_health"`
}

// NewTargetResource returns a new resource factory.
func NewTargetResource() resource.Resource {
	return &TargetResource{}
}

func (r *TargetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_target"
}

func (r *TargetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	hcCompPlanModifierStr := stringplanmodifier.UseStateForUnknown()
	hcCompPlanModifierInt := int64planmodifier.UseStateForUnknown()
	hcCompPlanModifierBool := boolplanmodifier.UseStateForUnknown()
	resp.Schema = schema.Schema{
		Description: "Manages a Pangolin target (backend endpoint for an HTTP resource). " +
			"Exposes the full health-check (`hc_*`) and request-routing (`path`, `rewrite_path`, `priority`) " +
			"configuration accepted by the Pangolin API.\n\n" +
			"> **Note:** `hc_*` fields are sent only when set. Leaving one unset means the server default applies. " +
			"`hc_headers` is a list of `{name, value}` objects sent as the typed probe header set.",
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
				Description: "The IP address or hostname of the target (e.g. `localhost`, `10.0.0.1`).",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"port": schema.Int64Attribute{
				Description: "The port of the target.",
				Required:    true,
				Validators: []validator.Int64{
					int64validator.Between(1, 65535),
				},
			},
			"method": schema.StringAttribute{
				Description: "Scheme used to reach the target (`http` or `https`). Defaults to `http`.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("http"),
				Validators: []validator.String{
					stringvalidator.OneOf("http", "https"),
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Enable or disable this target. Defaults to `true`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			// Health-check configuration
			"hc_enabled": schema.BoolAttribute{
				Description: "Whether to enable active health-check probing for this target.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					hcCompPlanModifierBool,
				},
			},
			"hc_path": schema.StringAttribute{
				Description: "URL path the probe requests (e.g. `/health`).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					hcCompPlanModifierStr,
				},
			},
			"hc_scheme": schema.StringAttribute{
				Description: "Scheme used by the probe (`http` or `https`).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					hcCompPlanModifierStr,
				},
				Validators: []validator.String{
					stringvalidator.OneOf("http", "https"),
				},
			},
			"hc_mode": schema.StringAttribute{
				Description: "Health-check mode (e.g. `http`, `tcp`).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					hcCompPlanModifierStr,
				},
			},
			"hc_hostname": schema.StringAttribute{
				Description: "`Host` header value used by the probe.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					hcCompPlanModifierStr,
				},
			},
			"hc_port": schema.Int64Attribute{
				Description: "Port used by the probe. Defaults to the target port when unset.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					hcCompPlanModifierInt,
				},
				Validators: []validator.Int64{
					int64validator.Between(1, 65535),
				},
			},
			"hc_interval": schema.Int64Attribute{
				Description: "Probe interval in seconds while the target is healthy.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					hcCompPlanModifierInt,
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			"hc_unhealthy_interval": schema.Int64Attribute{
				Description: "Probe interval in seconds while the target is unhealthy (typically shorter).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					hcCompPlanModifierInt,
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			"hc_timeout": schema.Int64Attribute{
				Description: "Probe timeout in seconds.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					hcCompPlanModifierInt,
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			"hc_headers": schema.ListNestedAttribute{
				Description: "Request headers to set on the probe — list of `{name, value}` objects.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.UseStateForUnknown(),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":  schema.StringAttribute{Description: "Header name.", Required: true},
						"value": schema.StringAttribute{Description: "Header value.", Required: true},
					},
				},
			},
			"hc_follow_redirects": schema.BoolAttribute{
				Description: "Whether the probe follows HTTP redirects.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					hcCompPlanModifierBool,
				},
			},
			"hc_method": schema.StringAttribute{
				Description: "HTTP method used by the probe (e.g. `GET`, `HEAD`).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					hcCompPlanModifierStr,
				},
			},
			"hc_status": schema.Int64Attribute{
				Description: "Expected HTTP status code from the probe (e.g. `200`).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					hcCompPlanModifierInt,
				},
				Validators: []validator.Int64{
					int64validator.Between(100, 599),
				},
			},
			"hc_tls_server_name": schema.StringAttribute{
				Description: "TLS SNI used when probing over HTTPS.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					hcCompPlanModifierStr,
				},
			},
			"hc_healthy_threshold": schema.Int64Attribute{
				Description: "Consecutive successful probes required to mark the target healthy.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					hcCompPlanModifierInt,
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			"hc_unhealthy_threshold": schema.Int64Attribute{
				Description: "Consecutive failed probes required to mark the target unhealthy.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					hcCompPlanModifierInt,
				},
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			"hc_health": schema.StringAttribute{
				Description: "Current health summary as reported by Pangolin (`unknown`, `healthy`, `unhealthy`).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					hcCompPlanModifierStr,
				},
			},
			// Routing extras
			"path": schema.StringAttribute{
				Description: "URL path prefix routed to this target. Combined with `path_match_type`.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					hcCompPlanModifierStr,
				},
			},
			"path_match_type": schema.StringAttribute{
				Description: "How `path` is matched (e.g. `prefix`, `exact`, `regex`).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					hcCompPlanModifierStr,
				},
			},
			"rewrite_path": schema.StringAttribute{
				Description: "Path the request is rewritten to before being forwarded.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					hcCompPlanModifierStr,
				},
			},
			"rewrite_path_type": schema.StringAttribute{
				Description: "Rewrite mode.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					hcCompPlanModifierStr,
				},
			},
			"priority": schema.Int64Attribute{
				Description: "Routing priority — lower numbers win when multiple targets match.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					hcCompPlanModifierInt,
				},
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

	createReq := &client.CreateTargetRequest{
		IP:     plan.IP.ValueString(),
		Port:   int(plan.Port.ValueInt64()),
		Method: plan.Method.ValueString(),
		SiteID: int(plan.SiteID.ValueInt64()),
	}
	if v := plan.Enabled.ValueBool(); plan.Enabled.IsNull() || plan.Enabled.IsUnknown() {
		_ = v
	} else {
		b := v
		createReq.Enabled = &b
	}
	applyTargetHCFields(plan, &createReq.HCEnabled, &createReq.HCPath, &createReq.HCScheme,
		&createReq.HCMode, &createReq.HCHostname, &createReq.HCPort, &createReq.HCInterval,
		&createReq.HCUnhealthyInterval, &createReq.HCTimeout, &createReq.HCHeaders,
		&createReq.HCFollowRedirects, &createReq.HCMethod, &createReq.HCStatus,
		&createReq.HCTLSServerName, &createReq.HCHealthyThreshold, &createReq.HCUnhealthyThreshold,
		&createReq.Path, &createReq.PathMatchType, &createReq.RewritePath, &createReq.RewritePathType,
		&createReq.Priority)

	target, err := r.client.CreateTarget(ctx, int(plan.ResourceID.ValueInt64()), createReq)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create target", err.Error())
		return
	}

	state := targetToModel(target, plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *TargetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TargetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	target, err := r.client.GetTarget(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read target", err.Error())
		return
	}

	next := targetToModel(target, state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *TargetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TargetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateReq := &client.UpdateTargetRequest{
		IP:      plan.IP.ValueString(),
		Port:    int(plan.Port.ValueInt64()),
		Method:  plan.Method.ValueString(),
		Enabled: plan.Enabled.ValueBool(),
		SiteID:  int(plan.SiteID.ValueInt64()),
	}
	applyTargetHCFields(plan, &updateReq.HCEnabled, &updateReq.HCPath, &updateReq.HCScheme,
		&updateReq.HCMode, &updateReq.HCHostname, &updateReq.HCPort, &updateReq.HCInterval,
		&updateReq.HCUnhealthyInterval, &updateReq.HCTimeout, &updateReq.HCHeaders,
		&updateReq.HCFollowRedirects, &updateReq.HCMethod, &updateReq.HCStatus,
		&updateReq.HCTLSServerName, &updateReq.HCHealthyThreshold, &updateReq.HCUnhealthyThreshold,
		&updateReq.Path, &updateReq.PathMatchType, &updateReq.RewritePath, &updateReq.RewritePathType,
		&updateReq.Priority)

	target, err := r.client.UpdateTarget(ctx, int(plan.ID.ValueInt64()), updateReq)
	if err != nil {
		resp.Diagnostics.AddError("Failed to update target", err.Error())
		return
	}

	state := targetToModel(target, plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *TargetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TargetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteTarget(ctx, int(state.ID.ValueInt64()))
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

	target, err := r.client.GetTarget(ctx, int(id))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import target", err.Error())
		return
	}

	state := targetToModel(target, TargetResourceModel{})
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// applyTargetHCFields copies plan health-check + routing attributes
// into the pointer fields of the matching client request struct. A
// null/unknown plan attribute leaves the request pointer nil (=
// "omit from body, keep server default"). hc_headers is the single
// list-typed attribute and goes through ElementsAs.
func applyTargetHCFields(
	plan TargetResourceModel,
	hcEnabled **bool, hcPath, hcScheme, hcMode, hcHostname **string,
	hcPort, hcInterval, hcUnhealthyInterval, hcTimeout **int,
	hcHeaders *[]client.TargetHCHeader, hcFollowRedirects **bool,
	hcMethod **string, hcStatus **int, hcTLSServerName **string,
	hcHealthyThreshold, hcUnhealthyThreshold **int,
	path, pathMatchType, rewritePath, rewritePathType **string,
	priority **int,
) {
	setStr := func(t types.String, dst **string) {
		if !t.IsNull() && !t.IsUnknown() {
			v := t.ValueString()
			*dst = &v
		}
	}
	setInt := func(t types.Int64, dst **int) {
		if !t.IsNull() && !t.IsUnknown() {
			v := int(t.ValueInt64())
			*dst = &v
		}
	}
	setBool := func(t types.Bool, dst **bool) {
		if !t.IsNull() && !t.IsUnknown() {
			v := t.ValueBool()
			*dst = &v
		}
	}
	setBool(plan.HCEnabled, hcEnabled)
	setStr(plan.HCPath, hcPath)
	setStr(plan.HCScheme, hcScheme)
	setStr(plan.HCMode, hcMode)
	setStr(plan.HCHostname, hcHostname)
	setInt(plan.HCPort, hcPort)
	setInt(plan.HCInterval, hcInterval)
	setInt(plan.HCUnhealthyInterval, hcUnhealthyInterval)
	setInt(plan.HCTimeout, hcTimeout)
	setBool(plan.HCFollowRedirects, hcFollowRedirects)
	setStr(plan.HCMethod, hcMethod)
	setInt(plan.HCStatus, hcStatus)
	setStr(plan.HCTLSServerName, hcTLSServerName)
	setInt(plan.HCHealthyThreshold, hcHealthyThreshold)
	setInt(plan.HCUnhealthyThreshold, hcUnhealthyThreshold)
	setStr(plan.Path, path)
	setStr(plan.PathMatchType, pathMatchType)
	setStr(plan.RewritePath, rewritePath)
	setStr(plan.RewritePathType, rewritePathType)
	setInt(plan.Priority, priority)
	if len(plan.HCHeaders) > 0 {
		hs := make([]client.TargetHCHeader, len(plan.HCHeaders))
		for i, h := range plan.HCHeaders {
			hs[i] = client.TargetHCHeader{
				Name:  h.Name.ValueString(),
				Value: h.Value.ValueString(),
			}
		}
		*hcHeaders = hs
	}
}

// targetToModel maps a *client.Target onto a TargetResourceModel,
// decoding the JSON-string-encoded hcHeaders into the typed list.
// Malformed hcHeaders silently fall back to an empty list — the
// alternative would be a hard error on every Read of a corrupt
// server-side payload, which is the wrong tradeoff here.
func targetToModel(t *client.Target, prior TargetResourceModel) TargetResourceModel {
	headers, _ := client.ParseTargetHCHeaders(t.HCHeadersRaw)
	hcHeaders := make([]TargetHCHeaderModel, len(headers))
	for i, h := range headers {
		hcHeaders[i] = TargetHCHeaderModel{
			Name:  types.StringValue(h.Name),
			Value: types.StringValue(h.Value),
		}
	}
	_ = prior

	return TargetResourceModel{
		ID:                   types.Int64Value(int64(t.TargetID)),
		ResourceID:           types.Int64Value(int64(t.ResourceID)),
		SiteID:               types.Int64Value(int64(t.SiteID)),
		IP:                   types.StringValue(t.IP),
		Port:                 types.Int64Value(int64(t.Port)),
		Method:               types.StringValue(t.Method),
		Enabled:              types.BoolValue(t.Enabled),
		HCEnabled:            types.BoolValue(t.HCEnabled),
		HCPath:               tfconv.StringFromPtr(t.HCPath),
		HCScheme:             tfconv.StringFromPtr(t.HCScheme),
		HCMode:               tfconv.StringFromPtr(t.HCMode),
		HCHostname:           tfconv.StringFromPtr(t.HCHostname),
		HCPort:               tfconv.Int64FromIntPtr(t.HCPort),
		HCInterval:           tfconv.Int64FromIntPtr(t.HCInterval),
		HCUnhealthyInterval:  tfconv.Int64FromIntPtr(t.HCUnhealthyInterval),
		HCTimeout:            tfconv.Int64FromIntPtr(t.HCTimeout),
		HCHeaders:            hcHeaders,
		HCFollowRedirects:    tfconv.BoolFromPtr(t.HCFollowRedirects),
		HCMethod:             tfconv.StringFromPtr(t.HCMethod),
		HCStatus:             tfconv.Int64FromIntPtr(t.HCStatus),
		HCTLSServerName:      tfconv.StringFromPtr(t.HCTLSServerName),
		HCHealthyThreshold:   tfconv.Int64FromIntPtr(t.HCHealthyThreshold),
		HCUnhealthyThreshold: tfconv.Int64FromIntPtr(t.HCUnhealthyThreshold),
		HCHealth:             types.StringValue(t.HCHealth),
		Path:                 tfconv.StringFromPtr(t.Path),
		PathMatchType:        tfconv.StringFromPtr(t.PathMatchType),
		RewritePath:          tfconv.StringFromPtr(t.RewritePath),
		RewritePathType:      tfconv.StringFromPtr(t.RewritePathType),
		Priority:             tfconv.Int64FromIntPtr(t.Priority),
	}
}
