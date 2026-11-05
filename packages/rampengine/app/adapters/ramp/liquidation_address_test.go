package ramp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type payoutAdapter struct {
	realAdapter
}

func (payoutAdapter) RegisterBankAccount(context.Context, CustomerRef, BankAccountRegistration) (*RegisteredBankAccount, error) {
	return nil, nil
}
func (payoutAdapter) DeactivateBankAccount(context.Context, CustomerRef, string) error {
	return nil
}
func (payoutAdapter) ProvisionLiquidationAddress(context.Context, CustomerRef, LiquidationAddressRequest) (*LiquidationAddress, error) {
	return nil, nil
}
func (payoutAdapter) UpdateLiquidationAddress(context.Context, CustomerRef, string, LiquidationAddressUpdate) (*LiquidationAddress, error) {
	return nil, nil
}
func (payoutAdapter) GetDrain(context.Context, CustomerRef, string, string) (*LiquidationDrain, error) {
	return nil, nil
}
func (payoutAdapter) SimulateLiquidationDeposit(context.Context, CustomerRef, string, SimulatedDeposit) (string, error) {
	return "", nil
}

// The refusal keeps the sentinel wrapped: the router fails over on it and the
// client controllers map it with errors.Is.
func TestPayoutAddress(t *testing.T) {
	payout, err := PayoutAddress(payoutAdapter{realAdapter{code: ProviderBridge}})
	require.NoError(t, err)
	require.Equal(t, ProviderBridge, payout.Code())

	_, err = PayoutAddress(realAdapter{code: ProviderOzav})
	require.True(t, errors.Is(err, ErrLiquidationAddressUnsupported))
	require.EqualError(t, err, `provider "ozav": `+ErrLiquidationAddressUnsupported.Error())
}

// A provider without the port was never asked anything, so the next one in the
// rail order may serve; any other failure stops the chain.
func TestFailoverOnUnsupportedCoversBothStandingPorts(t *testing.T) {
	require.True(t, FailoverOnUnsupported(ErrStandingAccountUnsupported))
	require.True(t, FailoverOnUnsupported(ErrLiquidationAddressUnsupported))
	require.False(t, FailoverOnUnsupported(ErrProviderFailed))
}
