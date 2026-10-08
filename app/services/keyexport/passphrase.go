package keyexport

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

const (
	walletPassphrasePrompt      = "Passphrase da wallet %s (%s, %s) — não aparece na tela: "
	maxWalletPassphraseAttempts = 3
	emptyLabelDisplay           = "(sem label)"
)

// PromptPassphrases asks for each wallet passphrase on the terminal, without echo.
type PromptPassphrases struct {
	Terminal Terminal
}

func (p PromptPassphrases) MaxAttempts() int { return maxWalletPassphraseAttempts }

func (p PromptPassphrases) Passphrase(_ context.Context, wallet models.Wallet, attempt int) (string, error) {
	if p.Terminal == nil {
		return "", ErrNoTerminal
	}
	if attempt > 1 {
		p.Terminal.Notify(fmt.Sprintf("A passphrase não decifra a share A (tentativa %d de %d).", attempt, maxWalletPassphraseAttempts))
	}
	typed, err := p.Terminal.ReadSecret(fmt.Sprintf(walletPassphrasePrompt, displayLabel(wallet.Label), wallet.ID, wallet.Chain))
	if err != nil {
		return "", err
	}
	defer zeroBytes(typed)
	if len(typed) == 0 {
		return "", errors.New("empty passphrase")
	}
	return string(typed), nil
}

// VaultPassphrases reads each passphrase from the encrypted e2e wallet vault.
type VaultPassphrases struct {
	Read func(ctx context.Context, walletID uuid.UUID) (string, error)
}

func (v VaultPassphrases) MaxAttempts() int { return 1 }

func (v VaultPassphrases) Passphrase(ctx context.Context, wallet models.Wallet, _ int) (string, error) {
	if v.Read == nil {
		return "", errors.New("passphrase vault is not configured")
	}
	return v.Read(ctx, wallet.ID)
}

func displayLabel(label string) string {
	if strings.TrimSpace(label) == "" {
		return emptyLabelDisplay
	}
	return label
}
