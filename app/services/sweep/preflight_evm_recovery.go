package sweep

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/mpcshare"
	"github.com/macrowallets/waas/pkg/types"
)

// EVMRecoverySigner signs an exact call with an existing wallet address. The
// recovery coordinator must claim the immutable operation before calling it and
// persist the returned sealed bytes before transmission. This port neither
// claims nor broadcasts, and never asks a node to choose a nonce.
type EVMRecoverySigner interface {
	SignEVMRecoveryCall(context.Context, uuid.UUID, uuid.UUID, int64, chain.EVMCall, string) (*types.SignedTx, error)
}

var _ EVMRecoverySigner = (*service)(nil)

func (s *service) SignEVMRecoveryCall(ctx context.Context, walletID, addressID uuid.UUID, networkID int64, call chain.EVMCall, passphrase string) (*types.SignedTx, error) {
	defer mpcshare.DiscardPassphrase(&passphrase)
	if ctx == nil || walletID == uuid.Nil || addressID == uuid.Nil || networkID <= 0 || s.walletRepo == nil {
		return nil, fmt.Errorf("evm recovery signing: context, wallet, address and network are required")
	}
	wallet, err := s.walletRepo.FindByID(ctx, walletID)
	if err != nil || wallet == nil || wallet.ID != walletID || wallet.DepositAddress == nil {
		return nil, fmt.Errorf("evm recovery signing: wallet is unavailable")
	}
	source, err := s.recoverySigningAddress(ctx, wallet, addressID)
	if err != nil {
		return nil, err
	}
	adapter, err := s.registry.ChainForWallet(wallet)
	if err != nil {
		return nil, fmt.Errorf("evm recovery signing: wallet adapter is unavailable: %w", err)
	}
	builder, ok := adapter.(chain.EVMCallBuilder)
	if !ok || adapter.ID() != wallet.Chain {
		return nil, fmt.Errorf("evm recovery signing: exact EVM call builder is required")
	}
	unsigned, err := builder.BuildCall(call)
	if err != nil {
		return nil, err
	}
	if unsigned == nil || unsigned.ChainID != wallet.Chain || unsigned.Metadata["chain_id"] != networkID {
		return nil, fmt.Errorf("evm recovery signing: call network differs from the reviewed network")
	}
	session, err := s.openPreflightSession(ctx, walletID, wallet.Chain, passphrase)
	if err != nil {
		return nil, err
	}
	defer session.close()
	if session.curve != mpcpkg.CurveSecp256k1 {
		return nil, fmt.Errorf("evm recovery signing: secp256k1 wallet is required")
	}
	signed, err := s.signUnsigned(ctx, adapter, session.curve, session.keys, session.wallet, *source, unsigned)
	if err != nil {
		return nil, err
	}
	if err := verifyRecoveryCall(signed, source.Address, networkID, call); err != nil {
		return nil, err
	}
	return signed, nil
}

func (s *service) recoverySigningAddress(ctx context.Context, wallet *models.Wallet, id uuid.UUID) (*models.Address, error) {
	var source *models.Address
	if wallet.DepositAddress.ID == id {
		copy := *wallet.DepositAddress
		source = &copy
	} else {
		if s.addressRepo == nil {
			return nil, fmt.Errorf("evm recovery signing: address reader is required")
		}
		addresses, err := s.addressRepo.FindByWalletID(ctx, wallet.ID)
		if err != nil {
			return nil, fmt.Errorf("evm recovery signing: addresses are unavailable: %w", err)
		}
		for _, address := range addresses {
			if address.ID == id {
				copy := address
				source = &copy
				break
			}
		}
	}
	if source == nil || source.WalletID != wallet.ID || source.Chain != wallet.Chain || !source.IsActive || !common.IsHexAddress(source.Address) {
		return nil, fmt.Errorf("evm recovery signing: active address does not belong to the wallet")
	}
	if source.ID != wallet.DepositAddress.ID && (source.DerivationType != bip32DerivationType || source.DerivationIndex <= 0) {
		return nil, fmt.Errorf("evm recovery signing: existing BIP32 child derivation is required")
	}
	return source, nil
}

func verifyRecoveryCall(signed *types.SignedTx, source string, networkID int64, call chain.EVMCall) error {
	if signed == nil || len(signed.RawBytes) == 0 || call.Value == nil || call.GasPrice == nil {
		return fmt.Errorf("evm recovery signing: signed call is unavailable")
	}
	var tx gethtypes.Transaction
	if err := tx.UnmarshalBinary(signed.RawBytes); err != nil {
		return fmt.Errorf("evm recovery signing: decode signed call: %w", err)
	}
	if tx.ChainId().Cmp(big.NewInt(networkID)) != 0 || tx.Nonce() != call.Nonce || tx.To() == nil || *tx.To() != common.HexToAddress(call.To) || tx.Value().Cmp(call.Value) != 0 || tx.Gas() != call.GasLimit || tx.GasPrice().Cmp(call.GasPrice) != 0 || !bytes.Equal(tx.Data(), call.Data) || !strings.EqualFold(tx.Hash().Hex(), signed.TxHash) {
		return fmt.Errorf("evm recovery signing: signed call differs from the reviewed exact call")
	}
	sender, err := gethtypes.Sender(gethtypes.LatestSignerForChainID(big.NewInt(networkID)), &tx)
	if err != nil || sender != common.HexToAddress(source) {
		return fmt.Errorf("evm recovery signing: signed sender differs from the reviewed address")
	}
	return nil
}
