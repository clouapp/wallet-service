package pagination

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
)

// ErrInvalidCursor is a cursor EncodeCursor did not write.
var ErrInvalidCursor = errors.New("cursor is invalid")

// cursorPosition is the JSON inside a cursor: where the last row of a page sits in
// an (updated_at, id) keyset.
type cursorPosition struct {
	UpdatedAt string `json:"updated_at"`
	ID        string `json:"id"`
}

// EncodeCursor returns the opaque cursor of a keyset position: unpadded base64url of
// {"updated_at": RFC 3339 in UTC, "id": uuid}.
func EncodeCursor(updatedAt time.Time, id uuid.UUID) string {
	// Marshalling two strings cannot fail.
	raw, _ := json.Marshal(cursorPosition{
		UpdatedAt: updatedAt.UTC().Format(time.RFC3339Nano),
		ID:        id.String(),
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor returns the position EncodeCursor wrote, with the time in UTC.
// Anything else, including a nil id, is ErrInvalidCursor.
func DecodeCursor(cursor string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) == 0 {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var position cursorPosition
	if decoder.Decode(&position) != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	if _, trailing := decoder.Token(); !errors.Is(trailing, io.EOF) {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, position.UpdatedAt)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	id, err := uuid.Parse(position.ID)
	if err != nil || id == uuid.Nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	return updatedAt.UTC(), id, nil
}

// CursorResponse is the list envelope of a keyset page: Response at offset 0 plus
// next_cursor, which is null on the last page.
func CursorResponse(data any, total int64, limit int, nextCursor *string) http.Json {
	body := Response(data, total, limit, 0)
	body["next_cursor"] = nextCursor
	return body
}
