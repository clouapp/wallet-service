package responses

import (
	"testing"
)

func TestField_Messages_SortsRules(t *testing.T) {
	messages := FieldMessages(fakeErrors{all: map[string]map[string]string{
		"email": {"email": "Please provide a valid email address", "required": "Email address is required"},
	}})
	got := messages["email"]
	if len(got) != 2 || got[0] != "Please provide a valid email address" || got[1] != "Email address is required" {
		t.Fatalf("got %#v", got)
	}
}

type fakeErrors struct {
	all map[string]map[string]string
}

func (f fakeErrors) One(_ ...string) string            { return "" }
func (f fakeErrors) Get(key string) map[string]string  { return f.all[key] }
func (f fakeErrors) All() map[string]map[string]string { return f.all }
func (f fakeErrors) Has(key string) bool               { _, ok := f.all[key]; return ok }
