package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// validateHTTPResourceConfig runs ValidateConfig against a config where only
// mode and protocol are set (nil means null) and returns the diagnostics.
func validateHTTPResourceConfig(t *testing.T, mode, protocol *string) (warnings, errors int) {
	t.Helper()
	ctx := context.Background()
	r := &HTTPResource{}
	var sresp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sresp)
	objType := sresp.Schema.Type().TerraformType(ctx).(tftypes.Object)

	vals := map[string]tftypes.Value{}
	for name, typ := range objType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	if mode != nil {
		vals["mode"] = tftypes.NewValue(tftypes.String, *mode)
	}
	if protocol != nil {
		vals["protocol"] = tftypes.NewValue(tftypes.String, *protocol)
	}

	req := resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: sresp.Schema, Raw: tftypes.NewValue(objType, vals)}}
	var resp resource.ValidateConfigResponse
	r.ValidateConfig(ctx, req, &resp)
	return resp.Diagnostics.WarningsCount(), resp.Diagnostics.ErrorsCount()
}

func TestHTTPResourceValidateConfig_ProtocolContradictsMode(t *testing.T) {
	for _, tc := range []struct {
		name           string
		mode, protocol *string
		wantWarnings   int
	}{
		{"protocol unset", strPtr("udp"), nil, 0},
		{"mode unset, protocol tcp", nil, strPtr("tcp"), 0},
		{"mode unset, protocol udp", nil, strPtr("udp"), 1},
		{"tcp/udp", strPtr("tcp"), strPtr("udp"), 1},
		{"udp/udp", strPtr("udp"), strPtr("udp"), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, e := validateHTTPResourceConfig(t, tc.mode, tc.protocol)
			if e != 0 {
				t.Fatalf("got %d errors, want none (a contradiction must only warn)", e)
			}
			if w != tc.wantWarnings {
				t.Errorf("got %d warnings, want %d", w, tc.wantWarnings)
			}
		})
	}
}
