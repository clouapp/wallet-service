package mocks

import (
	"bytes"
	"context"
	"crypto/rand"

	mpc "github.com/macrowallets/waas/app/services/mpc"
)

type MockMPCService struct {
	SignCalls              int
	ReconstructEd25519Fn   func(shareA, shareB []byte) ([]byte, error)
	ReconstructSecp256k1Fn func(shareA, shareB []byte) ([]byte, error)
}

func NewMockMPCService() *MockMPCService {
	return &MockMPCService{}
}

func (m *MockMPCService) Keygen(_ context.Context, curve mpc.Curve) (*mpc.KeygenResult, error) {
	shareA := make([]byte, 32)
	shareB := make([]byte, 32)
	chainCode := make([]byte, 32)
	if _, err := rand.Read(shareA); err != nil {
		return nil, err
	}
	if _, err := rand.Read(shareB); err != nil {
		return nil, err
	}
	if _, err := rand.Read(chainCode); err != nil {
		return nil, err
	}

	var pubKey []byte
	if curve == mpc.CurveEd25519 {
		pubKey = make([]byte, 32)
		if _, err := rand.Read(pubKey); err != nil {
			return nil, err
		}
	} else {
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
		ChainCode:      chainCode,
	}, nil
}

func (m *MockMPCService) Sign(_ context.Context, curve mpc.Curve, shareA, shareB []byte, inputs mpc.SignInputs) ([]byte, error) {
	m.SignCalls++
	sig := make([]byte, 64)
	_, err := rand.Read(sig)
	return sig, err
}

func (m *MockMPCService) ReconstructEd25519PrivateKey(shareA, shareB []byte) ([]byte, error) {
	if m.ReconstructEd25519Fn != nil {
		return m.ReconstructEd25519Fn(shareA, shareB)
	}
	privKey := make([]byte, 32)
	_, err := rand.Read(privKey)
	return privKey, err
}

func (m *MockMPCService) ReconstructSecp256k1PrivateKey(shareA, shareB []byte) ([]byte, error) {
	if m.ReconstructSecp256k1Fn != nil {
		return m.ReconstructSecp256k1Fn(shareA, shareB)
	}
	return bytes.Repeat([]byte{0x11}, 32), nil
}
