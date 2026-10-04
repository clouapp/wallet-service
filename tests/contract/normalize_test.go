package contract

import (
	"strings"
	"testing"
)

func TestNormalizer_NumbersUUIDsByFirstAppearanceAcrossBodies(t *testing.T) {
	normalizer := NewNormalizer()
	first := normalizer.Normalize(`{"id":"0F8FAD5B-D9CB-469F-A165-70867728950E","account_id":"7c9e6679-7425-40de-944b-e07fc1f90ae7"}`)
	second := normalizer.Normalize(`{"account":"7c9e6679-7425-40de-944b-e07fc1f90ae7","user":"0f8fad5b-d9cb-469f-a165-70867728950e"}`)

	if want := `{"id":"<uuid:1>","account_id":"<uuid:2>"}`; first != want {
		t.Errorf("first = %s, want %s", first, want)
	}
	if want := `{"account":"<uuid:2>","user":"<uuid:1>"}`; second != want {
		t.Errorf("second = %s, want %s", second, want)
	}
}

func TestNormalizer_ReplacesTimestampsAndJWTs(t *testing.T) {
	normalizer := NewNormalizer()
	got := normalizer.Normalize(`{"created_at":"2026-10-02T12:05:01.123456-03:00","at":"2026-10-02 15:05:01Z",` +
		`"access_token":"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJl","day":"2026-10-02"}`)
	want := `{"created_at":"<time>","at":"<time>","access_token":"<jwt>","day":"2026-10-02"}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNormalizer_MasksVolatileFieldsOfAnyScalarValue(t *testing.T) {
	normalizer := NewNormalizer("refresh_token", "secret")
	got := normalizer.Normalize(`{"refresh_token": "a1b2\"c3", "secret":12345, "secretive":"kept"}`)
	want := `{"refresh_token":"<refresh_token>", "secret":"<secret>", "secretive":"kept"}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNormalizer_LeavesStableBytesUntouched(t *testing.T) {
	body := `{"error":"invalid credentials","status":"ok","amount":"0.5","n":42}`
	if got := NewNormalizer("secret").Normalize(body); got != body {
		t.Errorf("got %s, want it unchanged", got)
	}
	if got := NewNormalizer().Normalize(""); got != "" {
		t.Errorf("got %q for an empty body", got)
	}
}

func TestRenderAndSections_RoundTrip(t *testing.T) {
	exchanges := []Exchange{
		{Step: "01 health", Method: "GET", Path: "/health", Status: 200, ContentType: "application/json", Body: `{"status":"ok"}`},
		{Step: "02 logout", Method: "POST", Path: "/v1/auth/logout", Status: 204, ContentType: "", Body: ""},
	}
	order, sections := Sections(Render(exchanges))

	wantOrder := []string{"01 health: GET /health", "02 logout: POST /v1/auth/logout"}
	if strings.Join(order, "|") != strings.Join(wantOrder, "|") {
		t.Fatalf("order = %v, want %v", order, wantOrder)
	}
	if want := "status: 200\ncontent-type: application/json\n{\"status\":\"ok\"}"; sections[order[0]] != want {
		t.Errorf("section = %q, want %q", sections[order[0]], want)
	}
}

func TestDiff_ReportsChangedAddedAndRemovedExchanges(t *testing.T) {
	base := []Exchange{
		{Step: "01 a", Method: "GET", Path: "/a", Status: 200, Body: `{"x":1}`},
		{Step: "02 b", Method: "GET", Path: "/b", Status: 200, Body: `{}`},
	}
	changed := []Exchange{
		{Step: "01 a", Method: "GET", Path: "/a", Status: 200, Body: `{"x": 1}`},
		{Step: "03 c", Method: "GET", Path: "/c", Status: 404, Body: `{}`},
	}
	differences := Diff(Render(base), Render(changed))
	if len(differences) != 3 {
		t.Fatalf("differences = %d (%v), want 3", len(differences), differences)
	}
	if !strings.HasPrefix(differences[0], "changed: 01 a") || !strings.HasPrefix(differences[1], "removed: 02 b") ||
		!strings.HasPrefix(differences[2], "added: 03 c") {
		t.Errorf("differences = %v", differences)
	}
	if got := Diff(Render(base), Render(base)); len(got) != 0 {
		t.Errorf("identical snapshots differ: %v", got)
	}
}

func TestJSONPath_ReadsNestedValues(t *testing.T) {
	document := []byte(`{"token":"t","metadata":{"id":"m"},"accounts":[{"id":"a0"},{"id":"a1"}],"n":3}`)
	for path, want := range map[string]string{"token": "t", "metadata.id": "m", "accounts.1.id": "a1", "n": "3"} {
		got, err := jsonPath(document, path)
		if err != nil || got != want {
			t.Errorf("jsonPath(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
	for _, path := range []string{"missing", "accounts.2.id", "accounts.x", "token.deeper"} {
		if _, err := jsonPath(document, path); err == nil {
			t.Errorf("jsonPath(%q) found a value, want an error", path)
		}
	}
	if _, err := jsonPath([]byte("not json"), "x"); err == nil {
		t.Error("jsonPath on a non-JSON body must fail")
	}
}
