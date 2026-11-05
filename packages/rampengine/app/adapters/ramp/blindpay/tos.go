package blindpay

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

const (
	tosHost = "app.blindpay.com"
	tosPath = "/e/terms-of-service"
)

type tosRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	RedirectURL    string `json:"redirect_url,omitempty"`
	CustomerID     string `json:"customer_id,omitempty"`
}

type tosResponse struct {
	URL string `json:"url"`
}

// CreateTosSession opens BlindPay's hosted terms page. The session is keyed by
// the body's idempotency_key — BlindPay ignores the Idempotency-Key header
// under /e/ — so the key is a UUID, derived when the caller's is not one.
func (a *Adapter) CreateTosSession(ctx context.Context, ref ramp.CustomerRef, opts ramp.TosOpts) (*ramp.TosSession, error) {
	if opts.RedirectURI != "" {
		if err := checkRedirect(opts.RedirectURI); err != nil {
			return nil, err
		}
	}
	key := opts.IdempotencyKey
	if _, err := uuid.Parse(key); err != nil {
		key = uuid.NewSHA1(uuid.NameSpaceURL, []byte("blindpay:tos:"+opts.IdempotencyKey)).String()
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	var out tosResponse
	err = client.Do(ctx, Request{
		Method: http.MethodPost,
		Path:   "tos",
		Scope:  ScopeExternal,
		Body:   tosRequest{IdempotencyKey: key, RedirectURL: opts.RedirectURI, CustomerID: ref.ExternalID},
	}, &out)
	if err != nil {
		return nil, err
	}
	if !validTosURL(out.URL, key) {
		return nil, fmt.Errorf("blindpay: terms session answer is not the hosted terms page: %w", ramp.ErrProviderFailed)
	}
	return &ramp.TosSession{URL: out.URL, SessionID: key, AllowedHosts: []string{tosHost}}, nil
}

// checkRedirect accepts only an absolute https or xip URI without userinfo or
// fragment; the error is raised before anything is sent.
func checkRedirect(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || (u.Scheme != "https" && u.Scheme != "xip") ||
		u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Host == "" && u.Scheme == "https" {
		return fmt.Errorf("blindpay: terms redirect is not an absolute https or xip URI: %w", ramp.ErrRequestNotSent)
	}
	return nil
}

func validTosURL(raw, key string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != tosHost || u.Path != tosPath || u.User != nil {
		return false
	}
	q := u.Query()
	return q.Get("session_token") != "" && q.Get("idempotency_key") == key
}
