package webhooks

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/services/webhook"
)

func TestRedeliverRequest_Rules_CheckTheShapeOfBothSelections(t *testing.T) {
	rules := (&RedeliverRequest{}).Rules(nil)
	want := map[string]string{
		"event_ids":   "array",
		"event_ids.*": "uuid",
		"since":       "rfc3339",
		"until":       "rfc3339",
		"types":       "array",
	}
	if len(rules) != len(want) {
		t.Fatalf("rules = %#v, want %#v", rules, want)
	}
	for field, rule := range want {
		if rules[field] != rule {
			t.Fatalf("rules[%s] = %q, want %q", field, rules[field], rule)
		}
	}
}

func TestRedeliverRequest_After_TurnsEventIDsIntoASelection(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	req := &RedeliverRequest{EventIDs: []string{first.String(), strings.ToUpper(second.String())}}

	if fields := req.After(nil); len(fields) != 0 {
		t.Fatalf("After = %v", fields)
	}
	selection := req.Selection()
	if len(selection.EventIDs) != 2 || selection.EventIDs[0] != first || selection.EventIDs[1] != second {
		t.Fatalf("event ids = %v", selection.EventIDs)
	}
	if !selection.Since.IsZero() || !selection.Until.IsZero() || len(selection.Types) != 0 {
		t.Fatalf("selection = %+v", selection)
	}
}

func TestRedeliverRequest_After_TurnsAWindowIntoASelection(t *testing.T) {
	req := &RedeliverRequest{Since: "2026-10-10T10:00:00Z", Until: "2026-10-10T12:00:00-03:00", Types: []string{"deposit.confirmed"}}

	if fields := req.After(nil); len(fields) != 0 {
		t.Fatalf("After = %v", fields)
	}
	selection := req.Selection()
	if !selection.Since.Equal(time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)) || !selection.Until.Equal(time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("window = %v .. %v", selection.Since, selection.Until)
	}
	if len(selection.Types) != 1 || selection.Types[0] != "deposit.confirmed" || len(selection.EventIDs) != 0 {
		t.Fatalf("selection = %+v", selection)
	}
}

func TestRedeliverRequest_After_RefusesAnAmbiguousOrOversizedSelection(t *testing.T) {
	tooMany := make([]string, webhook.MaxRedeliveryEvents+1)
	for i := range tooMany {
		tooMany[i] = uuid.NewString()
	}
	id := uuid.NewString()
	cases := []struct {
		name  string
		req   RedeliverRequest
		field string
	}{
		{"nothing", RedeliverRequest{}, "event_ids"},
		{"ids and since", RedeliverRequest{EventIDs: []string{id}, Since: "2026-10-10T10:00:00Z"}, "event_ids"},
		{"ids and until", RedeliverRequest{EventIDs: []string{id}, Until: "2026-10-10T10:00:00Z"}, "event_ids"},
		{"ids and types", RedeliverRequest{EventIDs: []string{id}, Types: []string{"deposit.confirmed"}}, "event_ids"},
		{"until without since", RedeliverRequest{Until: "2026-10-10T10:00:00Z"}, "event_ids"},
		{"too many ids", RedeliverRequest{EventIDs: tooMany}, "event_ids"},
		{"unknown type", RedeliverRequest{Since: "2026-10-10T10:00:00Z", Types: []string{"deposit.credited"}}, "types"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := c.req
			fields := req.After(nil)
			if len(fields) != 1 || len(fields[c.field]) != 1 || fields[c.field][0] == "" {
				t.Fatalf("After = %v, want one message on %s", fields, c.field)
			}
		})
	}
}
