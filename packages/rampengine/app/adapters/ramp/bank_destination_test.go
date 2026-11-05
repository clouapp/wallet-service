package ramp_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

func TestOfframpQuoteRequest_ValidateDestination(t *testing.T) {
	pixKey := ramp.PixDestination{PixKey: "user@example.com", PixKeyType: "email"}
	ach := &ramp.BankDestination{Rail: ramp.RailACHPayout, Currency: "usd", ExternalAccountID: "ba_000000000001"}

	cases := []struct {
		name    string
		rail    ramp.Rail
		request ramp.OfframpQuoteRequest
		valid   bool
	}{
		{"pix rail with a pix key", ramp.RailPixPayout, ramp.OfframpQuoteRequest{Destination: pixKey}, true},
		{"pix rail with a pix key and a registered account", ramp.RailPixPayout,
			ramp.OfframpQuoteRequest{Destination: ramp.PixDestination{PixKey: "user@example.com", PixKeyType: "email", ExternalAccountID: "ba_000000000002"}}, true},
		{"pix rail carrying a bank account", ramp.RailPixPayout,
			ramp.OfframpQuoteRequest{Destination: pixKey, Bank: ach}, false},
		{"bank rail with its account", ramp.RailACHPayout, ramp.OfframpQuoteRequest{Bank: ach}, true},
		{"bank rail without an account", ramp.RailACHPayout, ramp.OfframpQuoteRequest{}, false},
		{"bank rail with an unregistered account", ramp.RailACHPayout,
			ramp.OfframpQuoteRequest{Bank: &ramp.BankDestination{Rail: ramp.RailACHPayout, Currency: "usd"}}, false},
		{"bank account registered for the other rail", ramp.RailWirePayout,
			ramp.OfframpQuoteRequest{Bank: ach}, false},
		{"bank rail also carrying a pix key", ramp.RailACHPayout,
			ramp.OfframpQuoteRequest{Destination: pixKey, Bank: ach}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.request.ValidateDestination(c.rail)
			if c.valid {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, ramp.ErrInvalidDestination)
		})
	}
}
