package swap

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Router follows administrative preference and only fails over while obtaining a quote.
type Router struct {
	factory *Factory
	policy  Policy
}

// NewRouter injects adapter construction and the current routing policy.
func NewRouter(factory *Factory, policy Policy) *Router {
	return &Router{factory: factory, policy: policy}
}

// Quote tries the preferred provider, then its fallback for eligible quote failures.
func (r *Router) Quote(ctx context.Context, request QuoteRequest) (*Quote, error) {
	if r == nil || r.policy == nil || r.factory == nil {
		return nil, ErrDisabled
	}
	config, err := r.policy.Resolve(ctx, request.From.Symbol, request.From.Chain)
	if err != nil {
		return nil, err
	}
	if !config.Enabled {
		return nil, ErrDisabled
	}
	request.SlippageBPS = config.SlippageBPS
	if err := request.Validate(); err != nil {
		return nil, err
	}
	order := []string{config.Preferred}
	if config.Preferred != ProviderLiFi && config.Preferred != ProviderSwapKit {
		return nil, ErrDisabled
	}
	if config.Fallback {
		next := ProviderLiFi
		if config.Preferred == next {
			next = ProviderSwapKit
		}
		order = append(order, next)
	}
	attempts := make([]Attempt, 0, len(order))
	for _, name := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		credential := config.Providers[name]
		if !credential.Enabled {
			attempts = append(attempts, Attempt{Provider: name, Outcome: "disabled"})
			continue
		}
		adapter, err := r.factory.Get(name)
		if err != nil {
			return nil, err
		}
		quote, err := adapter.Quote(ctx, request, credential)
		now := time.Now().UTC()
		if err == nil {
			err = quote.Validate(now)
			if err == nil && quote.Provider != name {
				err = ErrInvalidQuote
			}
		}
		if err != nil {
			attempts = append(attempts, Attempt{Provider: name, Outcome: quoteFailureCode(err)})
			if !quoteFallbackAllowed(err) {
				return nil, err
			}
			continue
		}
		quote.ID = uuid.New()
		quote.Request = request
		if config.QuoteTTL > 0 && quote.ExpiresAt.After(now.Add(config.QuoteTTL)) {
			quote.ExpiresAt = now.Add(config.QuoteTTL)
		}
		quote.Attempts = append(attempts, Attempt{Provider: name, Outcome: "selected"})
		return quote, nil
	}
	return nil, ErrNoRoute
}

func quoteFallbackAllowed(err error) bool {
	return errors.Is(err, ErrProviderUnavailable) || errors.Is(err, ErrUnsupported) || errors.Is(err, ErrNoRoute)
}

func quoteFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrProviderUnavailable):
		return "provider_unavailable"
	case errors.Is(err, ErrUnsupported):
		return "unsupported"
	case errors.Is(err, ErrNoRoute):
		return "no_route"
	case errors.Is(err, ErrProviderRejected):
		return "rejected"
	default:
		return "invalid_quote"
	}
}
