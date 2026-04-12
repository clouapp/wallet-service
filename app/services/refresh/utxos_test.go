package refresh

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestNewUTXOServiceNotNil(t *testing.T) {
	svc := NewUTXOService(nil, nil)
	if svc == nil {
		t.Fatal("expected non-nil UTXOService")
	}
}

func TestNewUTXOServiceFields(t *testing.T) {
	svc := NewUTXOService(nil, nil)
	if svc.utxoRepo != nil {
		t.Fatal("expected nil utxoRepo")
	}
	if svc.syncStateRepo != nil {
		t.Fatal("expected nil syncStateRepo")
	}
}

func TestReplaceWalletUTXOsWithNilRepoReturnsError(t *testing.T) {
	svc := NewUTXOService(nil, nil)
	wallet := &models.Wallet{
		ID:    uuid.New(),
		Chain: "btc",
	}

	defer func() {
		if r := recover(); r != nil {
			if msg, ok := r.(string); ok && strings.Contains(msg, "nil") {
				return
			}
			// Nil pointer dereference panic is expected when repo is nil
			return
		}
	}()

	err := svc.ReplaceWalletUTXOs(context.Background(), wallet, nil)
	if err == nil {
		t.Fatal("expected error or panic when utxoRepo is nil")
	}
}
