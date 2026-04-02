package resources

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var (
	_ resource.Resource              = &SiteResource{}
	_ resource.ResourceWithImportState = &SiteResource{}
)

// SiteResource defines the resource implementation.
type SiteResource struct {
	client *client.Client
}

// SiteResourceModel describes the resource data model.
type SiteResourceModel struct {
	ID         types.Int64  `tfsdk:"id"`
	NiceID     types.String `tfsdk:"nice_id"`
	Name       types.String `tfsdk:"name"`
	Type       types.String `tfsdk:"type"`
	Online     types.Bool   `tfsdk:"online"`
	Address    types.String `tfsdk:"address"`
	NewtID     types.String `tfsdk:"newt_id"`
	NewtSecret types.String `tfsdk:"newt_secret"`
}

// NewSiteResource returns a new resource factory.
func NewSiteResource() resource.Resource {
	return &SiteResource{}
}

func (r *SiteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site"
}

func (r *SiteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Pangolin site (tunnel connector).",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "The numeric ID of the site.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"nice_id": schema.StringAttribute{
				Description: "The human-readable ID of the site.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The name of the site.",
				Required:    true,
			},
			"type": schema.StringAttribute{
				Description: "The type of the site (e.g. 'newt').",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"online": schema.BoolAttribute{
				Description: "Whether the site is online.",
				Computed:    true,
			},
			"address": schema.StringAttribute{
				Description: "The WireGuard address of the site.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"newt_id": schema.StringAttribute{
				Description: "The Newt client ID assigned to this site.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"newt_secret": schema.StringAttribute{
				Description: "The Newt client secret assigned to this site. Sensitive — not returned by the API after creation.",
				Computed:    true,
				Sensitive:   true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *SiteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *SiteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SiteResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get site defaults (newtId, secret, address)
	defaults, err := r.client.GetSiteDefaults()
	if err != nil {
		resp.Diagnostics.AddError("Failed to get site defaults", err.Error())
		return
	}

	site, err := r.client.CreateSite(&client.CreateSiteRequest{
		Name:   plan.Name.ValueString(),
		Type:   "newt",
		NewtID: defaults.NewtID,
		Secret: defaults.NewtSecret,
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to create site", err.Error())
		return
	}

	plan.ID = types.Int64Value(int64(site.SiteID))
	plan.NiceID = types.StringValue(site.NiceID)
	plan.Type = types.StringValue(site.Type)
	plan.Online = types.BoolValue(site.Online)
	plan.Address = types.StringValue(site.Address)
	plan.NewtID = types.StringValue(defaults.NewtID)
	plan.NewtSecret = types.StringValue(defaults.NewtSecret)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SiteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SiteResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	site, err := r.client.GetSite(int(state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to read site", err.Error())
		return
	}

	state.NiceID = types.StringValue(site.NiceID)
	state.Name = types.StringValue(site.Name)
	state.Type = types.StringValue(site.Type)
	state.Online = types.BoolValue(site.Online)
	state.Address = types.StringValue(site.Address)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SiteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan SiteResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	site, err := r.client.UpdateSite(int(plan.ID.ValueInt64()), &client.UpdateSiteRequest{
		Name: plan.Name.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to update site", err.Error())
		return
	}

	plan.NiceID = types.StringValue(site.NiceID)
	plan.Name = types.StringValue(site.Name)
	plan.Type = types.StringValue(site.Type)
	plan.Online = types.BoolValue(site.Online)
	plan.Address = types.StringValue(site.Address)
	// newt_id and newt_secret are write-once; UseStateForUnknown preserves them in plan.

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SiteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SiteResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteSite(int(state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Failed to delete site", err.Error())
		return
	}
}

func (r *SiteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ID", fmt.Sprintf("Cannot parse site ID %q as integer", req.ID))
		return
	}

	site, err := r.client.GetSite(int(id))
	if err != nil {
		resp.Diagnostics.AddError("Failed to import site", err.Error())
		return
	}

	state := SiteResourceModel{
		ID:         types.Int64Value(int64(site.SiteID)),
		NiceID:     types.StringValue(site.NiceID),
		Name:       types.StringValue(site.Name),
		Type:       types.StringValue(site.Type),
		Online:     types.BoolValue(site.Online),
		Address:    types.StringValue(site.Address),
		NewtID:     types.StringNull(),
		NewtSecret: types.StringNull(),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
