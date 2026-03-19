package mocks

import (
	"context"
	"crypto/rand"

	mpc "github.com/macromarkets/vault/app/services/mpc"
)

// MockMPCService is a minimal in-memory MPC service for tests.
// It generates random key material instead of running a real MPC ceremony.
type MockMPCService struct{}

func NewMockMPCService() *MockMPCService {
	return &MockMPCService{}
}

func (m *MockMPCService) Keygen(_ context.Context, curve mpc.Curve) (*mpc.KeygenResult, error) {
	shareA := make([]byte, 32)
	shareB := make([]byte, 32)
	if _, err := rand.Read(shareA); err != nil {
		return nil, err
	}
	if _, err := rand.Read(shareB); err != nil {
		return nil, err
	}

	var pubKey []byte
	if curve == mpc.CurveEd25519 {
		// 32-byte ed25519 public key
		pubKey = make([]byte, 32)
		if _, err := rand.Read(pubKey); err != nil {
			return nil, err
		}
	} else {
		// 33-byte compressed secp256k1 public key (prefix 0x02 + 32 bytes)
		pubKey = make([]byte, 33)
		pubKey[0] = 0x02
		if _, err := rand.Read(pubKey[1:]); err != nil {
			return nil, err
		}
	}

	return &mpc.KeygenResult{
		ShareA:         shareA,
		ShareB:         shareB,
		CombinedPubKey: pubKey,
	}, nil
}

func (m *MockMPCService) Sign(_ context.Context, curve mpc.Curve, shareA, shareB []byte, inputs mpc.SignInputs) ([]byte, error) {
	sig := make([]byte, 64)
	_, err := rand.Read(sig)
	return sig, err
}
