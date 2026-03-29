package wallet

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/macrowallets/waas/tests/testutil"
)

func TestMain(m *testing.M) {
	// Boot Goravel once for all tests
	testutil.BootTest()
	os.Exit(m.Run())
}

const testPassphrase = "test-passphrase-long-enough"

var testAccountID = uuid.MustParse("00000000-0000-0000-0000-000000000099")

func newTestService(t *testing.T, registry *chain.Registry) *Service {
	t.Helper()
	return newTestServiceWithRepos(t, registry, nil, nil)
}

func newTestServiceWithRepos(t *testing.T, registry *chain.Registry, walletRepo repositories.WalletRepository, addressRepo repositories.AddressRepository) *Service {
	t.Helper()
	svc := NewService(
		registry,
		nil, // no redis in tests
		mocks.NewMockMPCService(),
		nil, // secretsManager concrete type replaced by mock interface below
		walletRepo,
		addressRepo,
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
	_, err := s.service.CreateWallet(ctx, testAccountID, "eth", "Test", "short")
	s.Error(err)
	s.Contains(err.Error(), "passphrase must be at least 12 characters")
}

func (s *WalletUnitTestSuite) TestCreateWallet_UnknownChain() {
	ctx := context.Background()
	_, err := s.service.CreateWallet(ctx, testAccountID, "unknown_chain", "Test", testPassphrase)
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
	s.service = newTestServiceWithRepos(s.T(), s.registry, repositories.NewWalletRepository(), repositories.NewAddressRepository())
}

func (s *WalletServiceTestSuite) TestCreateWallet_Success() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, testAccountID, "eth", "Ethereum Wallet", testPassphrase)
	s.Require().NoError(err)
	s.Equal("eth", result.Wallet.Chain)
	s.Equal("Ethereum Wallet", result.Wallet.Label)
	s.NotEmpty(result.Wallet.DepositAddress)
	s.NotEmpty(result.Wallet.MPCPublicKey)
	s.NotEmpty(result.Wallet.MPCCustomerShare)
	s.NotEmpty(result.Wallet.MPCSecretARN)
	s.Equal("secp256k1", result.Wallet.MPCCurve)
	s.Equal(&testAccountID, result.Wallet.AccountID)
}

func (s *WalletServiceTestSuite) TestCreateWallet_SolanaUsesEd25519() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, testAccountID, "sol", "Solana Wallet", testPassphrase)
	s.Require().NoError(err)
	s.Equal("sol", result.Wallet.Chain)
	s.Equal("ed25519", result.Wallet.MPCCurve)
	s.NotEmpty(result.Wallet.DepositAddress)
}


func (s *WalletServiceTestSuite) TestCreateWallet_Success_ReturnsKeycardData() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, testAccountID, "eth", "Ethereum Wallet", testPassphrase)
	s.Require().NoError(err)

	// Wallet fields
	s.Equal("eth", result.Wallet.Chain)
	s.Equal("Ethereum Wallet", result.Wallet.Label)
	s.Equal("pending", result.Wallet.Status)
	s.NotEmpty(result.Wallet.DepositAddress)
	s.NotEmpty(result.Wallet.MPCPublicKey)
	s.NotEmpty(result.Wallet.MPCCustomerShare)
	s.NotEmpty(result.Wallet.MPCSecretARN)
	s.Equal("secp256k1", result.Wallet.MPCCurve)
	s.NotNil(result.Wallet.ActivationCode)
	s.Len(*result.Wallet.ActivationCode, 6)

	// Keycard fields
	s.NotEmpty(result.EncryptedUserKey)
	s.NotEmpty(result.ServicePublicKey)
	s.NotEmpty(result.EncryptedPasscode)
	s.NotEmpty(result.ActivationCode)
	s.Len(result.ActivationCode, 6)
	s.Regexp(`^\d{6}$`, result.ActivationCode)
}

func (s *WalletServiceTestSuite) TestCreateWallet_NormalisesChainID() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, testAccountID, "ETH", "Test", testPassphrase)
	s.Require().NoError(err)
	s.Equal("eth", result.Wallet.Chain)
}

func (s *WalletServiceTestSuite) TestCreateWallet_MATICNormalisedToPolygon() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	// register polygon chain
	s.registry.RegisterChain(mocks.NewMockChain("polygon"))
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, testAccountID, "MATIC", "Test", testPassphrase)
	s.Require().NoError(err)
	s.Equal("polygon", result.Wallet.Chain)
}

func (s *WalletServiceTestSuite) TestActivateWallet_Success() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, testAccountID, "eth", "Test", testPassphrase)
	s.Require().NoError(err)
	s.Equal("pending", result.Wallet.Status)

	activated, err := s.service.ActivateWallet(ctx, result.Wallet.ID, result.ActivationCode)
	s.Require().NoError(err)
	s.Equal("active", activated.Status)
	s.Nil(activated.ActivationCode)
}

func (s *WalletServiceTestSuite) TestActivateWallet_WrongCode() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, testAccountID, "eth", "Test", testPassphrase)
	s.Require().NoError(err)

	_, err = s.service.ActivateWallet(ctx, result.Wallet.ID, "000000")
	s.ErrorIs(err, ErrInvalidActivationCode)
}

func (s *WalletServiceTestSuite) TestActivateWallet_AlreadyActive() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, testAccountID, "eth", "Test", testPassphrase)
	s.Require().NoError(err)

	// Activate once
	_, err = s.service.ActivateWallet(ctx, result.Wallet.ID, result.ActivationCode)
	s.Require().NoError(err)

	// Activate again — expect error
	_, err = s.service.ActivateWallet(ctx, result.Wallet.ID, result.ActivationCode)
	s.ErrorIs(err, ErrWalletAlreadyActive)
}

func (s *WalletServiceTestSuite) TestActivateWallet_NotFound() {
	ctx := context.Background()
	_, err := s.service.ActivateWallet(ctx, uuid.New(), "123456")
	s.ErrorIs(err, ErrWalletNotFound)
}
