package keyexport

import (
	"context"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/app/models"
)

// ChainSource loads the chain row an export classifies.
type ChainSource interface {
	FindByID(ctx context.Context, id string) (*models.Chain, error)
}

// ChainNetworkResolver classifies a chain record like the wallet API does. The
// RPC URL is opened only for adapters whose network it names and never leaves
// this type; DecryptRPC returns the endpoint to call, and a failure leaves the
// network to the record's own fields.
type ChainNetworkResolver struct {
	Chains     ChainSource
	DecryptRPC func(sealed string) (string, error)
}

var _ NetworkResolver = ChainNetworkResolver{}

func (r ChainNetworkResolver) ResolveNetwork(ctx context.Context, chainID string) (Network, error) {
	if ctx == nil {
		return Network{}, errors.New("chain repository: context is required")
	}
	if r.Chains == nil {
		return Network{}, errors.New("chain repository is not configured")
	}
	record, err := r.Chains.FindByID(ctx, chainID)
	if err != nil || record == nil {
		return Network{}, fmt.Errorf("chain record %s not found", chainID)
	}
	resolved := record.ResolveNetwork(r.networkRPCURL(record))
	return Network{ChainID: record.ID, AdapterType: record.AdapterType, Name: resolved.Name, Testnet: resolved.Testnet}, nil
}

func (r ChainNetworkResolver) networkRPCURL(record *models.Chain) string {
	if r.DecryptRPC == nil {
		return ""
	}
	if record.AdapterType != models.AdapterTypeSolana && record.AdapterType != models.AdapterTypeBitcoin {
		return ""
	}
	rpcURL, err := r.DecryptRPC(record.RpcURL)
	if err != nil {
		return ""
	}
	return rpcURL
}
