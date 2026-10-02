package chain

import "errors"

// ErrUnsupportedChain is returned by chain adapters for operations that are not
// yet implemented for that chain. Currently applies to Solana and Bitcoin sweep
// methods — their base-level adapters are still POC; sweep implementation waits
// on a dedicated epic that ships base + sweep together.
var ErrUnsupportedChain = errors.New("unsupported chain operation in v1 EVM-only sweep")

// ErrGasEstimateFailed means the node could not size the transfer (for example
// eth_estimateGas reports it would revert). Nothing may be broadcast.
var ErrGasEstimateFailed = errors.New("gas estimate failed")
