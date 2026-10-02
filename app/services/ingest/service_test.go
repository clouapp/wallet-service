package ingest

import (
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestProcessTransfers_UnknownChain(t *testing.T) {
	reg := chain.NewRegistry()
	svc := &Service{registry: reg}
	err := svc.ProcessTransfers(t.Context(), "unknown_chain", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown chain")
}

func TestNewService_NilDeps(t *testing.T) {
	svc := NewService(nil, nil, nil, nil, nil)
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
	svc := NewService(nil, reg, webhook.NewService(nil, &ingestWebhookConfigRepo{}, &ingestWebhookEventRepo{}), addrs, txs)

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

type ingestAddressRepo struct {
	addr *models.Address
}

func (f *ingestAddressRepo) Create(addr *models.Address) error { return nil }
func (f *ingestAddressRepo) UpdateFields(id uuid.UUID, fields map[string]interface{}) error {
	return nil
}
func (f *ingestAddressRepo) CountByChainAndAddress(chainID, address string) (int64, error) {
	if f.addr != nil && f.addr.Chain == chainID && f.addr.Address == address {
		return 1, nil
	}
	return 0, nil
}
func (f *ingestAddressRepo) FindByChainAndAddress(chainID, address string) (*models.Address, error) {
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
}

func (f *ingestTxRepo) Create(tx *models.Transaction) error {
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
func (f *ingestTxRepo) CountByChainTxHashAndLogIndex(chainID, txHash string, logIndex int, txType string) (int64, error) {
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

func (f *ingestWebhookConfigRepo) Create(cfg *models.WebhookConfig) error { return nil }
func (f *ingestWebhookConfigRepo) FindByWalletID(walletID uuid.UUID) ([]models.WebhookConfig, error) {
	return nil, nil
}
func (f *ingestWebhookConfigRepo) FindByIDAndWallet(id, walletID uuid.UUID) (*models.WebhookConfig, error) {
	return nil, nil
}
func (f *ingestWebhookConfigRepo) FindActive() ([]models.WebhookConfig, error) { return nil, nil }
func (f *ingestWebhookConfigRepo) FindAll() ([]models.WebhookConfig, error)    { return nil, nil }
func (f *ingestWebhookConfigRepo) Delete(cfg *models.WebhookConfig) error      { return nil }
func (f *ingestWebhookConfigRepo) DeleteByID(id uuid.UUID) error               { return nil }
func (f *ingestWebhookConfigRepo) FindByID(id uuid.UUID) (*models.WebhookConfig, error) {
	return nil, nil
}
func (f *ingestWebhookConfigRepo) FindVisibleToAccount(accountID uuid.UUID) ([]models.WebhookConfig, error) {
	return nil, nil
}
func (f *ingestWebhookConfigRepo) UpdateFields(id uuid.UUID, fields map[string]any) error { return nil }

type ingestWebhookEventRepo struct{}

func (f *ingestWebhookEventRepo) Create(event *models.WebhookEvent) error { return nil }
func (f *ingestWebhookEventRepo) MarkDelivered(eventID string) error      { return nil }
func (f *ingestWebhookEventRepo) IncrementAttempt(eventID string, errMsg string) error {
	return nil
}
func (f *ingestWebhookEventRepo) ExistsForSubject(configID uuid.UUID, eventType, subjectID string) (bool, error) {
	return false, nil
}
func (f *ingestWebhookEventRepo) FindDueForDelivery(limit int, baseBackoff, maxBackoff time.Duration) ([]models.WebhookEvent, error) {
	return nil, nil
}
func (f *ingestWebhookEventRepo) MarkFailed(eventID string, errMsg string) error { return nil }
