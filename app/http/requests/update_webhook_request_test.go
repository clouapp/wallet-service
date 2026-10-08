package requests

import (
	"strings"
	"testing"
)

func TestUpdate_Webhook_RequestRulesMatchTheFieldsTheHandlerAccepts(t *testing.T) {
	rules := (&UpdateWebhookRequest{}).Rules(nil)
	want := map[string]string{
		"events":    "array",
		"is_active": "bool",
		"secret":    optionalString,
	}
	if len(rules) != len(want) {
		t.Fatalf("rules = %#v, want %#v", rules, want)
	}
	for field, rule := range want {
		if rules[field] != rule {
			t.Fatalf("rules[%s] = %q, want %q", field, rules[field], rule)
		}
	}
	if strings.Contains(rules["secret"], "required") {
		t.Fatal("a blank secret keeps the stored value")
	}
}
