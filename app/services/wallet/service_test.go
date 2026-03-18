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
	s.service = NewService(s.registry, nil) // no redis in tests
}

func (s *WalletServiceTestSuite) TestCreateWallet() {
	ctx := context.Background()

	w, err := s.service.CreateWallet(ctx, "eth", "Ethereum Wallet")
	s.Nil(err)
	s.Equal("eth", w.Chain)
	s.Equal("Ethereum Wallet", w.Label)
	s.Equal(0, w.AddressIndex)
	s.Equal("m/44'/60'/0'/0", w.DerivationPath)
}

func (s *WalletServiceTestSuite) TestGenerateAddress() {
	ctx := context.Background()

	w, _ := s.service.CreateWallet(ctx, "eth", "ETH")
	addr, err := s.service.GenerateAddress(ctx, w.ID, "user_123", `{"tier":"premium"}`)

	s.Nil(err)
	s.Equal("user_123", addr.ExternalUserID)
	s.Equal(0, addr.DerivationIndex)
	s.Equal("eth", addr.Chain)
	s.NotEmpty(addr.Address)
}
