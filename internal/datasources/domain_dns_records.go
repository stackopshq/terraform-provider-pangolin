package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

var _ datasource.DataSource = &DomainDNSRecordsDataSource{}

// DomainDNSRecordsDataSource lists the DNS records configured for a
// Pangolin domain. Useful to expose the required records to an
// external DNS-as-code stack so it can publish them.
type DomainDNSRecordsDataSource struct {
	client *client.Client
}

// DomainDNSRecordItemModel mirrors the wire shape of one DNS record.
type DomainDNSRecordItemModel struct {
	ID         types.Int64  `tfsdk:"id"`
	DomainID   types.String `tfsdk:"domain_id"`
	RecordType types.String `tfsdk:"record_type"`
	BaseDomain types.String `tfsdk:"base_domain"`
	Value      types.String `tfsdk:"value"`
	Verified   types.Bool   `tfsdk:"verified"`
}

// DomainDNSRecordsDataSourceModel describes the data source data model.
type DomainDNSRecordsDataSourceModel struct {
	DomainID types.String               `tfsdk:"domain_id"`
	Records  []DomainDNSRecordItemModel `tfsdk:"records"`
}

// NewDomainDNSRecordsDataSource returns a new data source factory.
func NewDomainDNSRecordsDataSource() datasource.DataSource {
	return &DomainDNSRecordsDataSource{}
}

func (d *DomainDNSRecordsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain_dns_records"
}

func (d *DomainDNSRecordsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the DNS records Pangolin expects (or has verified) for a domain - useful to feed an external " +
			"DNS-as-code provider that publishes them on your authoritative nameserver.",
		Attributes: map[string]schema.Attribute{
			"domain_id": schema.StringAttribute{
				Description: "ID of the Pangolin domain whose DNS records to retrieve.",
				Required:    true,
			},
			"records": schema.ListNestedAttribute{
				Description: "The DNS records associated with the domain.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          schema.Int64Attribute{Description: "Numeric ID of the DNS record.", Computed: true},
						"domain_id":   schema.StringAttribute{Description: "ID of the domain this record belongs to.", Computed: true},
						"record_type": schema.StringAttribute{Description: "DNS record type (`A`, `AAAA`, `CNAME`, `TXT`, …).", Computed: true},
						"base_domain": schema.StringAttribute{Description: "Name the record applies to (may be a wildcard, e.g. `*.example.com`).", Computed: true},
						"value":       schema.StringAttribute{Description: "Record value (IP, target name, TXT content, etc.).", Computed: true},
						"verified":    schema.BoolAttribute{Description: "Whether Pangolin has verified that the record is published on the authoritative nameserver.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *DomainDNSRecordsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *DomainDNSRecordsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg DomainDNSRecordsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	records, err := d.client.ListDomainDNSRecords(ctx, d.client.OrgID, cfg.DomainID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to list domain DNS records", err.Error())
		return
	}

	cfg.Records = make([]DomainDNSRecordItemModel, len(records))
	for i, r := range records {
		cfg.Records[i] = DomainDNSRecordItemModel{
			ID:         types.Int64Value(int64(r.ID)),
			DomainID:   types.StringValue(r.DomainID),
			RecordType: types.StringValue(r.RecordType),
			BaseDomain: types.StringValue(r.BaseDomain),
			Value:      types.StringValue(r.Value),
			Verified:   types.BoolValue(r.Verified),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
