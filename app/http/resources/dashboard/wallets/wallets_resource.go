package wallets

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	addressresource "github.com/macrowallets/waas/app/http/resources/addresses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
)

// Wallet is the wallet JSON the model used to emit: carbon timestamps
// first, the same names and omitempty rules, and the nested deposit address as
// an address view. MPC share material and the activation code stay off the wire.
// A nil wallet stays null.
type Wallet struct {
	CreatedAt           *carbon.DateTime         `json:"created_at"`
	UpdatedAt           *carbon.DateTime         `json:"updated_at"`
	ID                  uuid.UUID                `json:"id"`
	Chain               string                   `json:"chain"`
	Label               string                   `json:"label,omitempty"`
	AddressIndex        int                      `json:"address_index"`
	DepositAddressID    *uuid.UUID               `json:"deposit_address_id,omitempty"`
	AccountID           *uuid.UUID               `json:"account_id,omitempty"`
	Status              string                   `json:"status"`
	FeeRateMin          *int                     `json:"fee_rate_min,omitempty"`
	FeeRateMax          *int                     `json:"fee_rate_max,omitempty"`
	FeeMultiplier       numeric.NullDecimal      `json:"fee_multiplier,omitzero"`
	RequiredApprovals   int                      `json:"required_approvals"`
	FrozenUntil         *time.Time               `json:"frozen_until,omitempty"`
	BalanceAsset        *string                  `json:"balance_asset,omitempty"`
	BalanceRaw          *string                  `json:"balance_raw,omitempty"`
	BalanceDisplay      *string                  `json:"balance,omitempty"`
	BalanceUSD          numeric.NullDecimal      `json:"balance_usd,omitzero"`
	BalanceLastSyncedAt *time.Time               `json:"balance_last_synced_at,omitempty"`
	ReadModelStatus     string                   `json:"read_model_status"`
	GasStatus           string                   `json:"gas_status"`
	GasLastCheckedAt    *time.Time               `json:"gas_last_checked_at,omitempty"`
	SweepPolicyVersion  int                      `json:"sweep_policy_version"`
	DepositAddress      *addressresource.Address `json:"deposit_address,omitempty"`
}

// WalletFrom projects one wallet. It does not add network fields or reformat
// timestamps. Callers embed it in create responses and in a related balance row.
func WalletFrom(wallet models.Wallet) Wallet {
	return Wallet{
		CreatedAt:           wallet.CreatedAt,
		UpdatedAt:           wallet.UpdatedAt,
		ID:                  wallet.ID,
		Chain:               wallet.Chain,
		Label:               wallet.Label,
		AddressIndex:        wallet.AddressIndex,
		DepositAddressID:    wallet.DepositAddressID,
		AccountID:           wallet.AccountID,
		Status:              wallet.Status,
		FeeRateMin:          wallet.FeeRateMin,
		FeeRateMax:          wallet.FeeRateMax,
		FeeMultiplier:       wallet.FeeMultiplier,
		RequiredApprovals:   wallet.RequiredApprovals,
		FrozenUntil:         wallet.FrozenUntil,
		BalanceAsset:        wallet.BalanceAsset,
		BalanceRaw:          wallet.BalanceRaw,
		BalanceDisplay:      wallet.BalanceDisplay,
		BalanceUSD:          wallet.BalanceUSD,
		BalanceLastSyncedAt: wallet.BalanceLastSyncedAt,
		ReadModelStatus:     wallet.ReadModelStatus,
		GasStatus:           wallet.GasStatus,
		GasLastCheckedAt:    wallet.GasLastCheckedAt,
		SweepPolicyVersion:  wallet.SweepPolicyVersion,
		DepositAddress:      addressresource.AddressPtr(wallet.DepositAddress, WalletPtr),
	}
}

// WalletPtr keeps a nil wallet as JSON null.
func WalletPtr(wallet *models.Wallet) *Wallet {
	if wallet == nil {
		return nil
	}
	view := WalletFrom(*wallet)
	return &view
}
