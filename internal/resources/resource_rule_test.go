package resources

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

// resource_rule.go has no extracted helper - Create/Read/Update inline
// the mapping. These tests replay the inline logic against fixtures.

// buildRuleStateFromWire mirrors the Read() inline mapping.
func buildRuleStateFromWire(rule *client.ResourceRule) ResourceRuleModel {
	return ResourceRuleModel{
		ID:         types.Int64Value(int64(rule.RuleID)),
		ResourceID: types.Int64Value(int64(rule.ResourceID)),
		Action:     types.StringValue(rule.Action),
		Match:      types.StringValue(rule.Match),
		Value:      types.StringValue(rule.Value),
		Priority:   types.Int64Value(int64(rule.Priority)),
		Enabled:    types.BoolValue(rule.Enabled),
	}
}

// buildSetRuleRequestFromPlan mirrors the Create/Update inline mapping.
func buildSetRuleRequestFromPlan(plan ResourceRuleModel) *client.SetResourceRuleRequest {
	return &client.SetResourceRuleRequest{
		Action:   plan.Action.ValueString(),
		Match:    plan.Match.ValueString(),
		Value:    plan.Value.ValueString(),
		Priority: int(plan.Priority.ValueInt64()),
		Enabled:  plan.Enabled.ValueBool(),
	}
}

func TestResourceRule_WireToState(t *testing.T) {
	rule := &client.ResourceRule{
		RuleID:     9,
		ResourceID: 3,
		Action:     "ACCEPT",
		Match:      "CIDR",
		Value:      "10.0.0.0/8",
		Priority:   50,
		Enabled:    true,
	}
	m := buildRuleStateFromWire(rule)
	if m.ID.ValueInt64() != 9 || m.ResourceID.ValueInt64() != 3 {
		t.Errorf("ids: %d / %d", m.ID.ValueInt64(), m.ResourceID.ValueInt64())
	}
	if m.Action.ValueString() != "ACCEPT" || m.Match.ValueString() != "CIDR" {
		t.Errorf("action/match wrong")
	}
	if m.Value.ValueString() != "10.0.0.0/8" {
		t.Errorf("value = %q", m.Value.ValueString())
	}
	if m.Priority.ValueInt64() != 50 || !m.Enabled.ValueBool() {
		t.Errorf("priority/enabled wrong")
	}
}

func TestResourceRule_StateToRequest(t *testing.T) {
	plan := ResourceRuleModel{
		Action:   types.StringValue("DROP"),
		Match:    types.StringValue("IP"),
		Value:    types.StringValue("1.2.3.4"),
		Priority: types.Int64Value(1),
		Enabled:  types.BoolValue(false),
	}
	req := buildSetRuleRequestFromPlan(plan)
	if req.Action != "DROP" || req.Match != "IP" || req.Value != "1.2.3.4" ||
		req.Priority != 1 || req.Enabled != false {
		t.Errorf("request = %+v", req)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Every field is Required at the schema level, so the payload
	// is deterministic and can be exact-matched.
	want := `{"action":"DROP","match":"IP","value":"1.2.3.4","priority":1,"enabled":false}`
	if string(raw) != want {
		t.Errorf("payload = %s, want %s", raw, want)
	}
}

func TestResourceRule_RoundTrip(t *testing.T) {
	plan := ResourceRuleModel{
		ID:         types.Int64Value(1),
		ResourceID: types.Int64Value(2),
		Action:     types.StringValue("PASS"),
		Match:      types.StringValue("PATH"),
		Value:      types.StringValue("/admin"),
		Priority:   types.Int64Value(10),
		Enabled:    types.BoolValue(true),
	}
	req := buildSetRuleRequestFromPlan(plan)
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Emulate server round-trip: fill in the id fields the server
	// echoes and unmarshal back into ResourceRule.
	envelope := `{"ruleId":1,"resourceId":2,` + string(raw)[1:]
	var rule client.ResourceRule
	if err := json.Unmarshal([]byte(envelope), &rule); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	back := buildRuleStateFromWire(&rule)
	if back.Action.ValueString() != plan.Action.ValueString() {
		t.Errorf("Action lost")
	}
	if back.Match.ValueString() != plan.Match.ValueString() {
		t.Errorf("Match lost")
	}
	if back.Value.ValueString() != plan.Value.ValueString() {
		t.Errorf("Value lost")
	}
	if back.Priority.ValueInt64() != plan.Priority.ValueInt64() {
		t.Errorf("Priority lost")
	}
	if back.Enabled.ValueBool() != plan.Enabled.ValueBool() {
		t.Errorf("Enabled lost")
	}
}
