package tfconv

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestPtrFromKnownString(t *testing.T) {
	if PtrFromKnownString(types.StringNull()) != nil {
		t.Error("null should give nil")
	}
	if PtrFromKnownString(types.StringUnknown()) != nil {
		t.Error("unknown should give nil")
	}
	if p := PtrFromKnownString(types.StringValue("")); p == nil || *p != "" {
		t.Errorf("empty string should give pointer to \"\", got %v", p)
	}
	if p := PtrFromKnownString(types.StringValue("*")); p == nil || *p != "*" {
		t.Errorf("\"*\" should give pointer to \"*\", got %v", p)
	}
}
