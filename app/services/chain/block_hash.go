package chain

import "context"

// BlockHashReader is an adapter whose recorded blocks can be reorganized away (EVM,
// Bitcoin family, Solana, TRON): it reads the hash of the block its chain holds
// now at height. The confirmation tracker compares it with the block hash a
// deposit was recorded with before it confirms the deposit. A height the node has
// no block for (a lagging node, a slot without a block) is an error, never "": an
// error proves nothing, while a hash that differs is a reorganization. XRP
// validated ledgers are final, so its adapter does not implement it.
type BlockHashReader interface {
	BlockHashAt(ctx context.Context, height uint64) (string, error)
}
