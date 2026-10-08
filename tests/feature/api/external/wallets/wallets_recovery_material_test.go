package wallets

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/container"
	dashwallets "github.com/macrowallets/waas/app/http/controllers/dashboard/wallets"
	extwallets "github.com/macrowallets/waas/app/http/controllers/external/wallets"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/chain"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
	wallet "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
	ctltestutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	externalWalletsPath      = "/api/v1/wallets"
	externalCreateWalletPath = "/api/v1/recovery-material/wallets"
	adminCreateWalletPath    = "/v1/recovery-material/wallets"
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

// recordingMPCService captures a copy of the last keygen so tests can compare
// the response material against the shares the service actually produced. It
// must be a copy: the wallet service zeroes the buffers it is handed once it
// has sealed them (61d4608).
type recordingMPCService struct {
	*mocks.MockMPCService
	lastKeygen *mpc.KeygenResult
}

func (r *recordingMPCService) Keygen(ctx context.Context, curve mpc.Curve) (*mpc.KeygenResult, error) {
	result, err := r.MockMPCService.Keygen(ctx, curve)
	if err == nil {
		r.lastKeygen = &mpc.KeygenResult{
			ShareA:         bytes.Clone(result.ShareA),
			ShareB:         bytes.Clone(result.ShareB),
			CombinedPubKey: bytes.Clone(result.CombinedPubKey),
			ChainCode:      bytes.Clone(result.ChainCode),
		}
	}
	return result, err
}

// WalletRecoveryMaterialTestSuite checks that wallet creation does not return
// the customer share or the passphrase. Create handlers are controllers built
// with wallet.NewService, so the chain, MPC and Secrets Manager ports are the
// test doubles. List and get stay on the booted routes, which read the wallet
// row and do not use that service.
type WalletRecoveryMaterialTestSuite struct {
	ctltestutil.HTTPSuite
	mpcService    *recordingMPCService
	walletService *wallet.Service
}

func TestWallet_Recovery_MaterialSuite(t *testing.T) {
	ctltestutil.RunSuite(t, new(WalletRecoveryMaterialTestSuite))
}

func (s *WalletRecoveryMaterialTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())

	registry := chain.NewRegistry()
	registry.RegisterChain(mocks.NewMockChain(recoveryTestChain))
	s.mpcService = &recordingMPCService{MockMPCService: mocks.NewMockMPCService()}

	deps := container.Get()
	s.Require().NotNil(deps.WalletRepo)
	s.Require().NotNil(deps.AddressRepo)
	s.walletService = wallet.NewService(wallet.Deps{
		Registry:  registry,
		MPC:       s.mpcService,
		Secrets:   mocks.NewMockSecretsManager(),
		Wallets:   deps.WalletRepo,
		Addresses: deps.AddressRepo,
	})
	s.registerCreateRoutes()
}

// recoveryRoutesOnce registers the create routes on the booted router. The
// handlers close over this suite and read walletService on each call, so a
// later test's service is the one that runs.
var recoveryRoutesOnce sync.Once

func (s *WalletRecoveryMaterialTestSuite) registerCreateRoutes() {
	recoveryRoutesOnce.Do(func() {
		service := func() *wallet.Service { return s.walletService }
		external := extwallets.NewWalletsController(extwallets.WalletsControllerDeps{
			Wallets:       container.MustMake[*walletrecords.Wallets](),
			Balances:      container.MustMake[*walletrecords.Balances](),
			Chains:        container.MustMake[*chainsvc.Service](),
			WalletService: service,
		})
		dashboard := dashwallets.NewWalletsController(dashwallets.WalletsControllerDeps{
			Wallets:       container.MustMake[*walletrecords.Wallets](),
			Members:       container.MustMake[*walletrecords.Members](),
			Balances:      container.MustMake[*walletrecords.Balances](),
			Chains:        container.MustMake[*chainsvc.Service](),
			WalletService: service,
		})
		accounts := container.MustMake[*accountsvc.Service]()
		noCache := middleware.CacheControl(0)
		facades.Route().Prefix("/api/v1/recovery-material").Middleware(
			middleware.APITokenAuth(accounts),
			noCache,
			middleware.APIScope(middleware.ScopeLookups{
				Transactions: container.MustMake[*walletrecords.Transactions](),
				Webhooks:     container.MustMake[*walletrecords.Webhooks](),
			}, middleware.PermWalletsCreate),
		).Post("/wallets", external.CreateWallet)
		facades.Route().Prefix("/v1/recovery-material/wallets").Middleware(
			middleware.SessionAuth(),
			middleware.AccountHeader(accounts),
			middleware.TOTPEnrollment(
				container.MustMake[*featuressvc.Service](),
				container.MustMake[*settingssvc.Service](),
			),
			noCache,
			middleware.RequireFundAction(middleware.FundCreateWallet),
		).Post("", dashboard.CreateWalletAdmin)
	})
}

func (s *WalletRecoveryMaterialTestSuite) createExternalWallet(bearer string) (string, map[string]any) {
	body := fmt.Sprintf(`{"chain":%q,"label":"Recovery","passphrase":%q}`, recoveryTestChain, recoveryTestPassphrase)
	resp := s.External(externalCreateWalletPath, ctltestutil.Token{Bearer: bearer}).Post(body)
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

func (s *WalletRecoveryMaterialTestSuite) TestExternal_Create_OmitsShareAndPassphrase() {
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

func (s *WalletRecoveryMaterialTestSuite) TestExternal_Create_MaterialIsNotReturnedAgain() {
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	createdContent, created := s.createExternalWallet(bearer)
	walletID := created["id"].(string)
	stored := s.findWallet(walletID)
	s.assertShareStaysOffTheWire(createdContent, created, stored)
	s.assertNoShareLeak(createdContent)

	getContent, fetched := s.decodeObject(s.External(externalWalletsPath+"/"+walletID, ctltestutil.Token{Bearer: bearer}).Get().AssertOk().Content())
	s.Equal(walletID, fetched["id"])
	s.assertShareStaysOffTheWire(getContent, fetched, stored)
	s.NotContains(fetched, servicePublicKeyField)

	listResp := s.External(externalWalletsPath, ctltestutil.Token{Bearer: bearer}).Get().AssertOk()
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

func (s *WalletRecoveryMaterialTestSuite) TestAdmin_Create_ResponseShapeUnchanged() {
	accountID, token := s.setupAdminSession()

	body := fmt.Sprintf(`{"chain":%q,"label":"Admin","passphrase":%q}`,
		recoveryTestChain, recoveryTestPassphrase)
	resp := s.Post(adminCreateWalletPath, ctltestutil.Session{AccessToken: token, AccountID: accountID.String()}, body)
	resp.AssertCreated()
	content, payload := s.decodeObject(resp.Content())

	expectedKeys := []string{adminCreateWalletField, servicePublicKeyField, activationCodeField}
	s.ElementsMatch(expectedKeys, mapKeys(payload))

	createdWallet, ok := payload[adminCreateWalletField].(map[string]any)
	s.Require().True(ok)
	walletID, ok := createdWallet["id"].(string)
	s.Require().True(ok)
	s.Require().NotEmpty(walletID)
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
	hash, err := authsvc.NewService(appfacades.Hash()).HashPassword(recoveryAdminPassword)
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
	resp := s.Post("/v1/auth/login", ctltestutil.Session{}, loginBody)
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
