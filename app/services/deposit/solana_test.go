package deposit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	solFixtureSlot      = 506367800
	solFixtureRecipient = "AJor34TKjdm2pAK6V7nkBmKNpnARHL7CkiwnBVTE9J6b"
	solFixtureSignature = "41iqE5xg9ttZAk1uZkirZsQz3GG1DJ3kcU33YUFJAyraS3MFG2BWMxweU7KUXqhDZuUmzW2bqs5L3PPE1RRQEK9r"
)

// solanaFixtureRPC serves devnet responses recorded in the chain package testdata.
func solanaFixtureRPC(t *testing.T) *chain.SolanaLive {
	t.Helper()
	block, err := os.ReadFile(filepath.Join("..", "chain", "testdata", "solana", "getBlock_transfers.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "getBlock":
			_, _ = w.Write(block)
		case "getSlot":
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":506367988}`)
		case "getSignatureStatuses":
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"context":{"slot":506367988},"value":[{"slot":506367800,"err":null,"confirmationStatus":"finalized","confirmations":null}]}}`)
		default:
			t.Errorf("unexpected rpc method %s", req.Method)
		}
	}))
	t.Cleanup(srv.Close)
	return chain.NewSolanaLive(chain.SolanaConfig{
		ChainIDStr: models.ChainSOL, NativeSymbol: models.NativeSOL, RPCURL: srv.URL, Confirmations: 1,
	})
}

func TestSolanaDeposit_DetectedFromRecordedBlockThenConfirmed(t *testing.T) {
	mocks.TestDB(t)
	adapter := solanaFixtureRPC(t)
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := mocks.InsertWallet(t, models.ChainSOL)
	addr := mocks.InsertAddress(t, w.ID, models.ChainSOL, solFixtureRecipient, "user_sol", 1)
	svc := newDepositSvc(registry, newWebhookSvc())

	if err := svc.processBlock(context.Background(), models.ChainSOL, adapter, solFixtureSlot); err != nil {
		t.Fatal(err)
	}

	var deposits []models.Transaction
	if err := facades.Orm().Query().Where("chain", models.ChainSOL).Where("tx_type", models.TxTypeDeposit).Find(&deposits); err != nil {
		t.Fatal(err)
	}
	if len(deposits) != 1 {
		t.Fatalf("expected one deposit for the watched address, got %d", len(deposits))
	}
	deposit := deposits[0]
	if deposit.TxHash != solFixtureSignature || deposit.AddressID == nil || *deposit.AddressID != addr.ID ||
		deposit.Amount != "150000000" || deposit.FromAddress != "AMbsiP9F8YY2y8n9uFdqtw7yNZZHvTWFEWSQGHKtmkoQ" ||
		deposit.BlockNumber != solFixtureSlot || deposit.Status != "pending" {
		t.Fatalf("unexpected deposit %+v", deposit)
	}

	if err := svc.updateConfirmations(context.Background(), models.ChainSOL, adapter, solFixtureSlot); err != nil {
		t.Fatal(err)
	}
	var reloaded models.Transaction
	if err := facades.Orm().Query().Find(&reloaded, deposit.ID); err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != "confirmed" || reloaded.Confirmations != 1 {
		t.Fatalf("expected confirmed once its own slot is finalized, got %s with %d", reloaded.Status, reloaded.Confirmations)
	}
}

func TestSolanaWithdrawal_SlotReconciledFromSignatureStatus(t *testing.T) {
	mocks.TestDB(t)
	adapter := solanaFixtureRPC(t)
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := mocks.InsertWallet(t, models.ChainSOL)
	withdrawal := mocks.InsertTransaction(t, w.ID, nil, models.ChainSOL, models.TxTypeWithdrawal, "confirming", models.NativeSOL, "20000000", 0)
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id", withdrawal.ID).
		Update(map[string]interface{}{"required_confs": 1, "tx_hash": solFixtureSignature}); err != nil {
		t.Fatal(err)
	}
	confirmations := &recordingWithdrawalConfirmations{}
	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(confirmations)

	if err := svc.updateConfirmations(context.Background(), models.ChainSOL, adapter, solFixtureSlot+1); err != nil {
		t.Fatal(err)
	}

	var reloaded models.Transaction
	if err := facades.Orm().Query().Find(&reloaded, withdrawal.ID); err != nil {
		t.Fatal(err)
	}
	if reloaded.BlockNumber != solFixtureSlot || reloaded.Status != "confirmed" {
		t.Fatalf("expected slot %d and confirmed, got %d / %s", solFixtureSlot, reloaded.BlockNumber, reloaded.Status)
	}
	if len(confirmations.confirmed) != 1 || confirmations.confirmed[0].ID != withdrawal.ID {
		t.Fatalf("withdrawal.confirmed must be published once, got %d", len(confirmations.confirmed))
	}
}
