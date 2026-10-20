package bitcoin

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/app/services/chain"
)

var _ chain.BlockHashReader = (*BitcoinLive)(nil)

// BlockHashAt is the hash (lowercase hex) of the block the network holds at height
// now, through the provider failover. A height a provider has no block for (past
// its tip) is an error.
func (a *BitcoinLive) BlockHashAt(ctx context.Context, height uint64) (string, error) {
	return failoverRead(ctx, a.chainProviders(), "block hash", func(p bitcoinProvider) (string, error) { return p.blockHash(ctx, height) })
}

func (p directProvider) blockHash(ctx context.Context, height uint64) (string, error) {
	if err := p.verifyNetwork(ctx); err != nil {
		return "", err
	}
	hash, err := p.readBlockHash(ctx, height)
	if err != nil {
		return "", fmt.Errorf("btc block %d hash: %w", height, err)
	}
	return normalizeBTCHash(hash, "block hash")
}

// readBlockHash asks the endpoint for the hash at height: Esplora's GET
// /block-height/:height or bitcoind's getblockhash.
func (p directProvider) readBlockHash(ctx context.Context, height uint64) (string, error) {
	if p.live.restAPI {
		body, err := p.live.esploraGet(ctx, fmt.Sprintf("/block-height/%d", height))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(body)), nil
	}
	var hash string
	if err := p.live.rpc.Call(ctx, "getblockhash", &hash, height); err != nil {
		return "", unsupportedWhenMethodNotFound(err)
	}
	return hash, nil
}

func (p *electrumProvider) blockHash(ctx context.Context, height uint64) (string, error) {
	return withSession(ctx, p, func(s *electrumSession) (string, error) { return headerHash(s, height) })
}

// headerHash is the hash of the server's block header at height: the double
// SHA-256 of its 80 bytes, byte-reversed.
func headerHash(s *electrumSession, height uint64) (string, error) {
	var headerHex string
	if err := s.call("blockchain.block.header", &headerHex, height); err != nil {
		return "", err
	}
	header, err := hex.DecodeString(headerHex)
	if err != nil || len(header) != blockHeaderBytes {
		return "", fmt.Errorf("electrum: header %d is not %d bytes of hex", height, blockHeaderBytes)
	}
	return reversedHex(hash256(header)), nil
}

func (p tatumProvider) blockHash(ctx context.Context, height uint64) (string, error) {
	return p.gateway.blockHash(ctx, height)
}
