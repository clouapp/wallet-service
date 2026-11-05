package ramp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type standingAdapter struct {
	realAdapter
}

func (standingAdapter) ProvisionVirtualAccount(context.Context, CustomerRef, VirtualAccountRequest) (*VirtualAccount, error) {
	return nil, nil
}

func (standingAdapter) GetVirtualAccount(context.Context, CustomerRef, string) (*VirtualAccount, error) {
	return nil, nil
}

func (standingAdapter) UpdateVirtualAccount(context.Context, CustomerRef, string, VirtualAccountUpdate) (*VirtualAccount, error) {
	return nil, nil
}

func (standingAdapter) GetDeposit(context.Context, CustomerRef, string, string) (*VirtualAccountDeposit, error) {
	return nil, nil
}

// The refusal keeps the sentinel wrapped: the client controllers map it with
// errors.Is, and the router fails over on it.
func TestStandingAccount(t *testing.T) {
	standing, err := StandingAccount(standingAdapter{realAdapter{code: ProviderBridge}})
	require.NoError(t, err)
	require.Equal(t, ProviderBridge, standing.Code())

	_, err = StandingAccount(realAdapter{code: ProviderOzav})
	require.True(t, errors.Is(err, ErrStandingAccountUnsupported))
	require.EqualError(t, err, `provider "ozav": `+ErrStandingAccountUnsupported.Error())
}
