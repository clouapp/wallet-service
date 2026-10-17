package sweep

import (
	"context"
	"fmt"
	"math/big"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// nativeTransferReserver is implemented by chains whose native transfers are paid by
// the source on top of the amount and may have to leave a minimum behind (Solana:
// signature fee and rent-exempt minimum; TRON: the worst-case transfer fee).
type nativeTransferReserver interface {
	NativeTransferReserve(ctx context.Context) (fee, minimumRemaining *big.Int, err error)
}

// nativeRecipientReserver is implemented by chains whose native transfer fee depends
// on its recipient (EVM: gas limit × gas price with the node's estimate, which
// prices creating a recipient account that does not exist yet). An empty recipient
// is one not known yet, priced as the most a transfer can need. It is preferred
// over nativeTransferReserver.
type nativeRecipientReserver interface {
	NativeTransferReserveTo(ctx context.Context, to string) (fee, minimumRemaining *big.Int, err error)
}

// reserveRecipients are the addresses a plan's native transfers pay: the
// withdrawal's recipient ("" when unknown) and the base address sweeps land on.
type reserveRecipients struct {
	withdrawal string
	base       string
}

// nativeReserve is what a plan keeps aside on every native transfer source: fee on a
// transfer to the withdrawal's recipient, sweepFee on a sweep into the base address.
// It is zero for chains and assets that do not need one, which leaves their plans
// unchanged.
type nativeReserve struct {
	fee              *big.Int
	sweepFee         *big.Int
	minimumRemaining *big.Int
}

func loadNativeReserve(ctx context.Context, adapter types.Chain, asset string, recipients reserveRecipients) (nativeReserve, error) {
	none := nativeReserve{fee: new(big.Int), sweepFee: new(big.Int), minimumRemaining: new(big.Int)}
	if !types.SameAssetSymbol(asset, adapter.NativeAsset()) {
		return none, nil
	}
	if reserver, ok := adapter.(nativeRecipientReserver); ok {
		return loadRecipientReserve(ctx, adapter, reserver, recipients)
	}
	reserver, ok := adapter.(nativeTransferReserver)
	if !ok {
		return none, nil
	}
	fee, minimumRemaining, err := reserver.NativeTransferReserve(ctx)
	if err = validReserve(adapter, fee, minimumRemaining, err); err != nil {
		return nativeReserve{}, err
	}
	return newNativeReserve(fee, fee, minimumRemaining), nil
}

// loadRecipientReserve prices a transfer to the withdrawal's recipient and, when
// the base address is another recipient, a sweep into it.
func loadRecipientReserve(ctx context.Context, adapter types.Chain, reserver nativeRecipientReserver, recipients reserveRecipients) (nativeReserve, error) {
	fee, minimumRemaining, err := reserver.NativeTransferReserveTo(ctx, recipients.withdrawal)
	if err = validReserve(adapter, fee, minimumRemaining, err); err != nil {
		return nativeReserve{}, err
	}
	if recipients.base == recipients.withdrawal {
		return newNativeReserve(fee, fee, minimumRemaining), nil
	}
	sweepFee, baseMinimum, err := reserver.NativeTransferReserveTo(ctx, recipients.base)
	if err = validReserve(adapter, sweepFee, baseMinimum, err); err != nil {
		return nativeReserve{}, err
	}
	return newNativeReserve(fee, sweepFee, minimumRemaining), nil
}

// validReserve refuses a failed, missing or negative reserve answer.
func validReserve(adapter types.Chain, fee, minimumRemaining *big.Int, err error) error {
	if err != nil {
		return fmt.Errorf("sweep: native transfer reserve: %w", err)
	}
	if fee == nil || minimumRemaining == nil || fee.Sign() < 0 || minimumRemaining.Sign() < 0 {
		return fmt.Errorf("sweep: chain %s returned an invalid native transfer reserve", adapter.ID())
	}
	return nil
}

func newNativeReserve(fee, sweepFee, minimumRemaining *big.Int) nativeReserve {
	return nativeReserve{
		fee:              new(big.Int).Set(fee),
		sweepFee:         new(big.Int).Set(sweepFee),
		minimumRemaining: new(big.Int).Set(minimumRemaining),
	}
}

// requiredBalance is the balance a source needs to send amount to the withdrawal's
// recipient and stay valid.
func (r nativeReserve) requiredBalance(amount *big.Int) *big.Int {
	required := new(big.Int).Add(amount, r.fee)
	return required.Add(required, r.minimumRemaining)
}

// sweepableAmount is what a sweep into the base address that empties a source of
// balance can move.
func (r nativeReserve) sweepableAmount(balance *big.Int) *big.Int {
	return new(big.Int).Sub(balance, r.sweepFee)
}

// withFee adds a source-specific transfer fee to the chain-wide reserve.
func (r nativeReserve) withFee(fee *big.Int) nativeReserve {
	return nativeReserve{
		fee:              new(big.Int).Add(r.fee, fee),
		sweepFee:         new(big.Int).Add(r.sweepFee, fee),
		minimumRemaining: new(big.Int).Set(r.minimumRemaining),
	}
}

// spendableFundsReader is implemented by chains whose spendable native funds differ
// from GetBalance and whose transfer fee depends on the source (Bitcoin: the builder
// spends confirmed UTXOs only, and the fee grows with the inputs spent).
type spendableFundsReader interface {
	SpendableFunds(ctx context.Context, address string) (chain.SpendableFunds, error)
}

// sourceFunds is what one source can put toward a plan, and what every transfer
// from it keeps aside.
type sourceFunds struct {
	balance *big.Int
	reserve nativeReserve
}

// loadSourceFunds reads addr's balance for asset. For a native asset on a chain
// that reports spendable funds, the balance is the spendable one and the source's
// own transfer fee joins the reserve; otherwise it is the plain balance with the
// chain-wide reserve. Displayed balances are not affected.
func loadSourceFunds(
	ctx context.Context,
	registry chainLookup,
	chainID string,
	adapter types.Chain,
	addr models.Address,
	asset string,
	reserve nativeReserve,
) (sourceFunds, error) {
	if reader, ok := adapter.(spendableFundsReader); ok && types.SameAssetSymbol(asset, adapter.NativeAsset()) {
		funds, err := reader.SpendableFunds(ctx, addr.Address)
		if err != nil {
			return sourceFunds{}, err
		}
		if !validSpendableFunds(funds) {
			return sourceFunds{}, fmt.Errorf("chain %s returned invalid spendable funds for %s", adapter.ID(), addr.Address)
		}
		return sourceFunds{balance: new(big.Int).Set(funds.Balance), reserve: reserve.withFee(funds.MaxTransferFee)}, nil
	}
	balance, err := fetchBalance(ctx, registry, chainID, adapter, addr, asset)
	if err != nil {
		return sourceFunds{}, err
	}
	return sourceFunds{balance: balance, reserve: reserve}, nil
}

func validSpendableFunds(funds chain.SpendableFunds) bool {
	return funds.Balance != nil && funds.MaxTransferFee != nil &&
		funds.Balance.Sign() >= 0 && funds.MaxTransferFee.Sign() >= 0 &&
		funds.MaxTransferFee.Cmp(funds.Balance) <= 0
}
