package withdraw

import (
	"context"
	"errors"
	"testing"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestEstimateNativeFee_Prices_ANativeTransferToTheDestination(t *testing.T) {
	feeChain := newCreateChain(t, &fakeBroadcaster{}, "0.0004", nil)
	svc := &Service{registry: feeChain.registry}

	estimate, err := svc.EstimateNativeFee(context.Background(), "eth", "0xdest")
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Fee != "0.0004" || feeChain.last.To != "0xdest" || feeChain.last.Asset != "ETH" || feeChain.last.From != "" {
		t.Fatalf("estimate %+v request %+v", estimate, feeChain.last)
	}
}

func TestEstimateNativeFee_Reports_AnUnknownChainAndAFailedEstimateAsUnavailable(t *testing.T) {
	cause := errors.New("rpc down https://rpc.example/secret")
	feeChain := newCreateChain(t, &fakeBroadcaster{}, "", cause)

	if _, err := (&Service{registry: feeChain.registry}).EstimateNativeFee(context.Background(), "eth", "0xdest"); !errors.Is(err, ErrFeeEstimateUnavailable) {
		t.Fatalf("failed estimate err = %v", err)
	}
	if _, err := (&Service{registry: chain.NewRegistry()}).EstimateNativeFee(context.Background(), "nope", "0xdest"); !errors.Is(err, ErrFeeEstimateUnavailable) {
		t.Fatalf("unknown chain err = %v", err)
	}

	empty := mocks.NewMockChain("eth")
	empty.EstimateFeeFn = func(context.Context, types.TransferRequest) (*types.FeeEstimate, error) { return nil, nil }
	registry := chain.NewRegistry()
	registry.RegisterChain(empty)
	if _, err := (&Service{registry: registry}).EstimateNativeFee(context.Background(), "eth", "0xdest"); !errors.Is(err, ErrFeeEstimateUnavailable) {
		t.Fatalf("empty estimate err = %v", err)
	}
}
