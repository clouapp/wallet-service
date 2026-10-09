package withdraw

import (
	"context"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/pkg/types"
)

// ErrFeeEstimateUnavailable is a chain the registry cannot price: it is
// unknown, or its node failed to answer. The cause stays out of the message.
var ErrFeeEstimateUnavailable = errors.New("fee estimation unavailable")

// EstimateNativeFee prices a native-coin transfer to the destination on the
// chain, without a sender. It signs and broadcasts nothing.
func (s *Service) EstimateNativeFee(ctx context.Context, chainID, to string) (*types.FeeEstimate, error) {
	if s == nil || s.registry == nil {
		return nil, fmt.Errorf("%w: chain registry is required", ErrFeeEstimateUnavailable)
	}
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFeeEstimateUnavailable, err)
	}
	estimate, err := adapter.EstimateFee(ctx, types.TransferRequest{To: to, Asset: adapter.NativeAsset()})
	if err != nil || estimate == nil {
		return nil, fmt.Errorf("%w: %v", ErrFeeEstimateUnavailable, err)
	}
	return estimate, nil
}
