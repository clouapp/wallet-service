package settings

import (
	"encoding/json"
	"testing"
)

func TestMailTestSent_Keeps_TheSentBody(t *testing.T) {
	raw, err := json.Marshal(NewMailTestSent())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"sent":true}` {
		t.Fatalf("wire = %s", raw)
	}
}
