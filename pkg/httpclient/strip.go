package httpclient

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// WithoutURL drops the URL carried by a *url.Error, including one wrapped by
// another error. The operation and the inner error stay so errors.Is still
// matches context cancellation and similar sentinels. The URL is not logged.
func WithoutURL(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	leaked := urlErr.URL
	inner := WithoutURL(urlErr.Err)
	if inner == nil {
		if urlErr.Op == "" {
			return errors.New("request failed")
		}
		return errors.New(urlErr.Op)
	}
	if leaked != "" && strings.Contains(inner.Error(), leaked) {
		return fmt.Errorf("%s: %s", urlErr.Op, RedactURLText(inner.Error(), leaked))
	}
	return fmt.Errorf("%s: %w", urlErr.Op, inner)
}

// RedactURLText replaces rawURL in text. An empty rawURL leaves text unchanged.
func RedactURLText(text, rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if text == "" || rawURL == "" {
		return text
	}
	text = strings.ReplaceAll(text, rawURL, "[redacted]")
	trimmed := strings.TrimRight(rawURL, "/")
	if trimmed != "" && trimmed != rawURL {
		text = strings.ReplaceAll(text, trimmed, "[redacted]")
	}
	return text
}

// RedactURL removes a *url.Error URL and any remaining copy of rawURL from err.
// When the text did not contain rawURL, err is returned unchanged so errors.Is
// and errors.As keep working.
func RedactURL(err error, rawURL string) error {
	err = WithoutURL(err)
	if err == nil {
		return nil
	}
	message := err.Error()
	cleaned := RedactURLText(message, rawURL)
	if cleaned == message {
		return err
	}
	return errors.New(cleaned)
}
