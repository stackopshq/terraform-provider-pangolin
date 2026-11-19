package datasources

import (
	"strings"
	"testing"

	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

func TestFindDomainByBaseDomain(t *testing.T) {
	domains := []client.Domain{
		{DomainID: "a1", BaseDomain: "example.com"},
		{DomainID: "b2", BaseDomain: "Other.Org"},
		{DomainID: "c3", BaseDomain: "dup.net"},
		{DomainID: "d4", BaseDomain: "DUP.net."},
	}
	tests := []struct {
		name, in, wantID, wantErr string
	}{
		{"exact", "example.com", "a1", ""},
		{"case", "OTHER.org", "b2", ""},
		{"trailing dot", "example.com.", "a1", ""},
		{"none", "missing.io", "", `no domain with base_domain "missing.io" in org`},
		{"multiple", "dup.net", "", "c3, d4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := findDomainByBaseDomain(domains, tt.in)
			if tt.wantErr == "" {
				if err != nil || id != tt.wantID {
					t.Fatalf("got %q, %v; want %q", id, err, tt.wantID)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got err %v; want containing %q", err, tt.wantErr)
			}
		})
	}
}
