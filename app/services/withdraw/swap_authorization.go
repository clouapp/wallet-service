package withdraw

import (
	"context"
	"errors"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/pkg/amount"
)

// AuthorizeSwap applies the withdrawal gate, 2FA and passphrase attempt limiter
// to a real swap. Resume still checks the passphrase; the swap service verifies
// the original actor and the short authorization lifetime before setting Resume.
func (s *Service) AuthorizeSwap(ctx context.Context, wallet *models.Wallet, in swap.ExecuteInput) error {
	if wallet.AccountID == nil || s.gate(ctx, *wallet.AccountID) != nil {
		return swap.ErrWalletBlocked
	}
	if !in.Resume {
		if err := s.verifyDashboardTOTP(ctx, in.UserID, in.TOTP); err != nil {
			var refusal *CreateRefusal
			if errors.As(err, &refusal) && refusal.Status == CreateStatusForbidden {
				return swap.ErrTOTPRequired
			}
			if errors.As(err, &refusal) && refusal.Status == CreateStatusInternal {
				return err
			}
			return swap.ErrInvalidTOTP
		}
	}
	if len(in.Passphrase) < 12 {
		return swap.ErrInvalidPassphrase
	}
	if err := s.verifyPassphraseBeforePersist(ctx, wallet, in.Passphrase); err != nil {
		var refusal *CreateRefusal
		if errors.As(err, &refusal) && refusal.Status == CreateStatusUnauthorized {
			return swap.ErrInvalidPassphrase
		}
		if errors.As(err, &refusal) && refusal.Status == CreateStatusTooManyRequests {
			return swap.ErrTooManyAttempts
		}
		return err
	}
	return nil
}

// ReserveSwapSpend uses the existing per-token daily USD counter. Called only
// for the first attempt of an order under the wallet operation locks.
func (s *Service) ReserveSwapSpend(ctx context.Context, wallet *models.Wallet, quote *swap.Quote, in swap.ExecuteInput) error {
	req := WithdrawRequest{WalletID: wallet.ID, CallerAccountID: *wallet.AccountID, Asset: strings.ToLower(quote.Request.From.Symbol)}
	if in.APIToken != nil {
		units, ok := amount.ParseBaseUnits(quote.Request.AmountRaw)
		if !ok {
			return swap.ErrInvalidRequest
		}
		req = req.WithAPIToken(in.APIToken, amount.FormatBaseUnits(units, quote.Request.From.Decimals))
	}
	return s.enforceTokenSpendingLimit(ctx, req)
}
