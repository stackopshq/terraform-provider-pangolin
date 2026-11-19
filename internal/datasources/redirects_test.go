package datasources

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

func ptr[T any](v T) *T { return &v }

func sampleRedirects() []client.Redirect {
	return []client.Redirect{
		{
			RedirectID: 1, NiceID: "a", Name: "A", ResourceID: ptr(10), DestinationHost: "https://a.example",
			PathMatchType: "regex", Priority: ptr(5), Enabled: true, SSL: true,
			ResourceName: ptr("res"), ResourceNiceID: ptr("res-nice"), ResourceFullDomain: ptr("a.example.com"),
		},
		{
			RedirectID: 2, NiceID: "b", Name: "B", DomainID: ptr("dom1"), Subdomain: ptr("www"),
			DestinationHost: "https://b.example", PathMatchType: "exact", MatchPath: ptr("/x"),
			RewritePath: ptr("/y"), RewritePathType: ptr("exact"), Permanent: true, Enabled: false,
			BaseDomain: ptr("example.com"),
		},
		{RedirectID: 3, NiceID: "c", Name: "C", DomainID: ptr("dom1"), DestinationHost: "https://c.example", PathMatchType: "prefix", Enabled: true},
	}
}

func ids(items []RedirectItemModel) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.ID.ValueString())
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBuildRedirectItemsFilters(t *testing.T) {
	null64, nullS, nullB := types.Int64Null(), types.StringNull(), types.BoolNull()
	tests := []struct {
		name string
		rid  types.Int64
		did  types.String
		en   types.Bool
		want []string
	}{
		{"none", null64, nullS, nullB, []string{"1", "2", "3"}},
		{"unknown ignored", types.Int64Unknown(), types.StringUnknown(), types.BoolUnknown(), []string{"1", "2", "3"}},
		{"resource_id", types.Int64Value(10), nullS, nullB, []string{"1"}},
		{"resource_id no match", types.Int64Value(99), nullS, nullB, []string{}},
		{"domain_id", null64, types.StringValue("dom1"), nullB, []string{"2", "3"}},
		{"enabled true", null64, nullS, types.BoolValue(true), []string{"1", "3"}},
		{"enabled false", null64, nullS, types.BoolValue(false), []string{"2"}},
		{"domain AND enabled", null64, types.StringValue("dom1"), types.BoolValue(true), []string{"3"}},
		{"resource AND domain", types.Int64Value(10), types.StringValue("dom1"), nullB, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ids(buildRedirectItems(sampleRedirects(), tt.rid, tt.did, tt.en))
			if !equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildRedirectItemsMapping(t *testing.T) {
	items := buildRedirectItems(sampleRedirects(), types.Int64Null(), types.StringNull(), types.BoolNull())

	if got := items[0].Priority.ValueInt64(); got != 5 {
		t.Errorf("priority = %d, want 5", got)
	}
	if got := items[1].Priority.ValueInt64(); got != 100 {
		t.Errorf("null priority = %d, want 100", got)
	}
	// Joined fields null for the domain-attached redirect.
	b := items[1]
	if !b.ResourceName.IsNull() || !b.ResourceNiceID.IsNull() || !b.ResourceFullDomain.IsNull() || !b.ResourceID.IsNull() {
		t.Errorf("expected null resource fields on domain redirect: %+v", b)
	}
	if b.BaseDomain.ValueString() != "example.com" || b.Subdomain.ValueString() != "www" || !b.Permanent.ValueBool() {
		t.Errorf("unexpected mapping: %+v", b)
	}
	// Joined fields null for the resource-attached redirect.
	a := items[0]
	if !a.BaseDomain.IsNull() || !a.DomainID.IsNull() || !a.MatchPath.IsNull() || !a.RewritePathType.IsNull() {
		t.Errorf("expected null domain fields on resource redirect: %+v", a)
	}
	if a.ResourceID.ValueInt64() != 10 || a.ResourceName.ValueString() != "res" {
		t.Errorf("unexpected mapping: %+v", a)
	}
}
