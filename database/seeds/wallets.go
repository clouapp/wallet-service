package seeds

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

// Placeholder MPC values — realistic hex lengths, not real key material.
const (
	fakePubKey    = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	fakeShareHex  = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	fakeIVHex     = "000102030405060708090a0b"
	fakeSaltHex   = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	fakeSecretARN = "arn:aws:secretsmanager:us-east-1:000000000000:secret:vault/wallet/seed/share-b"
)

// SeedWallets inserts demo wallets, deposit addresses, and wallet_users rows.
func SeedWallets(_ context.Context) error {
	wallets := []struct {
		id             uuid.UUID
		addressID      uuid.UUID
		accountID      uuid.UUID
		chain          string
		label          string
		depositAddress string
		curve          string
	}{
		{ethWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a0"), acmeAccountID, "eth", "Primary ETH Wallet", "0xDEADbeef00000000000000000000000000000001", "secp256k1"},
		{btcWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a1"), acmeAccountID, "btc", "Primary BTC Wallet", "bc1qseed000000000000000000000000000000000001", "secp256k1"},
		{polyWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a2"), acmeAccountID, "polygon", "Polygon Wallet", "0xDEADbeef00000000000000000000000000000002", "secp256k1"},
		{tethWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a3"), acmeTestAccountID, "teth", "Sepolia ETH Wallet", "0xDEADbeef00000000000000000000000000000003", "secp256k1"},
		{tbtcWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a4"), acmeTestAccountID, "tbtc", "Bitcoin Testnet Wallet", "bc1qseed000000000000000000000000000000000002", "secp256k1"},
		{tpolyWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a5"), acmeTestAccountID, "tpolygon", "Polygon Amoy Wallet", "0xDEADbeef00000000000000000000000000000004", "secp256k1"},
	}

	for _, w := range wallets {
		var existing models.Wallet
		if err := facades.Orm().Query().Where("id", w.id).First(&existing); err == nil && existing.ID != uuid.Nil {
			slog.Info("wallet already exists, skipping", "label", w.label)
			continue
		}

		aid := w.accountID
		wallet := models.Wallet{
			ID:                w.id,
			Chain:             w.chain,
			Label:             w.label,
			MPCCustomerShare:  fakeShareHex,
			MPCShareIV:        fakeIVHex,
			MPCShareSalt:      fakeSaltHex,
			MPCSecretARN:      fakeSecretARN,
			MPCPublicKey:      fakePubKey,
			MPCCurve:          w.curve,
			AccountID:         &aid,
			Status:            "active",
			RequiredApprovals: 1,
		}
		if err := facades.Orm().Query().Create(&wallet); err != nil {
			return fmt.Errorf("create wallet %s: %w", w.label, err)
		}

		addr := models.Address{
			ID:              w.addressID,
			WalletID:        w.id,
			Chain:           w.chain,
			Address:         w.depositAddress,
			DerivationIndex: 0,
			ExternalUserID:  "system",
			IsActive:        true,
			Label:           "Deposit Address",
		}
		if err := facades.Orm().Query().Create(&addr); err != nil {
			return fmt.Errorf("create deposit address for %s: %w", w.label, err)
		}

		addrID := w.addressID
		if _, err := facades.Orm().Query().Model(&models.Wallet{}).Where("id = ?", w.id).Update("deposit_address_id", addrID); err != nil {
			return fmt.Errorf("link deposit address for %s: %w", w.label, err)
		}

		slog.Info("created wallet", "label", w.label, "chain", w.chain)
	}

	walletUserSeeds := []struct {
		id       uuid.UUID
		walletID uuid.UUID
		userID   uuid.UUID
		roles    string
	}{
		{uuid.MustParse("00000000-0000-0000-0000-000000000040"), ethWalletID, aliceUserID, "viewer,spender"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000041"), ethWalletID, bobUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000042"), btcWalletID, aliceUserID, "viewer,spender"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000043"), btcWalletID, bobUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000044"), polyWalletID, aliceUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000050"), tethWalletID, aliceUserID, "viewer,spender"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000051"), tethWalletID, bobUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000052"), tbtcWalletID, aliceUserID, "viewer,spender"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000053"), tbtcWalletID, bobUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000054"), tpolyWalletID, aliceUserID, "viewer"},
	}
	for _, wu := range walletUserSeeds {
		var existing models.WalletUser
		if err := facades.Orm().Query().Where("id", wu.id).First(&existing); err == nil && existing.ID != uuid.Nil {
			continue
		}
		walletUser := models.WalletUser{
			ID:       wu.id,
			WalletID: wu.walletID,
			UserID:   wu.userID,
			Roles:    wu.roles,
			Status:   "active",
		}
		if err := facades.Orm().Query().Create(&walletUser); err != nil {
			return fmt.Errorf("create wallet_user: %w", err)
		}
	}
	slog.Info("seeded wallet users")
	return nil
}
