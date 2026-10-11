package pagination

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEncode_Cursor_RoundTripsThePositionInUTC(t *testing.T) {
	saoPaulo := time.FixedZone("BRT", -3*60*60)
	at := time.Date(2026, 10, 11, 9, 30, 15, 123456000, saoPaulo)
	id := uuid.MustParse("7d3c9f3e-2b1a-4c5d-8e6f-0a1b2c3d4e5f")

	cursor := EncodeCursor(at, id)

	if strings.ContainsAny(cursor, "+/=") {
		t.Fatalf("cursor %q is not unpadded base64url", cursor)
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if want := `{"updated_at":"2026-10-11T12:30:15.123456Z","id":"7d3c9f3e-2b1a-4c5d-8e6f-0a1b2c3d4e5f"}`; string(raw) != want {
		t.Fatalf("cursor body = %s, want %s", raw, want)
	}

	gotAt, gotID, err := DecodeCursor(cursor)
	if err != nil {
		t.Fatalf("DecodeCursor() error = %v", err)
	}
	if !gotAt.Equal(at) || gotAt.Location() != time.UTC {
		t.Fatalf("DecodeCursor() time = %v, want %v in UTC", gotAt, at)
	}
	if gotID != id {
		t.Fatalf("DecodeCursor() id = %s, want %s", gotID, id)
	}
}

func TestDecode_Cursor_RejectsWhatEncodeCursorDidNotWrite(t *testing.T) {
	encode := func(body string) string { return base64.RawURLEncoding.EncodeToString([]byte(body)) }
	id := "7d3c9f3e-2b1a-4c5d-8e6f-0a1b2c3d4e5f"
	cases := map[string]string{
		"empty":                "",
		"not base64url":        "not a cursor!",
		"padded base64":        encode(`{"updated_at":"2026-10-11T12:30:15Z","id":"`+id+`"} `) + "=",
		"not json":             encode("updated_at=2026-10-11"),
		"json array":           encode(`["2026-10-11T12:30:15Z","` + id + `"]`),
		"missing updated_at":   encode(`{"id":"` + id + `"}`),
		"missing id":           encode(`{"updated_at":"2026-10-11T12:30:15Z"}`),
		"timestamp not 3339":   encode(`{"updated_at":"2026-10-11 12:30:15","id":"` + id + `"}`),
		"id not a uuid":        encode(`{"updated_at":"2026-10-11T12:30:15Z","id":"42"}`),
		"nil id":               encode(`{"updated_at":"2026-10-11T12:30:15Z","id":"00000000-0000-0000-0000-000000000000"}`),
		"unknown field":        encode(`{"updated_at":"2026-10-11T12:30:15Z","id":"` + id + `","offset":10}`),
		"trailing data":        encode(`{"updated_at":"2026-10-11T12:30:15Z","id":"` + id + `"}{}`),
		"updated_at as number": encode(`{"updated_at":1760185815,"id":"` + id + `"}`),
	}
	for name, cursor := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := DecodeCursor(cursor); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("DecodeCursor(%q) error = %v, want ErrInvalidCursor", cursor, err)
			}
		})
	}
}

func TestCursor_Response_AddsNextCursorToTheListEnvelope(t *testing.T) {
	next := "abc"
	body := CursorResponse([]int{1}, 3, 1, &next)
	if len(body) != 5 || body["next_cursor"] != &next || body["offset"] != 0 || body["limit"] != 1 || body["total"] != int64(3) {
		t.Fatalf("CursorResponse() = %#v", body)
	}

	last := CursorResponse([]int{}, 0, 50, nil)
	value, present := last["next_cursor"]
	if !present || value != (*string)(nil) {
		t.Fatalf("last page next_cursor = %#v (present %v), want an explicit null", value, present)
	}
}
