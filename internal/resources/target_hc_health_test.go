package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// hcHealthCase describes one update as the framework hands it to the
// hc_health modifier: hc_enabled in the configuration, in the plan
// (before any attribute modifier ran) and in the prior state.
type hcHealthCase struct {
	configHCEnabled, planHCEnabled, priorHCEnabled types.Bool
	priorHealth                                    string
	planHealth                                     types.String
	noPriorState                                   bool
}

// runHCHealthModifier builds Config, Plan and State for the target
// schema and runs the hc_health modifier on them.
func runHCHealthModifier(t *testing.T, c hcHealthCase) types.String {
	t.Helper()
	ctx := context.Background()
	var sr resource.SchemaResponse
	(&TargetResource{}).Schema(ctx, resource.SchemaRequest{}, &sr)

	base := TargetResourceModel{
		ID: types.Int64Value(44), ResourceID: types.Int64Value(74), SiteID: types.Int64Value(6),
		IP: types.StringValue("paperless"), Port: types.Int64Value(8000),
		Method: types.StringValue("http"), Enabled: types.BoolValue(true),
		HCEnabled: c.priorHCEnabled, HCHealth: types.StringValue(c.priorHealth),
	}
	state := tfsdk.State{Schema: sr.Schema}
	if c.noPriorState {
		base.HCHealth = types.StringNull()
	} else if d := state.Set(ctx, &base); d.HasError() {
		t.Fatalf("state: %v", d)
	}

	planned := base
	planned.HCEnabled = c.planHCEnabled
	planned.HCHealth = c.planHealth
	plan := tfsdk.Plan{Schema: sr.Schema}
	if d := plan.Set(ctx, &planned); d.HasError() {
		t.Fatalf("plan: %v", d)
	}

	configured := base
	configured.HCEnabled = c.configHCEnabled
	configured.HCHealth = types.StringNull()
	// tfsdk.Config has no Set: build the object through a State.
	configState := tfsdk.State{Schema: sr.Schema}
	if d := configState.Set(ctx, &configured); d.HasError() {
		t.Fatalf("config: %v", d)
	}
	config := tfsdk.Config{Schema: sr.Schema, Raw: configState.Raw}

	req := planmodifier.StringRequest{
		Path: path.Root("hc_health"), Config: config, Plan: plan, State: state,
		PlanValue: planned.HCHealth, StateValue: base.HCHealth,
	}
	resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
	hcHealthPlanModifier{}.PlanModifyString(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("modifier: %v", resp.Diagnostics)
	}
	return resp.PlanValue
}

// Disabling the health check: Pangolin resets hcHealth to "unknown".
// Planning the prior "healthy"/"unhealthy" made the apply fail with
// "Provider produced inconsistent result after apply".
func TestHCHealthPlanModifier_DisablingPlansUnknownString(t *testing.T) {
	for _, prior := range []string{"healthy", "unhealthy", "unknown"} {
		got := runHCHealthModifier(t, hcHealthCase{
			configHCEnabled: types.BoolValue(false), planHCEnabled: types.BoolValue(false),
			priorHCEnabled: types.BoolValue(true), priorHealth: prior, planHealth: types.StringUnknown(),
		})
		if got.IsUnknown() || got.ValueString() != "unknown" {
			t.Errorf("prior %q: want \"unknown\", got %v", prior, got)
		}
	}
}

// hc_enabled left out of the configuration: the plan the modifier sees
// still holds it unknown, UseStateForUnknown resolves it to the prior
// false afterwards, and Update sends false.
func TestHCHealthPlanModifier_OmittedDisabledFollowsState(t *testing.T) {
	got := runHCHealthModifier(t, hcHealthCase{
		configHCEnabled: types.BoolNull(), planHCEnabled: types.BoolUnknown(),
		priorHCEnabled: types.BoolValue(false), priorHealth: "unknown", planHealth: types.StringUnknown(),
	})
	if got.IsUnknown() || got.ValueString() != "unknown" {
		t.Errorf("want \"unknown\", got %v", got)
	}
}

// hc_enabled configured from a value only known at apply: so is hc_health.
func TestHCHealthPlanModifier_UnknownConfigStaysUnknown(t *testing.T) {
	got := runHCHealthModifier(t, hcHealthCase{
		configHCEnabled: types.BoolUnknown(), planHCEnabled: types.BoolUnknown(),
		priorHCEnabled: types.BoolValue(false), priorHealth: "unknown", planHealth: types.StringUnknown(),
	})
	if !got.IsUnknown() {
		t.Errorf("want unknown (known after apply), got %v", got)
	}
}

// Enabled (or being turned on): only the server knows the answer
// ("unhealthy" when turned on, whatever the probe says otherwise).
func TestHCHealthPlanModifier_EnabledStaysUnknown(t *testing.T) {
	for _, prior := range []string{"healthy", "unknown"} {
		got := runHCHealthModifier(t, hcHealthCase{
			configHCEnabled: types.BoolValue(true), planHCEnabled: types.BoolValue(true),
			priorHCEnabled: types.BoolValue(true), priorHealth: prior, planHealth: types.StringUnknown(),
		})
		if !got.IsUnknown() {
			t.Errorf("prior %q: want unknown (known after apply), got %v", prior, got)
		}
	}
}

// No planned change: the framework hands over the state value, which
// must stay untouched so the plan remains empty.
func TestHCHealthPlanModifier_KnownValueUntouched(t *testing.T) {
	got := runHCHealthModifier(t, hcHealthCase{
		configHCEnabled: types.BoolValue(false), planHCEnabled: types.BoolValue(false),
		priorHCEnabled: types.BoolValue(true), priorHealth: "healthy", planHealth: types.StringValue("healthy"),
	})
	if got.ValueString() != "healthy" {
		t.Errorf("want state value kept, got %v", got)
	}
}

// Create: no prior state, value stays unknown until the server answers.
func TestHCHealthPlanModifier_CreateStaysUnknown(t *testing.T) {
	got := runHCHealthModifier(t, hcHealthCase{
		configHCEnabled: types.BoolValue(false), planHCEnabled: types.BoolValue(false),
		priorHCEnabled: types.BoolValue(false), planHealth: types.StringUnknown(), noPriorState: true,
	})
	if !got.IsUnknown() {
		t.Errorf("want unknown on create, got %v", got)
	}
}

// The schema must wire the modifier, not UseStateForUnknown.
func TestTargetSchema_HCHealthUsesHCHealthPlanModifier(t *testing.T) {
	var sr resource.SchemaResponse
	(&TargetResource{}).Schema(context.Background(), resource.SchemaRequest{}, &sr)
	attr, ok := sr.Schema.Attributes["hc_health"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("hc_health is not a StringAttribute")
	}
	if len(attr.PlanModifiers) != 1 {
		t.Fatalf("want exactly one plan modifier, got %d", len(attr.PlanModifiers))
	}
	if _, ok := attr.PlanModifiers[0].(hcHealthPlanModifier); !ok {
		t.Errorf("hc_health plan modifier is %T, want hcHealthPlanModifier", attr.PlanModifiers[0])
	}
}
