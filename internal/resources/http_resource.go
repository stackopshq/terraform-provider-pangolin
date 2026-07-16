package resources

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
)

var (
	_ resource.Resource                = &HTTPResource{}
	_ resource.ResourceWithImportState = &HTTPResource{}
)

// HTTPResource defines the resource implementation.
type HTTPResource struct {
	client *client.Client
}

// ResourceHeaderModel mirrors one injected request header for HCL input.
type ResourceHeaderModel struct {
	Name  types.String `tfsdk:"name"`
	Value types.String `tfsdk:"value"`
}

// HTTPResourceModel describes the resource data model.
type HTTPResourceModel struct {
	ID                    types.Int64  `tfsdk:"id"`
	NiceID                types.String `tfsdk:"nice_id"`
	Name                  types.String `tfsdk:"name"`
	Subdomain             types.String `tfsdk:"subdomain"`
	FullDomain            types.String `tfsdk:"full_domain"`
	DomainID              types.String `tfsdk:"domain_id"`
	Protocol              types.String `tfsdk:"protocol"`
	SSO                   types.Bool   `tfsdk:"sso"`
	SSL                   types.Bool   `tfsdk:"ssl"`
	Enabled               types.Bool   `tfsdk:"enabled"`
	BlockAccess           types.Bool   `tfsdk:"block_access"`
	EmailWhitelistEnabled types.Bool   `tfsdk:"email_whitelist_enabled"`
	ApplyRules            types.Bool   `tfsdk:"apply_rules"`
	StickySession         types.Bool   `tfsdk:"sticky_session"`
	TLSServerName         types.String `tfsdk:"tls_server_name"`

	// 1.19+ mode / PAM / auth-daemon (create-only).
	Mode           types.String `tfsdk:"mode"`
	ProxyPort      types.Int64  `tfsdk:"proxy_port"`
	PamMode        types.String `tfsdk:"pam_mode"`
	AuthDaemonMode types.String `tfsdk:"auth_daemon_mode"`
	AuthDaemonPort types.Int64  `tfsdk:"auth_daemon_port"`

	// 1.19+ mutable routing / auth injection.
	SetHostHeader types.String          `tfsdk:"set_host_header"`
	EnableProxy   types.Bool            `tfsdk:"enable_proxy"`
	Headers       []ResourceHeaderModel `tfsdk:"headers"`
	SkipToIdpID   types.Int64           `tfsdk:"skip_to_idp_id"`
	PostAuthPath  types.String          `tfsdk:"post_auth_path"`

	// 1.19+ mutable proxy-protocol config.
	ProxyProtocol        types.Bool  `tfsdk:"proxy_protocol"`
	ProxyProtocolVersion types.Int64 `tfsdk:"proxy_protocol_version"`

	// 1.19+ mutable maintenance mode.
	MaintenanceModeEnabled   types.Bool   `tfsdk:"maintenance_mode_enabled"`
	MaintenanceModeType      types.String `tfsdk:"maintenance_mode_type"`
	MaintenanceTitle         types.String `tfsdk:"maintenance_title"`
	MaintenanceMessage       types.String `tfsdk:"maintenance_message"`
	MaintenanceEstimatedTime types.String `tfsdk:"maintenance_estimated_time"`

	// 1.19+ read-only identity / policy backing.
	ResourceGuid            types.String `tfsdk:"resource_guid"`
	Wildcard                types.Bool   `tfsdk:"wildcard"`
	Health                  types.String `tfsdk:"health"`
	ResourcePolicyID        types.Int64  `tfsdk:"resource_policy_id"`
	DefaultResourcePolicyID types.Int64  `tfsdk:"default_resource_policy_id"`
}

// NewHTTPResource returns a new resource factory.
func NewHTTPResource() resource.Resource {
	return &HTTPResource{}
}

func (r *HTTPResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource"
}

func (r *HTTPResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Pangolin resource - the public-facing endpoint that Pangolin exposes. " +
			"The `mode` attribute picks the transport: `http` (reverse-proxy the historical default), " +
			"`tcp` or `udp` (raw L4). HTTP resources are addressed by `domain_id` + `subdomain`; " +
			"L4 resources are addressed by `proxy_port` on the Pangolin edge.\n\n" +
			"> **Note:** the 1.19+ attributes (`mode`, `pam_mode`, `auth_daemon_*`, `set_host_header`, " +
			"`enable_proxy`, `headers`, `proxy_protocol*`, `maintenance_*`, `post_auth_path`, " +
			"`skip_to_idp_id`, `resource_guid`, `health`, `resource_policy_id`, `default_resource_policy_id`) " +
			"are only meaningful on Pangolin >= 1.19. On older servers they either fall back to the " +
			"server default or stay absent from the wire (Optional+Computed keeps state consistent).\n\n" +
			"> **Note:** SSH / RDP / VNC bastions are `mode = \"tcp\"` resources with `pam_mode` and " +
			"`auth_daemon_*` set. The Pangolin server does not yet accept `ssh`/`rdp`/`vnc` as " +
			"literal `mode` values.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "The numeric ID of the resource.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"nice_id": schema.StringAttribute{
				Description: "The human-readable ID of the resource.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The name of the resource.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"subdomain": schema.StringAttribute{
				Description: "The subdomain for the resource. Only used when `mode = \"http\"`; ignored " +
					"for L4 modes. Set to null to use the base domain.",
				Optional: true,
				Validators: []validator.String{
					// RFC 1123 label: lowercase letters, digits, hyphens; cannot
					// start or end with a hyphen; max 63 characters.
					stringvalidator.RegexMatches(
						regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`),
						"must be a valid DNS label (lowercase letters, digits and hyphens; cannot start or end with a hyphen)",
					),
				},
			},
			"full_domain": schema.StringAttribute{
				Description: "The full domain of the resource (computed). Empty for L4 modes.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"domain_id": schema.StringAttribute{
				Description: "The domain ID to attach this resource to. Required when `mode = \"http\"`, " +
					"leave unset for L4 modes.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"protocol": schema.StringAttribute{
				Description: "The L4 protocol (tcp or udp). Only meaningful for the `ssh` / `rdp` / `vnc` / " +
					"`tcp` / `udp` L4 modes; ignored for `http`. Defaults to `tcp`.",
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString("tcp"),
				Validators: []validator.String{
					stringvalidator.OneOf("tcp", "udp"),
				},
			},
			"mode": schema.StringAttribute{
				Description: "The resource mode. One of `http` (reverse proxy, default), `tcp` or `udp` " +
					"(raw L4). Changing this attribute forces replacement.\n\n" +
					"> **Note:** SSH / RDP / VNC bastion modes are configured by starting from `mode = " +
					"\"tcp\"` and layering `pam_mode` / `auth_daemon_mode` / `auth_daemon_port`. " +
					"The Pangolin server does **not** accept `\"ssh\"` / `\"rdp\"` / `\"vnc\"` as literal " +
					"mode values yet (spec-ahead-of-server pattern in 1.19).",
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString("http"),
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("http", "tcp", "udp"),
				},
			},
			"proxy_port": schema.Int64Attribute{
				Description: "The proxy port that the Pangolin edge listens on for this L4 resource. " +
					"Required for `ssh` / `rdp` / `vnc` / `tcp` / `udp` modes; ignored for `http`. " +
					"Changing this attribute forces replacement.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
					int64planmodifier.UseStateForUnknown(),
				},
				Validators: []validator.Int64{
					int64validator.Between(1, 65535),
				},
			},
			"pam_mode": schema.StringAttribute{
				Description: "PAM (Pluggable Authentication Module) mode for SSH resources. One of " +
					"`passthrough` or `push`. Only meaningful for `mode = \"ssh\"`.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("passthrough", "push"),
				},
			},
			"auth_daemon_mode": schema.StringAttribute{
				Description: "Auth-daemon deployment mode for L4 resources. Typical values: `sidecar`, `remote`.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"auth_daemon_port": schema.Int64Attribute{
				Description: "TCP port the auth daemon listens on when `auth_daemon_mode` is set.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
				Validators: []validator.Int64{
					int64validator.Between(1, 65535),
				},
			},
			"sso": schema.BoolAttribute{
				Description: "Enable Pangolin SSO authentication on this resource. Set to false to make the resource publicly accessible.",
				Optional:    true,
				Computed:    true,
			},
			"ssl": schema.BoolAttribute{
				Description: "Enable SSL towards the backend.",
				Optional:    true,
				Computed:    true,
			},
			"enabled": schema.BoolAttribute{
				Description: "Enable or disable the resource.",
				Optional:    true,
				Computed:    true,
			},
			"block_access": schema.BoolAttribute{
				Description: "Block all access to the resource.",
				Optional:    true,
				Computed:    true,
			},
			"email_whitelist_enabled": schema.BoolAttribute{
				Description: "Enable the email whitelist on this resource.",
				Optional:    true,
				Computed:    true,
			},
			"apply_rules": schema.BoolAttribute{
				Description: "Enable evaluation of access rules on this resource.",
				Optional:    true,
				Computed:    true,
			},
			"sticky_session": schema.BoolAttribute{
				Description: "Enable sticky sessions (persistent sessions) on this resource.",
				Optional:    true,
				Computed:    true,
			},
			"tls_server_name": schema.StringAttribute{
				Description: "TLS server name for the backend. Set to null to clear.",
				Optional:    true,
				Computed:    true,
			},
			"set_host_header": schema.StringAttribute{
				Description: "Override the upstream `Host` header. Only meaningful for `mode = \"http\"`. " +
					"Set to null to keep the incoming host.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enable_proxy": schema.BoolAttribute{
				Description: "Whether the upstream HTTP proxy layer (headers, path rewriting, buffering) " +
					"is on. Read-only in the current provider: the server-side POST endpoint refuses this " +
					"key with `Unrecognized key: \"enableProxy\"`. Configure it out-of-band and Terraform " +
					"will surface the effective value.",
				Computed: true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"headers": schema.ListNestedAttribute{
				Description: "Static request headers injected on the upstream request - list of `{name, value}` objects. " +
					"Leave unset to keep the server default (no injection). Cannot be marked Computed because " +
					"the Go slice model type in the plugin framework does not carry an \"unknown\" sentinel.",
				Optional: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":  schema.StringAttribute{Description: "Header name.", Required: true},
						"value": schema.StringAttribute{Description: "Header value.", Required: true},
					},
				},
			},
			"skip_to_idp_id": schema.Int64Attribute{
				Description: "Bypass the Pangolin login page and jump straight to the given IdP.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"post_auth_path": schema.StringAttribute{
				Description: "Landing path after a successful login (e.g. `/dashboard`).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"proxy_protocol": schema.BoolAttribute{
				Description: "Whether PROXY-protocol encapsulation is on for the upstream L4 socket. " +
					"Read-only in the current provider - the server-side POST endpoint refuses this key.",
				Computed: true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"proxy_protocol_version": schema.Int64Attribute{
				Description: "PROXY-protocol version (1 or 2). Read-only in the current provider - " +
					"the server-side POST endpoint refuses this key.",
				Computed: true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"maintenance_mode_enabled": schema.BoolAttribute{
				Description: "Serve a maintenance page (or reject the L4 connection) instead of proxying.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"maintenance_mode_type": schema.StringAttribute{
				Description: "Maintenance type - free-form label surfaced on the maintenance page.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"maintenance_title": schema.StringAttribute{
				Description: "Maintenance page title.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"maintenance_message": schema.StringAttribute{
				Description: "Maintenance page body message.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"maintenance_estimated_time": schema.StringAttribute{
				Description: "Free-form estimated end-of-maintenance timestamp / duration (surfaced on the maintenance page).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"resource_guid": schema.StringAttribute{
				Description: "Server-assigned globally unique identifier (1.19+).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"wildcard": schema.BoolAttribute{
				Description: "Whether the resource matches a wildcard subdomain (1.19+).",
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"health": schema.StringAttribute{
				Description: "Server-reported aggregate health status (1.19+).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"resource_policy_id": schema.Int64Attribute{
				Description: "ID of the shared resource-policy this resource is attached to (1.19+). " +
					"Read-only: the resource-policy CRUD endpoints require an API key scoped to " +
					"resource policies and are not exposed via this provider.",
				Computed: true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"default_resource_policy_id": schema.Int64Attribute{
				Description: "ID of the auto-created default resource-policy backing this resource (1.19+). " +
					"Read-only.",
				Computed: true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *HTTPResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *HTTPResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HTTPResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 1.19+ mode selection. `http` (or empty, on pre-1.19) is
	// the legacy HTTP resource; anything else is a new L4 mode
	// gated on `HTTP: false` server-side.
	mode := plan.Mode.ValueString()
	if mode == "" {
		mode = "http"
	}
	createReq := &client.CreateResourceRequest{
		Name:     plan.Name.ValueString(),
		HTTP:     mode == "http",
		DomainID: plan.DomainID.ValueString(),
		Protocol: plan.Protocol.ValueString(),
	}
	if mode != "http" {
		createReq.Mode = mode
	}
	if !plan.Subdomain.IsNull() && !plan.Subdomain.IsUnknown() {
		subdomain := plan.Subdomain.ValueString()
		createReq.Subdomain = &subdomain
	}
	if !plan.ProxyPort.IsNull() && !plan.ProxyPort.IsUnknown() {
		v := int(plan.ProxyPort.ValueInt64())
		createReq.ProxyPort = &v
	}
	// pam_mode / auth_daemon_* are refused by the server at Create
	// (400 "Unrecognized keys"). They are applied on the follow-up
	// Update call - buildHTTPResourceUpdateRequest picks them up
	// from the plan.

	resource, err := r.client.CreateResource(ctx, createReq)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create resource", err.Error())
		return
	}

	plan.ID = types.Int64Value(int64(resource.ResourceID))
	plan.NiceID = types.StringValue(resource.NiceID)

	// L4 resources (mode = tcp/udp) reject almost every scalar on
	// the follow-up POST /resource/{id} - only `name`, `enabled`,
	// and `proxyPort` are accepted, and proxyPort is create-only.
	// So we hydrate from the Create response directly and skip the
	// Update round-trip. L7 (http) still uses the historical
	// Create + Update pattern so user-set sso/ssl/etc. are pushed.
	if mode == "http" {
		updated, err := r.client.UpdateResource(ctx, int(plan.ID.ValueInt64()), buildHTTPResourceUpdateRequest(plan))
		if err != nil {
			resp.Diagnostics.AddError("Failed to apply resource settings after creation", err.Error())
			return
		}
		plan = applyHTTPResourceResponse(plan, updated)
	} else {
		plan = applyHTTPResourceResponse(plan, resource)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *HTTPResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HTTPResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resource, err := r.client.GetResource(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read resource", err.Error())
		return
	}

	state = applyHTTPResourceResponse(state, resource)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HTTPResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HTTPResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// See the note in Create: L4 resources accept only `name` and
	// `enabled` on POST /resource/{id}. Build a narrow payload for
	// them so we don't 400 on an unrelated field the user could not
	// have written anyway.
	updateReq := buildHTTPResourceUpdateRequest(plan)
	if plan.Mode.ValueString() != "http" {
		updateReq = &client.UpdateResourceRequest{
			Name:    plan.Name.ValueString(),
			Enabled: updateReq.Enabled,
		}
	}
	res, err := r.client.UpdateResource(ctx, int(plan.ID.ValueInt64()), updateReq)
	if err != nil {
		resp.Diagnostics.AddError("Failed to update resource", err.Error())
		return
	}

	plan = applyHTTPResourceResponse(plan, res)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *HTTPResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HTTPResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteResource(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to delete resource", err.Error())
		return
	}
}

func (r *HTTPResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ID", fmt.Sprintf("Cannot parse resource ID %q as integer", req.ID))
		return
	}

	res, err := r.client.GetResource(ctx, int(id))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import resource", err.Error())
		return
	}

	state := HTTPResourceModel{ID: types.Int64Value(int64(res.ResourceID))}
	state = applyHTTPResourceResponse(state, res)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// buildHTTPResourceUpdateRequest builds an UpdateResourceRequest from the model,
// sending only fields that the user explicitly set (non-unknown).
func buildHTTPResourceUpdateRequest(plan HTTPResourceModel) *client.UpdateResourceRequest {
	req := &client.UpdateResourceRequest{
		Name: plan.Name.ValueString(),
	}
	if !plan.Subdomain.IsNull() && !plan.Subdomain.IsUnknown() {
		s := plan.Subdomain.ValueString()
		req.Subdomain = &s
	}
	if !plan.SSO.IsUnknown() {
		v := plan.SSO.ValueBool()
		req.SSO = &v
	}
	if !plan.SSL.IsUnknown() {
		v := plan.SSL.ValueBool()
		req.SSL = &v
	}
	if !plan.Enabled.IsUnknown() {
		v := plan.Enabled.ValueBool()
		req.Enabled = &v
	}
	if !plan.BlockAccess.IsUnknown() {
		v := plan.BlockAccess.ValueBool()
		req.BlockAccess = &v
	}
	if !plan.EmailWhitelistEnabled.IsUnknown() {
		v := plan.EmailWhitelistEnabled.ValueBool()
		req.EmailWhitelistEnabled = &v
	}
	if !plan.ApplyRules.IsUnknown() {
		v := plan.ApplyRules.ValueBool()
		req.ApplyRules = &v
	}
	if !plan.StickySession.IsUnknown() {
		v := plan.StickySession.ValueBool()
		req.StickySession = &v
	}
	if !plan.TLSServerName.IsUnknown() {
		if plan.TLSServerName.IsNull() {
			req.TLSServerName = nil
		} else {
			s := plan.TLSServerName.ValueString()
			req.TLSServerName = &s
		}
	}

	// 1.19+ mutable fields - only send when the user set the value
	// (i.e. not Unknown coming from a Computed default). Empty strings
	// mean "empty string on wire", nulls mean "clear the field".
	if !plan.SetHostHeader.IsUnknown() {
		if plan.SetHostHeader.IsNull() {
			req.SetHostHeader = nil
		} else {
			s := plan.SetHostHeader.ValueString()
			req.SetHostHeader = &s
		}
	}
	if !isUnknownList(plan.Headers) {
		hs := make([]client.ResourceHeader, len(plan.Headers))
		for i, h := range plan.Headers {
			hs[i] = client.ResourceHeader{
				Name:  h.Name.ValueString(),
				Value: h.Value.ValueString(),
			}
		}
		req.Headers = &hs
	}
	if !plan.SkipToIdpID.IsUnknown() {
		if plan.SkipToIdpID.IsNull() {
			req.SkipToIdpID = nil
		} else {
			v := int(plan.SkipToIdpID.ValueInt64())
			req.SkipToIdpID = &v
		}
	}
	if !plan.PostAuthPath.IsUnknown() {
		if plan.PostAuthPath.IsNull() {
			req.PostAuthPath = nil
		} else {
			s := plan.PostAuthPath.ValueString()
			req.PostAuthPath = &s
		}
	}
	if !plan.MaintenanceModeEnabled.IsUnknown() {
		v := plan.MaintenanceModeEnabled.ValueBool()
		req.MaintenanceModeEnabled = &v
	}
	if !plan.MaintenanceModeType.IsUnknown() && !plan.MaintenanceModeType.IsNull() {
		req.MaintenanceModeType = plan.MaintenanceModeType.ValueString()
	}
	if !plan.MaintenanceTitle.IsUnknown() {
		if plan.MaintenanceTitle.IsNull() {
			req.MaintenanceTitle = nil
		} else {
			s := plan.MaintenanceTitle.ValueString()
			req.MaintenanceTitle = &s
		}
	}
	if !plan.MaintenanceMessage.IsUnknown() {
		if plan.MaintenanceMessage.IsNull() {
			req.MaintenanceMessage = nil
		} else {
			s := plan.MaintenanceMessage.ValueString()
			req.MaintenanceMessage = &s
		}
	}
	if !plan.MaintenanceEstimatedTime.IsUnknown() {
		if plan.MaintenanceEstimatedTime.IsNull() {
			req.MaintenanceEstimatedTime = nil
		} else {
			s := plan.MaintenanceEstimatedTime.ValueString()
			req.MaintenanceEstimatedTime = &s
		}
	}
	if !plan.PamMode.IsUnknown() && !plan.PamMode.IsNull() {
		req.PamMode = plan.PamMode.ValueString()
	}
	if !plan.AuthDaemonMode.IsUnknown() && !plan.AuthDaemonMode.IsNull() {
		req.AuthDaemonMode = plan.AuthDaemonMode.ValueString()
	}
	if !plan.AuthDaemonPort.IsUnknown() && !plan.AuthDaemonPort.IsNull() {
		v := int(plan.AuthDaemonPort.ValueInt64())
		req.AuthDaemonPort = &v
	}
	return req
}

// isUnknownList reports whether a nested-list model slice came in
// from Terraform as an Unknown value. A nil slice is our own signal
// (never populated from the plan yet), while a non-nil-but-empty
// slice is a user-provided empty list.
func isUnknownList(hs []ResourceHeaderModel) bool {
	return hs == nil
}

// applyHTTPResourceResponse copies API response fields into the model.
func applyHTTPResourceResponse(m HTTPResourceModel, res *client.Resource) HTTPResourceModel {
	m.NiceID = types.StringValue(res.NiceID)
	m.Name = types.StringValue(res.Name)
	m.FullDomain = types.StringValue(res.FullDomain)
	m.DomainID = types.StringValue(res.DomainID)
	if res.Subdomain != "" {
		m.Subdomain = types.StringValue(res.Subdomain)
	} else {
		m.Subdomain = types.StringNull()
	}
	m.SSO = types.BoolValue(res.SSO)
	m.SSL = types.BoolValue(res.SSL)
	m.Enabled = types.BoolValue(res.Enabled)
	m.BlockAccess = types.BoolValue(res.BlockAccess)
	m.EmailWhitelistEnabled = types.BoolValue(res.EmailWhitelistEnabled)
	m.ApplyRules = types.BoolValue(res.ApplyRules)
	m.StickySession = types.BoolValue(res.StickySession)
	if res.TLSServerName != nil {
		m.TLSServerName = types.StringValue(*res.TLSServerName)
	} else {
		m.TLSServerName = types.StringNull()
	}

	// 1.19+ scalars. Empty strings from the server are surfaced as
	// null in state so the plan diff stays clean on pre-1.19 servers.
	if res.Mode != "" {
		m.Mode = types.StringValue(res.Mode)
	} else {
		m.Mode = types.StringValue("http")
	}
	if res.PamMode != "" {
		m.PamMode = types.StringValue(res.PamMode)
	} else {
		m.PamMode = types.StringNull()
	}
	if res.AuthDaemonMode != "" {
		m.AuthDaemonMode = types.StringValue(res.AuthDaemonMode)
	} else {
		m.AuthDaemonMode = types.StringNull()
	}
	m.ProxyPort = tfconv.Int64FromIntPtr(res.ProxyPort)
	m.AuthDaemonPort = tfconv.Int64FromIntPtr(res.AuthDaemonPort)

	m.SetHostHeader = tfconv.StringFromPtr(res.SetHostHeader)
	m.EnableProxy = tfconv.BoolFromPtr(res.EnableProxy)
	m.SkipToIdpID = tfconv.Int64FromIntPtr(res.SkipToIdpID)
	m.PostAuthPath = tfconv.StringFromPtr(res.PostAuthPath)
	m.ProxyProtocol = tfconv.BoolFromPtr(res.ProxyProtocol)
	m.ProxyProtocolVersion = tfconv.Int64FromIntPtr(res.ProxyProtocolVersion)

	m.MaintenanceModeEnabled = tfconv.BoolFromPtr(res.MaintenanceModeEnabled)
	if res.MaintenanceModeType != "" {
		m.MaintenanceModeType = types.StringValue(res.MaintenanceModeType)
	} else {
		m.MaintenanceModeType = types.StringNull()
	}
	m.MaintenanceTitle = tfconv.StringFromPtr(res.MaintenanceTitle)
	m.MaintenanceMessage = tfconv.StringFromPtr(res.MaintenanceMessage)
	m.MaintenanceEstimatedTime = tfconv.StringFromPtr(res.MaintenanceEstimatedTime)

	// Read-only identity / policy backing.
	m.ResourceGuid = types.StringValue(res.ResourceGuid)
	m.Wildcard = types.BoolValue(res.Wildcard)
	if res.Health != "" {
		m.Health = types.StringValue(res.Health)
	} else {
		m.Health = types.StringNull()
	}
	m.ResourcePolicyID = tfconv.Int64FromIntPtr(res.ResourcePolicyID)
	m.DefaultResourcePolicyID = tfconv.Int64FromIntPtr(res.DefaultResourcePolicyID)

	// Headers - round-trip the list. An absent/empty wire list keeps
	// the model at nil so it round-trips as null (Optional+Computed).
	if len(res.Headers) > 0 {
		hs := make([]ResourceHeaderModel, len(res.Headers))
		for i, h := range res.Headers {
			hs[i] = ResourceHeaderModel{
				Name:  types.StringValue(h.Name),
				Value: types.StringValue(h.Value),
			}
		}
		m.Headers = hs
	} else {
		m.Headers = nil
	}
	return m
}
