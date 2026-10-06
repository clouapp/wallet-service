package httpclient

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestWithout_URL_DropsTheRequestURL(t *testing.T) {
	const secret = "http://user:secret-key@rpc.example/secret-path"
	inner := fmt.Errorf("dial tcp: %w", context.DeadlineExceeded)
	err := WithoutURL(&url.Error{Op: "Post", URL: secret, Err: inner})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "secret-path") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedact_URL_DropsAnEchoedEndpoint(t *testing.T) {
	const secret = "http://rpc.example/v2/secret-key"
	err := RedactURL(fmt.Errorf("upstream said %s", secret), secret)
	if err == nil || strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "rpc.example") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedact_URL_KeepsAnUnrelatedError(t *testing.T) {
	err := fmt.Errorf("parse height: %w", context.Canceled)
	got := RedactURL(err, "http://rpc.example/v2/secret-key")
	if got != err || !errors.Is(got, context.Canceled) {
		t.Fatalf("got = %v", got)
	}
}
