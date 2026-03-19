package mpc

import "context"

// Curve identifies the elliptic curve used for MPC operations.
type Curve string

const (
	CurveSecp256k1 Curve = "secp256k1"
	CurveEd25519   Curve = "ed25519"
)

// KeygenResult holds the outputs of a 2-party MPC key generation ceremony.
type KeygenResult struct {
	ShareA         []byte // customer's share — must be encrypted before storage
	ShareB         []byte // service's share — must be sent to Secrets Manager
	CombinedPubKey []byte // compressed public key (33 bytes secp256k1; 32 bytes ed25519)
}

// SignInputs carries all transaction data required for signing.
// Bitcoin may have multiple UTXO inputs (one hash per input).
// ETH and Solana have a single hash.
type SignInputs struct {
	TxHashes [][]byte // one entry per input
}

// Service is the MPC co-signing interface.
type Service interface {
	Keygen(ctx context.Context, curve Curve) (*KeygenResult, error)
	Sign(ctx context.Context, curve Curve, shareA, shareB []byte, inputs SignInputs) ([]byte, error)
}
