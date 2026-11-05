package middleware

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
)

// ReadAndRestoreBody fully reads req.Body and reassigns a fresh reader so the
// body can be consumed again downstream. The returned slice is the raw body.
//
// Webhook verification needs the exact bytes a provider signed, and the
// standard parsers consume the stream — so the body has to be read once, kept,
// and put back.
func ReadAndRestoreBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(raw))
	return raw, nil
}
