package ingest

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestProcessTransfers_UnknownChain(t *testing.T) {
	reg := chain.NewRegistry()
	svc := &Service{registry: reg}
	err := svc.ProcessTransfers(t.Context(), "unknown_chain", nil)
	assert.ErrorIs(t, err, chainregistry.ErrUnknownChain)
}

func TestNewService_NilDeps(t *testing.T) {
	svc := NewService(Deps{})
	assert.NotNil(t, svc)
}

// InboundTransfer matches the deposit-scanner DetectedTransfer shape field-for-field
// (LogIndex is int in webhook payloads vs uint in types.DetectedTransfer).
func TestInboundTransfer_DetectedTransferMapping(t *testing.T) {
	ts := time.Unix(1700000000, 0).UTC()
	token := &types.Token{Symbol: "USDC", Name: "USD Coin", Contract: "0xtoken", Decimals: 6, ChainID: "ethereum"}
	amount := big.NewInt(42)
	in := providers.InboundTransfer{
		TxHash:      "0xabc",
		BlockNumber: 99,
		BlockHash:   "0xbeef",
		From:        "0xfrom",
		To:          "0xto",
		Amount:      amount,
		Asset:       "ETH",
		Token:       token,
		LogIndex:    7,
		Timestamp:   ts,
	}

	dt := types.DetectedTransfer{
		TxHash:      in.TxHash,
		BlockNumber: in.BlockNumber,
		BlockHash:   in.BlockHash,
		From:        in.From,
		To:          in.To,
		Amount:      in.Amount,
		Asset:       in.Asset,
		Token:       in.Token,
		LogIndex:    uint(in.LogIndex),
		Timestamp:   in.Timestamp,
	}

	assert.Equal(t, in.TxHash, dt.TxHash)
	assert.Equal(t, in.BlockNumber, dt.BlockNumber)
	assert.Equal(t, in.BlockHash, dt.BlockHash)
	assert.Equal(t, in.From, dt.From)
	assert.Equal(t, in.To, dt.To)
	assert.Equal(t, 0, in.Amount.Cmp(dt.Amount))
	assert.Equal(t, in.Asset, dt.Asset)
	assert.Equal(t, in.Token, dt.Token)
	assert.Equal(t, uint(in.LogIndex), dt.LogIndex)
	assert.True(t, in.Timestamp.Equal(dt.Timestamp))
}

func TestProcessTransfers_HumanUSDTUsesSeedDecimals(t *testing.T) {
	reg := chain.NewRegistry()
	mockChain := mocks.NewMockChain(models.ChainETH)
	mockChain.NativeAssetVal = models.NativeETH
	reg.RegisterChain(mockChain)
	reg.RegisterToken(types.Token{
		Symbol:   models.SymbolUSDT,
		ChainID:  models.ChainETH,
		Decimals: 6,
		Contract: models.USDTContractETH,
	})

	const to = "0xReceiver"
	walletID := uuid.New()
	addrs := &ingestAddressRepo{addr: &models.Address{
		ID:             uuid.New(),
		WalletID:       walletID,
		ExternalUserID: "user-1",
		Chain:          models.ChainETH,
		Address:        to,
	}}
	txs := &ingestTxRepo{}
	svc := NewService(Deps{
		Registry: reg,
		Webhook: webhook.NewService(webhook.Deps{
			Configs: &ingestWebhookConfigRepo{},
			Events:  &ingestWebhookEventRepo{},
		}),
		AddressRepo:  addrs,
		Transactions: txs,
	})

	err := svc.ProcessTransfers(t.Context(), models.ChainETH, []providers.InboundTransfer{{
		TxHash:        "0xtoken",
		To:            to,
		From:          "0xfrom",
		AmountIsHuman: true,
		HumanAmount:   "1.5",
		LogIndex:      1,
		Token: &types.Token{
			Contract: models.USDTContractETH,
			Symbol:   "",
		},
	}})
	require.NoError(t, err)
	require.Len(t, txs.created, 1)
	assert.Equal(t, models.SymbolUSDT, txs.created[0].Asset)
	assert.Equal(t, "1500000", txs.created[0].Amount)
}

func TestProcessTransfers_AddressSetKeepsTheMembershipDecision(t *testing.T) {
	const to = "0xReceiver"
	reg, addrs, txs := ingestFixture(to)
	member := &stubAddressSet{member: true}
	svc := NewService(Deps{Addresses: member, Registry: reg, AddressRepo: addrs, Transactions: txs})

	err := svc.ProcessTransfers(t.Context(), models.ChainETH, []providers.InboundTransfer{{
		TxHash: "0xnative",
		To:     to,
		From:   "0xfrom",
		Amount: big.NewInt(42),
	}})
	require.NoError(t, err)
	require.Len(t, txs.created, 1)
	assert.Equal(t, "42", txs.created[0].Amount)
	assert.Equal(t, "vault:addresses:"+models.ChainETH, member.key)
	assert.Equal(t, to, member.value)

	skipped := &ingestTxRepo{}
	absent := &stubAddressSet{}
	absentSvc := NewService(Deps{Addresses: absent, Registry: reg, AddressRepo: addrs, Transactions: skipped})
	err = absentSvc.ProcessTransfers(t.Context(), models.ChainETH, []providers.InboundTransfer{{
		TxHash: "0xskip",
		To:     to,
		From:   "0xfrom",
		Amount: big.NewInt(1),
	}})
	require.NoError(t, err)
	assert.Empty(t, skipped.created)

	failed := &ingestTxRepo{}
	broken := &stubAddressSet{err: assert.AnError}
	brokenSvc := NewService(Deps{Addresses: broken, Registry: reg, AddressRepo: addrs, Transactions: failed})
	err = brokenSvc.ProcessTransfers(t.Context(), models.ChainETH, []providers.InboundTransfer{{
		TxHash: "0xerr",
		To:     to,
		From:   "0xfrom",
		Amount: big.NewInt(1),
	}})
	require.NoError(t, err)
	assert.Empty(t, failed.created)
}

func ingestFixture(to string) (*chain.Registry, *ingestAddressRepo, *ingestTxRepo) {
	reg := chain.NewRegistry()
	mockChain := mocks.NewMockChain(models.ChainETH)
	mockChain.NativeAssetVal = models.NativeETH
	reg.RegisterChain(mockChain)
	addrs := &ingestAddressRepo{addr: &models.Address{
		ID:             uuid.New(),
		WalletID:       uuid.New(),
		ExternalUserID: "user-1",
		Chain:          models.ChainETH,
		Address:        to,
	}}
	return reg, addrs, &ingestTxRepo{}
}

type stubAddressSet struct {
	member bool
	err    error
	key    string
	value  any
}

func (s *stubAddressSet) SIsMember(_ context.Context, key string, member any) Membership {
	s.key = key
	s.value = member
	return stubMembership{member: s.member, err: s.err}
}

type stubMembership struct {
	member bool
	err    error
}

func (s stubMembership) Result() (bool, error) {
	return s.member, s.err
}

// A provider webhook for the sweep that moved funds into the base address is not a
// deposit of the base owner; a transfer from anywhere else to that address still is.
func TestProcessTransfers_SkipsSweepOfTheSameWallet(t *testing.T) {
	const (
		baseAddress = "0xBase"
		sweepHash   = "0xsweep"
		inboundHash = "0xinbound"
		baseOwner   = "system"
	)
	reg := chain.NewRegistry()
	mockChain := mocks.NewMockChain(models.ChainBase)
	mockChain.NativeAssetVal = models.NativeETH
	reg.RegisterChain(mockChain)

	walletID := uuid.New()
	addrs := &ingestAddressRepo{addr: &models.Address{
		ID:             uuid.New(),
		WalletID:       walletID,
		ExternalUserID: baseOwner,
		Chain:          models.ChainBase,
		Address:        baseAddress,
	}}
	txs := &ingestTxRepo{internalHashes: map[string]uuid.UUID{sweepHash: walletID}}
	svc := NewService(Deps{
		Registry: reg,
		Webhook: webhook.NewService(webhook.Deps{
			Configs: &ingestWebhookConfigRepo{},
			Events:  &ingestWebhookEventRepo{},
		}),
		AddressRepo:  addrs,
		Transactions: txs,
	})

	err := svc.ProcessTransfers(t.Context(), models.ChainBase, []providers.InboundTransfer{
		{TxHash: sweepHash, To: baseAddress, From: "0xchild", Amount: big.NewInt(304736467038418)},
		{TxHash: inboundHash, To: baseAddress, From: "0xexternal", Amount: big.NewInt(30000000000000)},
	})
	require.NoError(t, err)
	require.Len(t, txs.created, 1)
	assert.Equal(t, inboundHash, txs.created[0].TxHash)
	assert.Equal(t, baseOwner, txs.created[0].ExternalUserID)
}

type ingestAddressRepo struct {
	addr *models.Address
}

func (f *ingestAddressRepo) Create(addr *models.Address) error { return nil }
func (f *ingestAddressRepo) UpdateFields(id uuid.UUID, fields map[string]interface{}) error {
	return nil
}
func (f *ingestAddressRepo) CountByChainAndAddress(_ context.Context, chainID, address string) (int64, error) {
	if f.addr != nil && f.addr.Chain == chainID && f.addr.Address == address {
		return 1, nil
	}
	return 0, nil
}
func (f *ingestAddressRepo) FindByChainAndAddress(_ context.Context, chainID, address string) (*models.Address, error) {
	if f.addr != nil && f.addr.Chain == chainID && f.addr.Address == address {
		return f.addr, nil
	}
	return nil, nil
}
func (f *ingestAddressRepo) FindByChainAndAddressAndAccount(chainID, address string, accountID uuid.UUID) (*models.Address, error) {
	return nil, nil
}
func (f *ingestAddressRepo) FindByExternalUserID(externalUserID string) ([]models.Address, error) {
	return nil, nil
}
func (f *ingestAddressRepo) FindByExternalUserIDAndAccount(externalUserID string, accountID uuid.UUID) ([]models.Address, error) {
	return nil, nil
}
func (f *ingestAddressRepo) FindByID(id uuid.UUID) (*models.Address, error) { return nil, nil }
func (f *ingestAddressRepo) FindByWalletID(walletID uuid.UUID) ([]models.Address, error) {
	return nil, nil
}
func (f *ingestAddressRepo) MaxDerivationIndex(walletID uuid.UUID) (int, error) { return 0, nil }
func (f *ingestAddressRepo) PaginateByWalletID(walletID uuid.UUID, limit, offset int) ([]models.Address, int64, error) {
	return nil, 0, nil
}
func (f *ingestAddressRepo) PluckActiveAddresses(chainID string) ([]string, error) {
	return nil, nil
}

type ingestTxRepo struct {
	created []*models.Transaction
	// internalHashes lists the transaction hashes recorded as a sweep or gas seed.
	internalHashes map[string]uuid.UUID
}

func (f *ingestTxRepo) Create(_ context.Context, tx *models.Transaction) error {
	f.created = append(f.created, tx)
	return nil
}
func (f *ingestTxRepo) FindByID(id uuid.UUID) (*models.Transaction, error) { return nil, nil }
func (f *ingestTxRepo) FindByIDAndWallet(txID string, walletID uuid.UUID) (*models.Transaction, error) {
	return nil, nil
}
func (f *ingestTxRepo) FindByIdempotencyKey(key string) (*models.Transaction, error) {
	return nil, nil
}
func (f *ingestTxRepo) FindByWallet(walletID uuid.UUID, txType, status string, limit, offset int) ([]models.Transaction, int64, error) {
	return nil, 0, nil
}
func (f *ingestTxRepo) FindByChainAndTxHash(chainID, txHash string) (*models.Transaction, error) {
	return nil, nil
}
func (f *ingestTxRepo) CountByChainAndTxHash(chainID, txHash, txType string) (int64, error) {
	return 0, nil
}
func (f *ingestTxRepo) CountByChainTxHashAndLogIndex(_ context.Context, chainID, txHash string, logIndex int, txType string) (int64, error) {
	return 0, nil
}
func (f *ingestTxRepo) CountInternalTransfers(_ context.Context, chainID, txHash string, walletID uuid.UUID) (int64, error) {
	if owner, ok := f.internalHashes[txHash]; ok && owner == walletID {
		return 1, nil
	}
	return 0, nil
}
func (f *ingestTxRepo) FindPendingByChain(chainID string) ([]models.Transaction, error) {
	return nil, nil
}
func (f *ingestTxRepo) UpdateFields(id uuid.UUID, fields map[string]interface{}) error { return nil }
func (f *ingestTxRepo) List(chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	return nil, 0, nil
}
func (f *ingestTxRepo) ListForAccount(accountID uuid.UUID, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	return nil, 0, nil
}
func (f *ingestTxRepo) ListByWalletAndChain(walletID uuid.UUID, chainID string, limit, offset int) ([]models.Transaction, int64, error) {
	return nil, 0, nil
}

type ingestWebhookConfigRepo struct{}

func (f *ingestWebhookConfigRepo) Create(_ context.Context, cfg *models.WebhookConfig) error {
	return nil
}
func (f *ingestWebhookConfigRepo) FindActive(_ context.Context) ([]models.WebhookConfig, error) {
	return nil, nil
}
func (f *ingestWebhookConfigRepo) FindAll(_ context.Context) ([]models.WebhookConfig, error) {
	return nil, nil
}
func (f *ingestWebhookConfigRepo) FindVisibleToAccount(_ context.Context, accountID uuid.UUID) ([]models.WebhookConfig, error) {
	return nil, nil
}
func (f *ingestWebhookConfigRepo) FindByID(_ context.Context, id uuid.UUID) (*models.WebhookConfig, error) {
	return nil, nil
}
func (f *ingestWebhookConfigRepo) AssignAccount(_ context.Context, id, accountID uuid.UUID, events *string, isActive *bool) error {
	return nil
}
func (f *ingestWebhookConfigRepo) DeleteByID(_ context.Context, id uuid.UUID) error { return nil }

type ingestWebhookEventRepo struct{}

func (f *ingestWebhookEventRepo) Create(_ context.Context, event *models.WebhookEvent) error {
	return nil
}
func (f *ingestWebhookEventRepo) MarkDelivered(_ context.Context, eventID string) error { return nil }
func (f *ingestWebhookEventRepo) IncrementAttempt(_ context.Context, eventID, errMsg string) error {
	return nil
}
func (f *ingestWebhookEventRepo) ExistsForSubject(_ context.Context, configID uuid.UUID, eventType, subjectID string) (bool, error) {
	return false, nil
}
func (f *ingestWebhookEventRepo) FindDueForDelivery(_ context.Context, limit int, baseBackoff, maxBackoff time.Duration) ([]models.WebhookEvent, error) {
	return nil, nil
}
func (f *ingestWebhookEventRepo) MarkFailed(_ context.Context, eventID, errMsg string) error {
	return nil
}
