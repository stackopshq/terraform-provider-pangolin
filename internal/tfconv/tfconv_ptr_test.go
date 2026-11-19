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

func TestPtrFromKnownBool(t *testing.T) {
	if PtrFromKnownBool(types.BoolNull()) != nil {
		t.Error("null should give nil")
	}
	if PtrFromKnownBool(types.BoolUnknown()) != nil {
		t.Error("unknown should give nil")
	}
	if p := PtrFromKnownBool(types.BoolValue(false)); p == nil || *p {
		t.Errorf("false should give pointer to false, got %v", p)
	}
	if p := PtrFromKnownBool(types.BoolValue(true)); p == nil || !*p {
		t.Errorf("true should give pointer to true, got %v", p)
	}
}

func TestPtrFromKnownInt64AsInt(t *testing.T) {
	if PtrFromKnownInt64AsInt(types.Int64Null()) != nil {
		t.Error("null should give nil")
	}
	if PtrFromKnownInt64AsInt(types.Int64Unknown()) != nil {
		t.Error("unknown should give nil")
	}
	if p := PtrFromKnownInt64AsInt(types.Int64Value(0)); p == nil || *p != 0 {
		t.Errorf("0 should give pointer to 0, got %v", p)
	}
	if p := PtrFromKnownInt64AsInt(types.Int64Value(42)); p == nil || *p != 42 {
		t.Errorf("42 should give pointer to 42, got %v", p)
	}
}
