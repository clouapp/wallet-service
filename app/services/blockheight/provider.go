package blockheight

import "context"

type Provider interface {
	GetBlockHeight(ctx context.Context, chainID string) (uint64, error)
}
