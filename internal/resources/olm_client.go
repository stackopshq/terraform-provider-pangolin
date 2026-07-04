package resources

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource                = &OLMClientResource{}
	_ resource.ResourceWithImportState = &OLMClientResource{}
)

// OLMClientResource manages a Pangolin OLM client device.
type OLMClientResource struct {
	client *client.Client
}

// OLMClientResourceModel describes the resource data model.
type OLMClientResourceModel struct {
	ID       types.Int64  `tfsdk:"id"`
	NiceID   types.String `tfsdk:"nice_id"`
	OlmID    types.String `tfsdk:"olm_id"`
	Name     types.String `tfsdk:"name"`
	Online   types.Bool   `tfsdk:"online"`
	Archived types.Bool   `tfsdk:"archived"`
	Blocked  types.Bool   `tfsdk:"blocked"`
	Secret   types.String `tfsdk:"secret"`
}

// NewOLMClientResource returns a new resource factory.
func NewOLMClientResource() resource.Resource {
	return &OLMClientResource{}
}

func (r *OLMClientResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_client"
}

func (r *OLMClientResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Pangolin OLM (Overlay LAN Manager) client device.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "The numeric ID of the client.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"nice_id": schema.StringAttribute{
				Description: "The human-readable ID of the client.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"olm_id": schema.StringAttribute{
				Description: "The OLM ID the client authenticates with (paired with `secret`).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The name of the client.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"online": schema.BoolAttribute{
				Description: "Whether the client is currently online.",
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"archived": schema.BoolAttribute{
				Description: "Whether the client is archived. Archived clients are kept for audit but cannot connect. Defaults to `false`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"blocked": schema.BoolAttribute{
				Description: "Whether the client is blocked. Blocked clients are denied from connecting until unblocked. Defaults to `false`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"secret": schema.StringAttribute{
				Description: "The client secret. Only available at creation time; stored in state.",
				Computed:    true,
				Sensitive:   true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *OLMClientResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *OLMClientResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan OLMClientResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	defaults, err := r.client.GetClientDefaults(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to get client defaults", err.Error())
		return
	}

	olmClient, err := r.client.CreateOLMClient(ctx, &client.CreateOLMClientRequest{
		Name:   plan.Name.ValueString(),
		OlmID:  defaults.OlmID,
		Secret: defaults.OlmSecret,
		Subnet: defaults.Subnet,
		Type:   "olm",
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to create OLM client", err.Error())
		return
	}

	if err := reconcileClientLifecycle(ctx, r.client, olmClient.ClientID, olmClient.Archived, plan.Archived.ValueBool(), olmClient.Blocked, plan.Blocked.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Failed to reconcile client lifecycle", err.Error())
		return
	}

	plan.ID = types.Int64Value(int64(olmClient.ClientID))
	plan.NiceID = types.StringValue(olmClient.NiceID)
	plan.Name = types.StringValue(olmClient.Name)
	plan.Online = types.BoolValue(olmClient.Online)
	plan.Archived = types.BoolValue(plan.Archived.ValueBool())
	plan.Blocked = types.BoolValue(plan.Blocked.ValueBool())
	plan.OlmID = types.StringValue(defaults.OlmID)
	// The secret is the olmSecret used during creation (not returned by the API).
	plan.Secret = types.StringValue(defaults.OlmSecret)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *OLMClientResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state OLMClientResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	olmClient, err := r.client.GetOLMClient(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read OLM client", err.Error())
		return
	}

	state.NiceID = types.StringValue(olmClient.NiceID)
	state.Name = types.StringValue(olmClient.Name)
	state.Online = types.BoolValue(olmClient.Online)
	state.Archived = types.BoolValue(olmClient.Archived)
	state.Blocked = types.BoolValue(olmClient.Blocked)
	state.OlmID = types.StringValue(olmClient.OlmID)
	// Secret is not returned by Get; preserve existing state value.

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *OLMClientResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan OLMClientResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state OLMClientResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	olmClient, err := r.client.UpdateOLMClient(ctx, int(plan.ID.ValueInt64()), &client.UpdateOLMClientRequest{
		Name: plan.Name.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to update OLM client", err.Error())
		return
	}

	if err := reconcileClientLifecycle(ctx, r.client, int(plan.ID.ValueInt64()), state.Archived.ValueBool(), plan.Archived.ValueBool(), state.Blocked.ValueBool(), plan.Blocked.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Failed to reconcile client lifecycle", err.Error())
		return
	}

	plan.NiceID = types.StringValue(olmClient.NiceID)
	plan.Name = types.StringValue(olmClient.Name)
	plan.Online = types.BoolValue(olmClient.Online)
	plan.Archived = types.BoolValue(plan.Archived.ValueBool())
	plan.Blocked = types.BoolValue(plan.Blocked.ValueBool())

	// olmId is immutable, so its current value is whatever is already in state.
	plan.OlmID = state.OlmID

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// reconcileClientLifecycle issues the archive/unarchive and block/unblock calls
// needed to drive a client from the (currentArchived, currentBlocked) tuple to
// the (wantArchived, wantBlocked) tuple. Each direction is independently no-op
// when already in the desired state.
func reconcileClientLifecycle(ctx context.Context, c *client.Client, clientID int, currentArchived, wantArchived, currentBlocked, wantBlocked bool) error {
	if currentArchived != wantArchived {
		if wantArchived {
			if err := c.ArchiveClient(ctx, clientID); err != nil {
				return err
			}
		} else {
			if err := c.UnarchiveClient(ctx, clientID); err != nil {
				return err
			}
		}
	}
	if currentBlocked != wantBlocked {
		if wantBlocked {
			if err := c.BlockClient(ctx, clientID); err != nil {
				return err
			}
		} else {
			if err := c.UnblockClient(ctx, clientID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *OLMClientResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state OLMClientResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteOLMClient(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to delete OLM client", err.Error())
		return
	}
}

func (r *OLMClientResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ID", fmt.Sprintf("Cannot parse client ID %q as integer", req.ID))
		return
	}

	olmClient, err := r.client.GetOLMClient(ctx, int(id))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import OLM client", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &OLMClientResourceModel{
		ID:       types.Int64Value(int64(olmClient.ClientID)),
		NiceID:   types.StringValue(olmClient.NiceID),
		OlmID:    types.StringValue(olmClient.OlmID),
		Name:     types.StringValue(olmClient.Name),
		Online:   types.BoolValue(olmClient.Online),
		Archived: types.BoolValue(olmClient.Archived),
		Blocked:  types.BoolValue(olmClient.Blocked),
		Secret:   types.StringValue(""), // Secret cannot be recovered after creation.
	})...)
}
