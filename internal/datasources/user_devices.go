package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
	"github.com/stackopshq/terraform-provider-pangolin/internal/tfconv"
)

var _ datasource.DataSource = &UserDevicesDataSource{}

// UserDevicesDataSource lists the user-bound devices for an
// organization — phones, laptops, browsers, anything with a per-user
// binding. Distinct from `pangolin_clients` (not yet implemented),
// which would list org-level OLM clients without user association.
//
// Optional filter inputs mirror the upstream query params; an
// unspecified filter falls through to the server default
// (pageSize=20, page=1, status=[active,pending], order=asc).
type UserDevicesDataSource struct {
	client *client.Client
}

// UserDeviceItemModel mirrors [client.UserDevice] field-by-field.
// All nullable upstream fields decode to TF null when the wire emits
// null — the `name`, `subnet`, `org_*` and the boolean lifecycle
// flags are always present.
type UserDeviceItemModel struct {
	ClientID      types.Int64   `tfsdk:"client_id"`
	OrgID         types.String  `tfsdk:"org_id"`
	Name          types.String  `tfsdk:"name"`
	PubKey        types.String  `tfsdk:"pub_key"`
	Subnet        types.String  `tfsdk:"subnet"`
	MegabytesIn   types.Float64 `tfsdk:"megabytes_in"`
	MegabytesOut  types.Float64 `tfsdk:"megabytes_out"`
	OrgName       types.String  `tfsdk:"org_name"`
	Type          types.String  `tfsdk:"type"`
	Online        types.Bool    `tfsdk:"online"`
	OLMVersion    types.String  `tfsdk:"olm_version"`
	UserID        types.String  `tfsdk:"user_id"`
	Username      types.String  `tfsdk:"username"`
	UserEmail     types.String  `tfsdk:"user_email"`
	NiceID        types.String  `tfsdk:"nice_id"`
	Agent         types.String  `tfsdk:"agent"`
	ApprovalState types.String  `tfsdk:"approval_state"`
	OLMArchived   types.Bool    `tfsdk:"olm_archived"`
	Archived      types.Bool    `tfsdk:"archived"`
	Blocked       types.Bool    `tfsdk:"blocked"`
}

// UserDevicesDataSourceModel is the data source's top-level shape.
// The optional input attributes mirror the upstream query params.
type UserDevicesDataSourceModel struct {
	// Filters (Optional inputs)
	Query  types.String `tfsdk:"query"`
	SortBy types.String `tfsdk:"sort_by"`
	Order  types.String `tfsdk:"order"`
	Online types.Bool   `tfsdk:"online"`
	Agent  types.String `tfsdk:"agent"`
	Status types.List   `tfsdk:"status"`

	// Outputs
	Total   types.Int64           `tfsdk:"total"`
	Devices []UserDeviceItemModel `tfsdk:"devices"`
}

// NewUserDevicesDataSource returns a new data source factory.
func NewUserDevicesDataSource() datasource.DataSource {
	return &UserDevicesDataSource{}
}

func (d *UserDevicesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_devices"
}

func (d *UserDevicesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the user-bound devices for an organization — phones, laptops, " +
			"browsers, anything with a per-user binding. Distinct from `pangolin_clients` " +
			"(org-level OLM clients with no user association).\n\n" +
			"All filter attributes are optional; when omitted, the upstream applies its " +
			"defaults (`page_size = 20`, `status = [\"active\", \"pending\"]`, `order = \"asc\"`).\n\n" +
			"> **Note:** the item shape is inferred from the sibling `GET /org/{org}/clients` " +
			"endpoint (same `Client` OpenAPI tag, same \"Clients retrieved successfully\" " +
			"message). The `sites` field is intentionally omitted until a real value is " +
			"observed — its element shape can't be guessed safely from spec alone.",
		Attributes: map[string]schema.Attribute{
			"query":   schema.StringAttribute{Description: "Free-text query filter.", Optional: true},
			"sort_by": schema.StringAttribute{Description: "Sort field. One of `megabytesIn`, `megabytesOut`.", Optional: true},
			"order":   schema.StringAttribute{Description: "Sort order. One of `asc`, `desc`. Default: `asc`.", Optional: true},
			"online":  schema.BoolAttribute{Description: "Filter by online status.", Optional: true},
			"agent": schema.StringAttribute{
				Description: "Filter by device agent. One of `windows`, `android`, `cli`, `olm`, `macos`, `ios`, `ipados`, `unknown`.",
				Optional:    true,
			},
			"status": schema.ListAttribute{
				Description: "Filter by device approval / lifecycle status. Each entry is one of `active`, `pending`, `denied`, `blocked`, `archived`. Default: `[\"active\", \"pending\"]`.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"total": schema.Int64Attribute{Description: "Total number of devices matching the filters (across all pages).", Computed: true},
			"devices": schema.ListNestedAttribute{
				Description: "Device list (current page).",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"client_id":      schema.Int64Attribute{Description: "The numeric client ID.", Computed: true},
						"org_id":         schema.StringAttribute{Description: "The organization ID.", Computed: true},
						"name":           schema.StringAttribute{Description: "Device display name.", Computed: true},
						"pub_key":        schema.StringAttribute{Description: "Wireguard public key. Null when the device hasn't completed handshake.", Computed: true},
						"subnet":         schema.StringAttribute{Description: "The device's assigned subnet.", Computed: true},
						"megabytes_in":   schema.Float64Attribute{Description: "Cumulative inbound traffic in MB. Null when the counters haven't been populated yet.", Computed: true},
						"megabytes_out":  schema.Float64Attribute{Description: "Cumulative outbound traffic in MB. Null when the counters haven't been populated yet.", Computed: true},
						"org_name":       schema.StringAttribute{Description: "Display name of the parent organization.", Computed: true},
						"type":           schema.StringAttribute{Description: "Connection type — `olm` for org-level connectors, otherwise an end-user device type.", Computed: true},
						"online":         schema.BoolAttribute{Description: "Whether the device is currently online.", Computed: true},
						"olm_version":    schema.StringAttribute{Description: "OLM agent version when applicable. Null otherwise.", Computed: true},
						"user_id":        schema.StringAttribute{Description: "ID of the user this device is bound to. Null for org-level devices.", Computed: true},
						"username":       schema.StringAttribute{Description: "Username of the bound user. Null for org-level devices.", Computed: true},
						"user_email":     schema.StringAttribute{Description: "Email of the bound user. Null for org-level devices.", Computed: true},
						"nice_id":        schema.StringAttribute{Description: "Human-readable device ID.", Computed: true},
						"agent":          schema.StringAttribute{Description: "Device agent. One of `windows`, `android`, `cli`, `olm`, `macos`, `ios`, `ipados`, `unknown`. Null when unknown.", Computed: true},
						"approval_state": schema.StringAttribute{Description: "Approval state when device approval is enabled. Null otherwise.", Computed: true},
						"olm_archived":   schema.BoolAttribute{Description: "Whether the underlying OLM tunnel is archived.", Computed: true},
						"archived":       schema.BoolAttribute{Description: "Whether the device is archived.", Computed: true},
						"blocked":        schema.BoolAttribute{Description: "Whether the device is blocked.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *UserDevicesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected DataSource Configure Type", "Expected *client.Client")
		return
	}
	d.client = c
}

func (d *UserDevicesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config UserDevicesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	opts := &client.ListUserDevicesOptions{
		Query:  config.Query.ValueString(),
		SortBy: config.SortBy.ValueString(),
		Order:  config.Order.ValueString(),
		Agent:  config.Agent.ValueString(),
	}
	if !config.Online.IsNull() && !config.Online.IsUnknown() {
		v := config.Online.ValueBool()
		opts.Online = &v
	}
	if !config.Status.IsNull() && !config.Status.IsUnknown() {
		var statuses []string
		diags := config.Status.ElementsAs(ctx, &statuses, false)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		opts.Status = statuses
	}

	page, err := d.client.ListUserDevices(ctx, opts)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list user devices", err.Error())
		return
	}

	state := config
	state.Total = types.Int64Value(int64(page.Pagination.Total))
	state.Devices = make([]UserDeviceItemModel, 0, len(page.Devices))
	for _, dev := range page.Devices {
		state.Devices = append(state.Devices, UserDeviceItemModel{
			ClientID:      types.Int64Value(int64(dev.ClientID)),
			OrgID:         types.StringValue(dev.OrgID),
			Name:          types.StringValue(dev.Name),
			PubKey:        tfconv.StringFromPtr(dev.PubKey),
			Subnet:        types.StringValue(dev.Subnet),
			MegabytesIn:   tfconv.Float64FromPtr(dev.MegabytesIn),
			MegabytesOut:  tfconv.Float64FromPtr(dev.MegabytesOut),
			OrgName:       types.StringValue(dev.OrgName),
			Type:          types.StringValue(dev.Type),
			Online:        types.BoolValue(dev.Online),
			OLMVersion:    tfconv.StringFromPtr(dev.OLMVersion),
			UserID:        tfconv.StringFromPtr(dev.UserID),
			Username:      tfconv.StringFromPtr(dev.Username),
			UserEmail:     tfconv.StringFromPtr(dev.UserEmail),
			NiceID:        types.StringValue(dev.NiceID),
			Agent:         tfconv.StringFromPtr(dev.Agent),
			ApprovalState: tfconv.StringFromPtr(dev.ApprovalState),
			OLMArchived:   types.BoolValue(dev.OLMArchived),
			Archived:      types.BoolValue(dev.Archived),
			Blocked:       types.BoolValue(dev.Blocked),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
