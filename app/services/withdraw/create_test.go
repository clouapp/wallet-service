package withdraw

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const createTestPassphrase = "create-passphrase-0001"

func TestCreateUnknownChainIsNotAStoreFailure(t *testing.T) {
	registry := chain.NewRegistry()
	registry.RegisterChain(mocks.NewMockChain("eth"))
	svc := &Service{registry: registry}
	svc.UseCreate(memUsers{}, acceptTotp{}, &memWithdrawalRows{}, &memChains{decimals: 18})
	storeDown := errors.New("db down")
	_, missing := svc.Create(context.Background(), CreateInput{
		Wallet:             sealedCreateWallet(t, "nope", nil),
		DashboardUserID:    uuid.New(),
		TotpCode:           "000000",
		Passphrase:         createTestPassphrase,
		Amount:             "1",
		DestinationAddress: "0xdest",
		IdempotencyKey:     uuid.New().String(),
	})
	if !errors.Is(missing, chainregistry.ErrUnknownChain) || errors.Is(missing, storeDown) {
		t.Fatalf("missing chain err = %v", missing)
	}
	svc.registry = &refusingLookup{err: storeDown}
	_, failed := svc.Create(context.Background(), CreateInput{
		Wallet:             sealedCreateWallet(t, "eth", nil),
		DashboardUserID:    uuid.New(),
		TotpCode:           "000000",
		Passphrase:         createTestPassphrase,
		Amount:             "1",
		DestinationAddress: "0xdest",
		IdempotencyKey:     uuid.New().String(),
	})
	if !errors.Is(failed, storeDown) || errors.Is(failed, chainregistry.ErrUnknownChain) {
		t.Fatalf("store failure err = %v", failed)
	}
}

type refusingLookup struct {
	err error
}

func (r refusingLookup) Chain(string) (types.Chain, error) {
	return nil, r.err
}

func (refusingLookup) TokensForChain(string) []types.Token {
	return nil
}

func TestCreateStoresTheFeeAndDoesNotBroadcast(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	feeChain := newCreateChain(t, broadcaster, "0.00021", nil)
	rows := &memWithdrawalRows{}
	svc := newCreateService(t, feeChain.chain, rows, acceptTotp{}, &memChains{decimals: 18})
	userID := uuid.New()
	accountID := uuid.New()
	key := uuid.New()

	result, err := svc.Create(context.Background(), CreateInput{
		Wallet:             sealedCreateWallet(t, "eth", &accountID),
		DashboardUserID:    userID,
		TotpCode:           "000000",
		Passphrase:         createTestPassphrase,
		Amount:             "1",
		DestinationAddress: "0xdest",
		Note:               "rent",
		IdempotencyKey:     key.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if broadcaster.calls != 0 {
		t.Fatalf("broadcast calls = %d", broadcaster.calls)
	}
	if result.Replayed || result.Withdrawal == nil || result.IdempotencyKey != key.String() {
		t.Fatalf("result = %+v", result)
	}
	if result.Withdrawal.ID != key || result.Withdrawal.FeeEstimate != "0.00021" || result.Withdrawal.Status != models.WithdrawalStatusBroadcasting {
		t.Fatalf("row = %+v", result.Withdrawal)
	}
	if result.Withdrawal.CreatedBy == nil || *result.Withdrawal.CreatedBy != userID {
		t.Fatal("dashboard caller was not stored as created_by")
	}
	if result.Withdrawal.AccountID == nil || *result.Withdrawal.AccountID != accountID {
		t.Fatal("account was not stored")
	}
	if feeChain.requests != 1 || feeChain.last.From != "0xfrom" || feeChain.last.To != "0xdest" || feeChain.last.Asset != "ETH" || feeChain.last.Amount.String() != "1000000000000000000" {
		t.Fatalf("fee request = %+v calls=%d", feeChain.last, feeChain.requests)
	}
	if rows.creates != 1 || rows.withins != 1 || !rows.createdInside {
		t.Fatalf("creates = %d withins = %d inside = %v", rows.creates, rows.withins, rows.createdInside)
	}
}

func TestCreatePassesTheTokenToTheFeeEstimate(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	feeChain := newCreateChain(t, broadcaster, "21000", nil)
	feeChain.chain.NativeAssetVal = "ETH"
	registry := feeChain.registry
	registry.RegisterToken(types.Token{Symbol: "USDT", ChainID: "eth", Decimals: 6, Contract: "0xtoken"})
	rows := &memWithdrawalRows{}
	svc := newCreateService(t, feeChain.chain, rows, acceptTotp{}, &memChains{decimals: 18})
	svc.registry = registry

	_, err := svc.Create(context.Background(), CreateInput{
		Wallet:             sealedCreateWallet(t, "eth", nil),
		DashboardUserID:    uuid.New(),
		TotpCode:           "000000",
		Passphrase:         createTestPassphrase,
		Asset:              "usdt",
		Amount:             "1.5",
		DestinationAddress: "0xdest",
		IdempotencyKey:     uuid.New().String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if broadcaster.calls != 0 {
		t.Fatalf("broadcast calls = %d", broadcaster.calls)
	}
	if feeChain.last.Asset != "USDT" || feeChain.last.Token == nil || feeChain.last.Token.Contract != "0xtoken" || feeChain.last.Amount.String() != "1500000" {
		t.Fatalf("fee request = %+v", feeChain.last)
	}
	if rows.created[0].FeeEstimate != "21000" {
		t.Fatalf("fee = %s", rows.created[0].FeeEstimate)
	}
}

func TestCreateKeepsAZeroFeeWhenTheEstimateFails(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	feeChain := newCreateChain(t, broadcaster, "", errors.New("rpc down"))
	rows := &memWithdrawalRows{}
	svc := newCreateService(t, feeChain.chain, rows, acceptTotp{}, &memChains{decimals: 18})

	result, err := svc.Create(context.Background(), CreateInput{
		Wallet:             sealedCreateWallet(t, "eth", nil),
		DashboardUserID:    uuid.New(),
		TotpCode:           "000000",
		Passphrase:         createTestPassphrase,
		Amount:             "1",
		DestinationAddress: "0xdest",
		IdempotencyKey:     uuid.New().String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if broadcaster.calls != 0 || result.Withdrawal.FeeEstimate != "0" {
		t.Fatalf("fee %q broadcasts %d", result.Withdrawal.FeeEstimate, broadcaster.calls)
	}
}

func TestCreateRejectsReplayedTOTPBeforeTheRow(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	feeChain := newCreateChain(t, broadcaster, "1", nil)
	rows := &memWithdrawalRows{}
	svc := newCreateService(t, feeChain.chain, rows, &refuseTotp{err: authsvc.ErrInvalidSecondFactor}, &memChains{decimals: 18})

	_, err := svc.Create(context.Background(), CreateInput{
		Wallet:             sealedCreateWallet(t, "eth", nil),
		DashboardUserID:    uuid.New(),
		TotpCode:           "123456",
		Passphrase:         "wrong-passphrase-00",
		Amount:             "1",
		DestinationAddress: "0xdest",
		IdempotencyKey:     uuid.New().String(),
	})
	refusal, ok := err.(*CreateRefusal)
	if !ok || refusal.Status != CreateStatusUnauthorized || refusal.Message != "invalid 2FA code" {
		t.Fatalf("got %v", err)
	}
	if rows.creates != 0 || feeChain.requests != 0 || broadcaster.calls != 0 {
		t.Fatalf("creates %d fees %d broadcasts %d", rows.creates, feeChain.requests, broadcaster.calls)
	}
}

func TestCreateRequiresEnabledTOTPBeforeThePassphrase(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	feeChain := newCreateChain(t, broadcaster, "1", nil)
	rows := &memWithdrawalRows{}
	svc := newCreateService(t, feeChain.chain, rows, acceptTotp{}, &memChains{decimals: 18})
	svc.createUsers = disabledUsers{}

	_, err := svc.Create(context.Background(), CreateInput{
		Wallet:             sealedCreateWallet(t, "eth", nil),
		DashboardUserID:    uuid.New(),
		TotpCode:           "000000",
		Passphrase:         "wrong-passphrase-00",
		Amount:             "1",
		DestinationAddress: "0xdest",
		IdempotencyKey:     uuid.New().String(),
	})
	refusal, ok := err.(*CreateRefusal)
	if !ok || refusal.Status != CreateStatusForbidden || refusal.Message != "2FA must be enabled before withdrawing" {
		t.Fatalf("got %v", err)
	}
	if rows.creates != 0 || feeChain.requests != 0 || broadcaster.calls != 0 {
		t.Fatalf("creates %d fees %d broadcasts %d", rows.creates, feeChain.requests, broadcaster.calls)
	}
}

func TestCreateRejectsABadPassphraseBeforeTheRow(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	feeChain := newCreateChain(t, broadcaster, "1", nil)
	rows := &memWithdrawalRows{}
	locker := &recordingLocker{}
	svc := newCreateService(t, feeChain.chain, rows, acceptTotp{}, &memChains{decimals: 18})
	svc.locker = locker

	_, err := svc.Create(context.Background(), CreateInput{
		Wallet:             sealedCreateWallet(t, "eth", nil),
		DashboardUserID:    uuid.New(),
		TotpCode:           "000000",
		Passphrase:         "wrong-passphrase-00",
		Amount:             "1",
		DestinationAddress: "0xdest",
		IdempotencyKey:     uuid.New().String(),
	})
	refusal, ok := err.(*CreateRefusal)
	if !ok || refusal.Status != CreateStatusUnauthorized || refusal.Message != "invalid passphrase" {
		t.Fatalf("got %v", err)
	}
	if rows.creates != 0 || feeChain.requests != 0 || broadcaster.calls != 0 {
		t.Fatalf("creates %d fees %d broadcasts %d", rows.creates, feeChain.requests, broadcaster.calls)
	}
	if locker.incrKey == "" || locker.incrTTL.Seconds() != 60 {
		t.Fatal("failed passphrase was not counted")
	}
}

func TestCreateStopsAtThePassphraseAttemptCap(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	feeChain := newCreateChain(t, broadcaster, "1", nil)
	rows := &memWithdrawalRows{}
	locker := &recordingLocker{count: 5}
	svc := newCreateService(t, feeChain.chain, rows, acceptTotp{}, &memChains{decimals: 18})
	svc.locker = locker

	_, err := svc.Create(context.Background(), CreateInput{
		Wallet:             sealedCreateWallet(t, "eth", nil),
		DashboardUserID:    uuid.New(),
		TotpCode:           "000000",
		Passphrase:         createTestPassphrase,
		Amount:             "1",
		DestinationAddress: "0xdest",
		IdempotencyKey:     uuid.New().String(),
	})
	refusal, ok := err.(*CreateRefusal)
	if !ok || refusal.Status != CreateStatusTooManyRequests || refusal.Message != ErrTooManyAttempts.Error() {
		t.Fatalf("got %v", err)
	}
	if rows.creates != 0 || broadcaster.calls != 0 || locker.incrKey != "" {
		t.Fatalf("creates %d broadcasts %d incr %q", rows.creates, broadcaster.calls, locker.incrKey)
	}
}

func TestCreateSkipsTOTPForAnAccessTokenCaller(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	feeChain := newCreateChain(t, broadcaster, "1", nil)
	rows := &memWithdrawalRows{}
	totp := &refuseTotp{err: authsvc.ErrInvalidSecondFactor}
	svc := newCreateService(t, feeChain.chain, rows, totp, &memChains{decimals: 18})

	result, err := svc.Create(context.Background(), CreateInput{
		Wallet:             sealedCreateWallet(t, "eth", nil),
		Passphrase:         createTestPassphrase,
		Amount:             "1",
		DestinationAddress: "0xdest",
		IdempotencyKey:     uuid.New().String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if totp.calls != 0 || result.Withdrawal.CreatedBy != nil || broadcaster.calls != 0 {
		t.Fatalf("totp calls %d created_by %v broadcasts %d", totp.calls, result.Withdrawal.CreatedBy, broadcaster.calls)
	}
}

func TestCreateReturnsABroadcastRowWithoutAnotherWrite(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	feeChain := newCreateChain(t, broadcaster, "9", nil)
	existingID := uuid.New()
	wallet := sealedCreateWallet(t, "eth", nil)
	existing := &models.Withdrawal{
		ID: existingID, WalletID: wallet.ID, Status: models.WithdrawalStatusBroadcast,
		Amount: "1", DestinationAddress: "0xold", FeeEstimate: "3",
	}
	rows := &memWithdrawalRows{byID: map[uuid.UUID]*models.Withdrawal{existingID: existing}}
	svc := newCreateService(t, feeChain.chain, rows, acceptTotp{}, &memChains{decimals: 18})

	result, err := svc.Create(context.Background(), CreateInput{
		Wallet:             wallet,
		DashboardUserID:    uuid.New(),
		TotpCode:           "000000",
		Passphrase:         createTestPassphrase,
		Amount:             "2",
		DestinationAddress: "0xnew",
		IdempotencyKey:     existingID.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replayed || result.Withdrawal != existing || rows.creates != 0 || rows.retries != 0 || broadcaster.calls != 0 {
		t.Fatalf("replayed=%v creates=%d retries=%d broadcasts=%d", result.Replayed, rows.creates, rows.retries, broadcaster.calls)
	}
	if existing.Amount != "1" || existing.FeeEstimate != "3" {
		t.Fatal("replayed row was rewritten")
	}
}

func TestCreateRetriesAFailedRowAndKeepsTheOldViewFields(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	feeChain := newCreateChain(t, broadcaster, "9", nil)
	existingID := uuid.New()
	wallet := sealedCreateWallet(t, "eth", nil)
	existing := &models.Withdrawal{
		ID: existingID, WalletID: wallet.ID, Status: models.WithdrawalStatusFailed,
		Amount: "1", DestinationAddress: "0xold", FeeEstimate: "3",
	}
	rows := &memWithdrawalRows{byID: map[uuid.UUID]*models.Withdrawal{existingID: existing}}
	svc := newCreateService(t, feeChain.chain, rows, acceptTotp{}, &memChains{decimals: 18})

	result, err := svc.Create(context.Background(), CreateInput{
		Wallet:             wallet,
		DashboardUserID:    uuid.New(),
		TotpCode:           "000000",
		Passphrase:         createTestPassphrase,
		Amount:             "2",
		DestinationAddress: "0xnew",
		Note:               "again",
		IdempotencyKey:     existingID.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed || rows.creates != 0 || rows.retries != 1 || rows.withins != 1 || !rows.retriedInside || broadcaster.calls != 0 {
		t.Fatalf("replayed=%v creates=%d retries=%d withins=%d inside=%v broadcasts=%d", result.Replayed, rows.creates, rows.retries, rows.withins, rows.retriedInside, broadcaster.calls)
	}
	if rows.retryAmount != "2" || rows.retryDestination != "0xnew" || rows.retryFee != "9" || rows.retryNote != "again" {
		t.Fatalf("retry args = %s %s %s %s", rows.retryAmount, rows.retryDestination, rows.retryFee, rows.retryNote)
	}
	if result.Withdrawal.Status != models.WithdrawalStatusBroadcasting || result.Withdrawal.Amount != "1" || result.Withdrawal.FeeEstimate != "3" {
		t.Fatalf("in-memory row = %+v", result.Withdrawal)
	}
}

func TestCreateLeavesTheFailedRowWhenTheRetryTransactionFails(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	feeChain := newCreateChain(t, broadcaster, "9", nil)
	existingID := uuid.New()
	wallet := sealedCreateWallet(t, "eth", nil)
	existing := &models.Withdrawal{
		ID: existingID, WalletID: wallet.ID, Status: models.WithdrawalStatusFailed,
		Amount: "1", DestinationAddress: "0xold", FeeEstimate: "3",
	}
	rows := &memWithdrawalRows{
		byID:     map[uuid.UUID]*models.Withdrawal{existingID: existing},
		retryErr: errors.New("fail the withdrawal retry"),
	}
	svc := newCreateService(t, feeChain.chain, rows, acceptTotp{}, &memChains{decimals: 18})

	_, err := svc.Create(context.Background(), CreateInput{
		Wallet:             wallet,
		DashboardUserID:    uuid.New(),
		TotpCode:           "000000",
		Passphrase:         createTestPassphrase,
		Amount:             "2",
		DestinationAddress: "0xnew",
		IdempotencyKey:     existingID.String(),
	})
	rowErr, ok := err.(*CreateRowError)
	if !ok || rowErr.Endpoint != "retry_broadcasting_withdrawal" || rowErr.Err.Error() != "fail the withdrawal retry" {
		t.Fatalf("got %v", err)
	}
	if existing.Status != models.WithdrawalStatusFailed || existing.Amount != "1" || rows.creates != 0 || rows.withins != 1 || !rows.retriedInside || broadcaster.calls != 0 {
		t.Fatalf("status=%s amount=%s creates=%d withins=%d inside=%v broadcasts=%d", existing.Status, existing.Amount, rows.creates, rows.withins, rows.retriedInside, broadcaster.calls)
	}
}

func TestCreateRejectsANonUUIDIdempotencyKeyAfterThePassphrase(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	feeChain := newCreateChain(t, broadcaster, "1", nil)
	rows := &memWithdrawalRows{}
	svc := newCreateService(t, feeChain.chain, rows, acceptTotp{}, &memChains{decimals: 18})

	_, err := svc.Create(context.Background(), CreateInput{
		Wallet:             sealedCreateWallet(t, "eth", nil),
		DashboardUserID:    uuid.New(),
		TotpCode:           "000000",
		Passphrase:         createTestPassphrase,
		Amount:             "1",
		DestinationAddress: "0xdest",
		IdempotencyKey:     "not-a-uuid",
	})
	refusal, ok := err.(*CreateRefusal)
	if !ok || refusal.Status != CreateStatusBadRequest || refusal.Message != "idempotency_key must be a UUID" {
		t.Fatalf("got %v", err)
	}
	if rows.creates != 0 || broadcaster.calls != 0 {
		t.Fatalf("creates %d broadcasts %d", rows.creates, broadcaster.calls)
	}
}

func TestCreateAssignsAnIDWhenTheIdempotencyKeyIsEmpty(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	feeChain := newCreateChain(t, broadcaster, "1", nil)
	rows := &memWithdrawalRows{}
	svc := newCreateService(t, feeChain.chain, rows, acceptTotp{}, &memChains{decimals: 18})

	result, err := svc.Create(context.Background(), CreateInput{
		Wallet:             sealedCreateWallet(t, "eth", nil),
		DashboardUserID:    uuid.New(),
		TotpCode:           "000000",
		Passphrase:         createTestPassphrase,
		Amount:             "1",
		DestinationAddress: "0xdest",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Withdrawal.ID == uuid.Nil || result.IdempotencyKey != result.Withdrawal.ID.String() || broadcaster.calls != 0 {
		t.Fatalf("id %s key %s broadcasts %d", result.Withdrawal.ID, result.IdempotencyKey, broadcaster.calls)
	}
}

type fakeBroadcaster struct {
	calls int
}

type createChain struct {
	chain    *mocks.MockChain
	registry *chain.Registry
	last     types.TransferRequest
	requests int
}

func newCreateChain(t *testing.T, broadcaster *fakeBroadcaster, fee string, feeErr error) *createChain {
	t.Helper()
	mock := mocks.NewMockChain("eth")
	mock.NativeAssetVal = "ETH"
	holder := &createChain{chain: mock}
	mock.EstimateFeeFn = func(_ context.Context, req types.TransferRequest) (*types.FeeEstimate, error) {
		holder.requests++
		holder.last = req
		if feeErr != nil {
			return nil, feeErr
		}
		return &types.FeeEstimate{Fee: fee, FeeAsset: "ETH"}, nil
	}
	mock.BroadcastTransactionFn = func(context.Context, *types.SignedTx) (string, error) {
		broadcaster.calls++
		return "", errors.New("broadcast must not run")
	}
	registry := chain.NewRegistry()
	registry.RegisterChain(mock)
	holder.registry = registry
	return holder
}

func newCreateService(t *testing.T, mock *mocks.MockChain, rows WithdrawalRows, totp TotpCheck, chains ChainCatalog) *Service {
	t.Helper()
	registry := chain.NewRegistry()
	registry.RegisterChain(mock)
	svc := &Service{registry: registry}
	svc.UseCreate(memUsers{}, totp, rows, chains)
	return svc
}

func sealedCreateWallet(t *testing.T, chainID string, accountID *uuid.UUID) *models.Wallet {
	t.Helper()
	sealed, err := mpcpkg.EncryptShare([]byte("0123456789abcdef0123456789abcdef"), createTestPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	return &models.Wallet{
		ID:               uuid.New(),
		Chain:            chainID,
		AccountID:        accountID,
		MPCCustomerShare: hex.EncodeToString(sealed.Ciphertext),
		MPCShareIV:       hex.EncodeToString(sealed.IV),
		MPCShareSalt:     hex.EncodeToString(sealed.Salt),
		DepositAddress:   &models.Address{Address: "0xfrom"},
	}
}

type disabledUsers struct{}

func (disabledUsers) FindByID(context.Context, uuid.UUID) (*models.User, error) {
	return &models.User{TotpEnabled: false}, nil
}

type memUsers struct{}

func (memUsers) FindByID(context.Context, uuid.UUID) (*models.User, error) {
	return &models.User{TotpEnabled: true}, nil
}

type acceptTotp struct{}

func (acceptTotp) Verify(*models.User, string, string) error { return nil }

type refuseTotp struct {
	err   error
	calls int
}

func (r *refuseTotp) Verify(*models.User, string, string) error {
	if r == nil {
		return errors.New("totp checker is required")
	}
	r.calls++
	return r.err
}

type memChains struct {
	decimals int
	missing  bool
}

func (m memChains) FindByID(context.Context, string) (*models.Chain, error) {
	if m.missing {
		return nil, models.ErrRepositoryNotFound
	}
	return &models.Chain{NativeDecimals: m.decimals}, nil
}

type memWithdrawalRows struct {
	byID             map[uuid.UUID]*models.Withdrawal
	created          []*models.Withdrawal
	creates          int
	withins          int
	createdInside    bool
	inside           bool
	retries          int
	retriedInside    bool
	retryErr         error
	retryAmount      string
	retryDestination string
	retryFee         string
	retryNote        string
}

func (m *memWithdrawalRows) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("callback is required")
	}
	m.withins++
	m.inside = true
	defer func() { m.inside = false }()
	return fn(ctx)
}

func (m *memWithdrawalRows) FindByIDAndWallet(_ context.Context, withdrawalID, walletID uuid.UUID) (*models.Withdrawal, error) {
	if m.byID == nil {
		return nil, models.ErrRepositoryNotFound
	}
	row := m.byID[withdrawalID]
	if row == nil || row.WalletID != walletID {
		return nil, models.ErrRepositoryNotFound
	}
	return row, nil
}

func (m *memWithdrawalRows) Create(_ context.Context, withdrawal *models.Withdrawal) error {
	m.creates++
	if m.inside {
		m.createdInside = true
	}
	m.created = append(m.created, withdrawal)
	if m.byID == nil {
		m.byID = map[uuid.UUID]*models.Withdrawal{}
	}
	m.byID[withdrawal.ID] = withdrawal
	return nil
}

func (m *memWithdrawalRows) RetryBroadcast(_ context.Context, _ uuid.UUID, amount, destination, feeEstimate, note string) error {
	m.retries++
	if m.inside {
		m.retriedInside = true
	}
	m.retryAmount = amount
	m.retryDestination = destination
	m.retryFee = feeEstimate
	m.retryNote = note
	if m.retryErr != nil {
		return m.retryErr
	}
	return nil
}
