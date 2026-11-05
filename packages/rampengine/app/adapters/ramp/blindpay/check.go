package blindpay

import (
	"context"
	"net/http"
	"net/url"
)

// CheckResult is what a successful credentials check proves: this key reaches
// this instance.
type CheckResult struct {
	InstanceID string
}

// Check makes the cheapest authenticated call scoped to the instance — list
// one customer — so a wrong key and a wrong instance id both fail here, with
// the sentinel the classifier gives them, instead of on a user's first
// request. The answer itself is discarded: it can name a customer. The limit
// is the smallest value BlindPay's limit enum allows.
func (a *Adapter) Check(ctx context.Context) (CheckResult, error) {
	client, err := a.client(ctx)
	if err != nil {
		return CheckResult{}, err
	}
	if err := client.Do(ctx, Request{
		Method: http.MethodGet,
		Path:   "customers",
		Query:  url.Values{"limit": {"10"}},
	}, nil); err != nil {
		return CheckResult{}, err
	}
	return CheckResult{InstanceID: client.InstanceID()}, nil
}
