package ingest

import (
	"encoding/json"
	"testing"
)

func TestAccepted_Keeps_TheOkBody(t *testing.T) {
	raw, err := json.Marshal(NewAccepted())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("wire = %s", raw)
	}
}
