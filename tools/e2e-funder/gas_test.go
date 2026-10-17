package main

import (
	"context"
	"errors"
	"math/big"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

type fakeGasEstimator struct {
	estimate uint64
	err      error
	calls    []ethereum.CallMsg
}

func (f *fakeGasEstimator) EstimateGas(_ context.Context, call ethereum.CallMsg) (uint64, error) {
	f.calls = append(f.calls, call)
	return f.estimate, f.err
}

var (
	funderSender    = common.HexToAddress("0xe3c1d504c4633521197ffa736b2c9a7f93478076")
	funderRecipient = common.HexToAddress("0xa9c37e6b4fd689a1daea4a144a45cfbb826ff80a")
)

func TestNative_Transfer_GasLimitPadsTheNodeEstimate(t *testing.T) {
	t.Parallel()

	// Sepolia, 2026-10-18: 0.002 ETH to an account that does not exist yet.
	estimator := &fakeGasEstimator{estimate: 207_391}
	value := big.NewInt(2_000_000_000_000_000)

	limit, err := nativeTransferGasLimit(context.Background(), estimator, funderSender, funderRecipient, value)
	if err != nil {
		t.Fatal(err)
	}
	if limit != 248_869 {
		t.Fatalf("limit = %d, want 207391 × 120%% = 248869", limit)
	}
	if len(estimator.calls) != 1 {
		t.Fatalf("expected one estimate, got %d", len(estimator.calls))
	}
	call := estimator.calls[0]
	if call.From != funderSender || call.To == nil || *call.To != funderRecipient || call.Value.Cmp(value) != 0 {
		t.Fatalf("the estimate must be the real transfer, got %+v", call)
	}
}

func TestNative_Transfer_GasLimitFailsWithoutAnEstimate(t *testing.T) {
	t.Parallel()

	for name, estimator := range map[string]*fakeGasEstimator{
		"node error":    {err: errors.New("insufficient funds for transfer")},
		"zero estimate": {estimate: 0},
	} {
		if _, err := nativeTransferGasLimit(context.Background(), estimator, funderSender, funderRecipient, big.NewInt(1)); err == nil {
			t.Fatalf("%s: expected an error instead of a fixed limit", name)
		}
	}
}
