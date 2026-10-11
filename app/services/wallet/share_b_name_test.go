package wallet

import (
	"testing"

	"github.com/google/uuid"
)

// The name is part of every stored ARN; secrets:migrate-shares relies on it.
func TestShare_B_SecretName_IsTheWalletPath(t *testing.T) {
	id := uuid.MustParse("0b6f6c2e-3d5a-4b1c-9f1e-2a7c8d9e0f11")
	if got := ShareBSecretName(id); got != "vault/wallet/0b6f6c2e-3d5a-4b1c-9f1e-2a7c8d9e0f11/share-b" {
		t.Fatalf("ShareBSecretName = %q", got)
	}
}
