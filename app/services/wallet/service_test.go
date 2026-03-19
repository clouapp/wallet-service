package wallet

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/macromarkets/vault/app/services/chain"
	"github.com/macromarkets/vault/tests/mocks"
	"github.com/macromarkets/vault/tests/testutil"
)

func TestMain(m *testing.M) {
	// Boot Goravel once for all tests
	testutil.BootTest()
	os.Exit(m.Run())
}

const testPassphrase = "test-passphrase-long-enough"

func newTestService(t *testing.T, registry *chain.Registry) *Service {
	t.Helper()
	svc := NewService(
		registry,
		nil, // no redis in tests
		mocks.NewMockMPCService(),
		nil, // secretsManager concrete type replaced by mock interface below
	)
	svc.secretsManager = mocks.NewMockSecretsManager()
	return svc
}

// ---------------------------------------------------------------------------
// Unit tests — no database required
// ---------------------------------------------------------------------------

type WalletUnitTestSuite struct {
	suite.Suite
	service  *Service
	registry *chain.Registry
}

func TestWalletUnitSuite(t *testing.T) {
	suite.Run(t, new(WalletUnitTestSuite))
}

func (s *WalletUnitTestSuite) SetupTest() {
	s.registry = chain.NewRegistry()
	s.registry.RegisterChain(mocks.NewMockChain("eth"))
	s.registry.RegisterChain(mocks.NewMockChain("btc"))
	s.registry.RegisterChain(mocks.NewMockChain("sol"))
	s.service = newTestService(s.T(), s.registry)
}

func (s *WalletUnitTestSuite) TestCreateWallet_PassphraseTooShort() {
	ctx := context.Background()
	_, err := s.service.CreateWallet(ctx, "eth", "Test", "short")
	s.Error(err)
	s.Contains(err.Error(), "passphrase must be at least 12 characters")
}

func (s *WalletUnitTestSuite) TestCreateWallet_UnknownChain() {
	ctx := context.Background()
	_, err := s.service.CreateWallet(ctx, "unknown_chain", "Test", testPassphrase)
	s.Error(err)
	s.Contains(err.Error(), "unknown chain")
}

func (s *WalletUnitTestSuite) TestGenerateAddress_ReturnsError() {
	ctx := context.Background()
	_, err := s.service.GenerateAddress(ctx, [16]byte{}, "user_123", `{}`)
	s.Error(err)
	s.Contains(err.Error(), "not supported for MPC wallets")
}

// ---------------------------------------------------------------------------
// Integration tests — require database
// ---------------------------------------------------------------------------

type WalletServiceTestSuite struct {
	suite.Suite
	service  *Service
	registry *chain.Registry
}

func TestWalletServiceSuite(t *testing.T) {
	suite.Run(t, new(WalletServiceTestSuite))
}

func (s *WalletServiceTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.registry = chain.NewRegistry()
	s.registry.RegisterChain(mocks.NewMockChain("eth"))
	s.registry.RegisterChain(mocks.NewMockChain("btc"))
	s.registry.RegisterChain(mocks.NewMockChain("sol"))
	s.service = newTestService(s.T(), s.registry)
}

func (s *WalletServiceTestSuite) TestCreateWallet_Success() {
	ctx := context.Background()

	w, err := s.service.CreateWallet(ctx, "eth", "Ethereum Wallet", testPassphrase)
	s.Require().NoError(err)
	s.Equal("eth", w.Chain)
	s.Equal("Ethereum Wallet", w.Label)
	s.NotEmpty(w.DepositAddress)
	s.NotEmpty(w.MPCPublicKey)
	s.NotEmpty(w.MPCCustomerShare)
	s.NotEmpty(w.MPCSecretARN)
	s.Equal("secp256k1", w.MPCCurve)
}

func (s *WalletServiceTestSuite) TestCreateWallet_SolanaUsesEd25519() {
	ctx := context.Background()

	w, err := s.service.CreateWallet(ctx, "sol", "Solana Wallet", testPassphrase)
	s.Require().NoError(err)
	s.Equal("sol", w.Chain)
	s.Equal("ed25519", w.MPCCurve)
	s.NotEmpty(w.DepositAddress)
}

func (s *WalletServiceTestSuite) TestCreateWallet_DuplicateChain() {
	ctx := context.Background()

	_, err := s.service.CreateWallet(ctx, "eth", "First", testPassphrase)
	s.Require().NoError(err)

	_, err = s.service.CreateWallet(ctx, "eth", "Second", testPassphrase)
	s.Error(err)
	s.Contains(err.Error(), "already exists")
}
