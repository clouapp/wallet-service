package wallets

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/chain"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	wallet "github.com/macrowallets/waas/app/services/wallet"
	ctltestutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	externalWalletsPath      = "/api/v1/wallets"
	adminWalletsPath         = "/v1/wallets"
	recoveryTestChain        = "eth"
	recoveryTestPassphrase   = "recovery-passphrase-long-enough"
	recoveryWrongPassphrase  = "not-the-recovery-passphrase"
	recoveryAdminPassword    = "correct-horse-battery"
	encryptedUserKeyField    = "encrypted_user_key"
	servicePublicKeyField    = "service_public_key"
	walletStatusPending      = "pending"
	activationCodeField      = "activation_code"
	encryptedPasscodeField   = "encrypted_passcode"
	adminCreateWalletField   = "wallet"
	accountHeaderName        = "X-Account-Id"
	accountMemberRoleOwner   = "owner"
	recoveryTestAccountLabel = "recovery-material"
)

var omittedSecretFields = [...]string{encryptedUserKeyField, encryptedPasscodeField}

// recordingMPCService captures the last keygen so tests can compare the
// response material against the shares the service actually produced.
type recordingMPCService struct {
	*mocks.MockMPCService
	lastKeygen *mpc.KeygenResult
}

func (r *recordingMPCService) Keygen(ctx context.Context, curve mpc.Curve) (*mpc.KeygenResult, error) {
	result, err := r.MockMPCService.Keygen(ctx, curve)
	if err == nil {
		r.lastKeygen = result
	}
	return result, err
}

// WalletRecoveryMaterialTestSuite checks that wallet creation does not return
// the customer share or the passphrase. The container's wallet service is
// swapped for one backed by in-memory MPC and Secrets Manager mocks, so
// creation runs through the real routes, middleware and controllers without
// RPC providers or LocalStack.
type WalletRecoveryMaterialTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
	mpcService    *recordingMPCService
	walletService *wallet.Service
}

func TestWalletRecoveryMaterialSuite(t *testing.T) {
	suite.Run(t, new(WalletRecoveryMaterialTestSuite))
}

func (s *WalletRecoveryMaterialTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())

	registry := chain.NewRegistry()
	registry.RegisterChain(mocks.NewMockChain(recoveryTestChain))
	s.mpcService = &recordingMPCService{MockMPCService: mocks.NewMockMPCService()}

	deps := container.Get()
	s.Require().NotNil(deps.WalletRepo)
	s.Require().NotNil(deps.AddressRepo)
	originalWalletService := deps.WalletService
	s.walletService = wallet.NewService(wallet.Deps{
		Registry:  registry,
		MPC:       s.mpcService,
		Secrets:   mocks.NewMockSecretsManager(),
		Wallets:   deps.WalletRepo,
		Addresses: deps.AddressRepo,
	})
	deps.WalletService = s.walletService
	s.T().Cleanup(func() { deps.WalletService = originalWalletService })
}

func (s *WalletRecoveryMaterialTestSuite) createExternalWallet(bearer string) (string, map[string]any) {
	body := fmt.Sprintf(`{"chain":%q,"label":"Recovery","passphrase":%q}`, recoveryTestChain, recoveryTestPassphrase)
	resp := ctltestutil.Post(s.T(), &s.TestCase, externalWalletsPath, body, bearer, nil)
	resp.AssertCreated()
	return s.decodeObject(resp.Content())
}

func (s *WalletRecoveryMaterialTestSuite) decodeObject(content string, err error) (string, map[string]any) {
	s.Require().NoError(err)
	var payload map[string]any
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	return content, payload
}

func (s *WalletRecoveryMaterialTestSuite) findWallet(walletID string) *models.Wallet {
	id, err := uuid.Parse(walletID)
	s.Require().NoError(err)
	s.Require().NotNil(s.walletService)
	stored, err := s.walletService.GetWallet(context.Background(), id)
	s.Require().NoError(err)
	s.Require().NotNil(stored)
	return stored
}

func (s *WalletRecoveryMaterialTestSuite) assertShareStaysOffTheWire(content string, payload map[string]any, stored *models.Wallet) {
	for _, field := range omittedSecretFields {
		s.NotContains(payload, field)
	}
	for _, secret := range []string{stored.MPCCustomerShare, stored.MPCShareIV, stored.MPCShareSalt} {
		s.NotContains(content, secret)
	}
}

func (s *WalletRecoveryMaterialTestSuite) assertNoShareLeak(content string) {
	s.Require().NotNil(s.mpcService.lastKeygen)
	for _, share := range [][]byte{s.mpcService.lastKeygen.ShareA, s.mpcService.lastKeygen.ShareB} {
		s.NotContains(content, hex.EncodeToString(share))
		s.NotContains(content, base64.StdEncoding.EncodeToString(share))
	}
}

func (s *WalletRecoveryMaterialTestSuite) TestExternalCreate_OmitsShareAndPassphrase() {
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	content, payload := s.createExternalWallet(bearer)

	walletID, ok := payload["id"].(string)
	s.Require().True(ok)
	s.Equal(recoveryTestChain, payload["chain"])
	s.Equal(walletStatusPending, payload["status"])
	s.Contains(payload, "deposit_address")
	s.NotContains(payload, activationCodeField)
	s.NotContains(payload, adminCreateWalletField)

	stored := s.findWallet(walletID)
	keygen := s.mpcService.lastKeygen
	s.Require().NotNil(keygen)
	s.Equal(hex.EncodeToString(keygen.CombinedPubKey), payload[servicePublicKeyField])
	s.Equal(stored.MPCPublicKey, payload[servicePublicKeyField])

	storedShareA, err := stored.DecryptShareA(recoveryTestPassphrase)
	s.Require().NoError(err)
	s.Equal(keygen.ShareA, storedShareA)

	_, err = stored.DecryptShareA(recoveryWrongPassphrase)
	s.ErrorIs(err, mpc.ErrInvalidPassphrase)

	s.assertShareStaysOffTheWire(content, payload, stored)
	s.assertNoShareLeak(content)
}

func (s *WalletRecoveryMaterialTestSuite) TestExternalCreate_MaterialIsNotReturnedAgain() {
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	createdContent, created := s.createExternalWallet(bearer)
	walletID := created["id"].(string)
	stored := s.findWallet(walletID)
	s.assertShareStaysOffTheWire(createdContent, created, stored)
	s.assertNoShareLeak(createdContent)

	getContent, fetched := s.decodeObject(ctltestutil.Get(s.T(), &s.TestCase, externalWalletsPath+"/"+walletID, bearer).AssertOk().Content())
	s.Equal(walletID, fetched["id"])
	s.assertShareStaysOffTheWire(getContent, fetched, stored)
	s.NotContains(fetched, servicePublicKeyField)

	listResp := ctltestutil.Get(s.T(), &s.TestCase, externalWalletsPath, bearer).AssertOk()
	listContent, err := listResp.Content()
	s.Require().NoError(err)
	var list struct {
		Data []map[string]any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(listContent), &list))
	s.Require().Len(list.Data, 1)
	s.Equal(walletID, list.Data[0]["id"])
	s.assertShareStaysOffTheWire(listContent, list.Data[0], stored)
	s.NotContains(list.Data[0], servicePublicKeyField)
}

func (s *WalletRecoveryMaterialTestSuite) TestAdminCreate_ResponseShapeUnchanged() {
	accountID, token := s.setupAdminSession()

	body := fmt.Sprintf(`{"chain":%q,"label":"Admin","passphrase":%q}`,
		recoveryTestChain, recoveryTestPassphrase)
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		WithHeader("Authorization", "Bearer "+token).
		WithHeader(accountHeaderName, accountID.String()).
		Post(adminWalletsPath, strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertCreated()
	content, payload := s.decodeObject(resp.Content())

	expectedKeys := []string{adminCreateWalletField, servicePublicKeyField, activationCodeField}
	s.ElementsMatch(expectedKeys, mapKeys(payload))

	createdWallet, ok := payload[adminCreateWalletField].(map[string]any)
	s.Require().True(ok)
	walletID, ok := createdWallet["id"].(string)
	s.Require().True(ok)
	s.NotEmpty(walletID)
	for _, field := range omittedSecretFields {
		s.NotContains(createdWallet, field)
		s.NotContains(payload, field)
	}

	stored := s.findWallet(walletID)
	storedShareA, err := stored.DecryptShareA(recoveryTestPassphrase)
	s.Require().NoError(err)
	s.Equal(s.mpcService.lastKeygen.ShareA, storedShareA)
	s.assertShareStaysOffTheWire(content, payload, stored)
	s.assertNoShareLeak(content)
}

func (s *WalletRecoveryMaterialTestSuite) setupAdminSession() (uuid.UUID, string) {
	hash, err := authsvc.NewService().HashPassword(recoveryAdminPassword)
	s.Require().NoError(err)
	userID := uuid.New()
	email := recoveryTestAccountLabel + "-" + userID.String()[:8] + "@example.com"
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)

	chainRecord, err := container.MustMake[*chainsvc.Service]().FindByID(context.Background(), recoveryTestChain)
	s.Require().NoError(err)
	s.Require().NotNil(chainRecord)
	environment := models.EnvironmentProd
	if chainRecord.IsTestnet {
		environment = models.EnvironmentTest
	}

	account := models.Account{ID: uuid.New(), Name: recoveryTestAccountLabel, Status: "active", Environment: environment}
	s.Require().NoError(facades.Orm().Query().Create(&account))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: account.ID, UserID: userID, Role: accountMemberRoleOwner,
	}))

	loginBody := fmt.Sprintf(`{"email":%q,"password":%q}`, email, recoveryAdminPassword)
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/login", strings.NewReader(loginBody))
	s.Require().NoError(err)
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var session struct {
		AccessToken string `json:"access_token"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &session))
	s.Require().NotEmpty(session.AccessToken)
	return account.ID, session.AccessToken
}

func mapKeys(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	return keys
}
