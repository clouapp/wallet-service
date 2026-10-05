package chainregistry

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
)

// Genesis address fields, as the wallet service writes them at creation.
const (
	GenesisDerivationIndex = 0
	GenesisExternalUserID  = "system"
	GenesisLabel           = "Deposit Address"
	GenesisDerivationType  = "genesis"
	bech32Separator        = "1"
)

// AddressStore is the persistence the address reissue needs.
type AddressStore interface {
	WalletsOnChain(ctx context.Context, chainID string) ([]models.Wallet, error)
	ActiveAddressesOfWallet(ctx context.Context, walletID uuid.UUID) ([]models.Address, error)
	// ReissueGenesis retires the retire ids, inserts genesis and points the wallet's
	// deposit address at it, all or nothing.
	ReissueGenesis(ctx context.Context, walletID uuid.UUID, genesis models.Address, retire []uuid.UUID) error
}

// AddressReissue is what one wallet gets when its chain moves to a network with
// another address format.
type AddressReissue struct {
	WalletID    uuid.UUID
	WalletLabel string
	// Genesis is the wallet's deposit address on the new network; empty when the
	// wallet already has it.
	Genesis models.Address
	Retired []models.Address
}

// ReissueTarget is a chain whose addresses must match a network's format.
type ReissueTarget struct {
	ChainID     string
	AdapterType string
	Testnet     bool
}

// PlanAddressReissue lists, for a Bitcoin chain, the active addresses not valid on
// the target network and the genesis address each wallet gets instead (same key,
// the target network's bech32 prefix). Per-user addresses are only retired: their
// owners ask for new ones. EVM and Solana addresses do not depend on the network,
// so other chains need nothing.
func PlanAddressReissue(ctx context.Context, store AddressStore, target ReissueTarget) ([]AddressReissue, error) {
	if store == nil {
		return nil, errors.New("chainregistry: address store is required")
	}
	if target.AdapterType != models.AdapterTypeBitcoin {
		return nil, nil
	}
	validPrefix := addressing.BtcHRP(target.Testnet) + bech32Separator

	wallets, err := store.WalletsOnChain(ctx, target.ChainID)
	if err != nil {
		return nil, fmt.Errorf("load wallets on %s: %w", target.ChainID, err)
	}
	var reissues []AddressReissue
	for _, wallet := range wallets {
		reissue, err := planWalletReissue(ctx, store, wallet, target, validPrefix)
		if err != nil {
			return nil, err
		}
		if reissue != nil {
			reissues = append(reissues, *reissue)
		}
	}
	return reissues, nil
}

func planWalletReissue(ctx context.Context, store AddressStore, wallet models.Wallet, target ReissueTarget, validPrefix string) (*AddressReissue, error) {
	active, err := store.ActiveAddressesOfWallet(ctx, wallet.ID)
	if err != nil {
		return nil, fmt.Errorf("load addresses of wallet %s: %w", wallet.ID, err)
	}
	reissue := &AddressReissue{WalletID: wallet.ID, WalletLabel: wallet.Label}
	hasValidGenesis := false
	for _, address := range active {
		if strings.HasPrefix(address.Address, validPrefix) {
			hasValidGenesis = hasValidGenesis || address.DerivationType == GenesisDerivationType
			continue
		}
		reissue.Retired = append(reissue.Retired, address)
	}
	if len(reissue.Retired) == 0 && hasValidGenesis {
		return nil, nil
	}
	if !hasValidGenesis {
		genesis, err := genesisAddress(wallet, target)
		if err != nil {
			return nil, err
		}
		reissue.Genesis = genesis
	}
	return reissue, nil
}

func genesisAddress(wallet models.Wallet, target ReissueTarget) (models.Address, error) {
	pubKey, err := hex.DecodeString(wallet.MPCPublicKey)
	if err != nil {
		return models.Address{}, fmt.Errorf("decode public key of wallet %s: %w", wallet.ID, err)
	}
	address, err := addressing.DeriveAddressOnNetwork(target.ChainID, target.Testnet, pubKey)
	if err != nil {
		return models.Address{}, fmt.Errorf("derive genesis address of wallet %s: %w", wallet.ID, err)
	}
	return models.Address{
		ID:              uuid.New(),
		WalletID:        wallet.ID,
		Chain:           target.ChainID,
		Address:         address,
		DerivationIndex: GenesisDerivationIndex,
		ExternalUserID:  GenesisExternalUserID,
		IsActive:        true,
		Label:           GenesisLabel,
		DerivationType:  GenesisDerivationType,
	}, nil
}

// ApplyAddressReissue writes one wallet's reissue.
func ApplyAddressReissue(ctx context.Context, store AddressStore, reissue AddressReissue) error {
	if store == nil {
		return errors.New("chainregistry: address store is required")
	}
	retire := make([]uuid.UUID, 0, len(reissue.Retired))
	for _, address := range reissue.Retired {
		retire = append(retire, address.ID)
	}
	return store.ReissueGenesis(ctx, reissue.WalletID, reissue.Genesis, retire)
}
