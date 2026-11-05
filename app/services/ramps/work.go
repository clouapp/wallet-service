package ramps

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	enginefacades "github.com/macrowallets/waas/packages/rampengine/app/facades"
	emodels "github.com/macrowallets/waas/packages/rampengine/app/models"
	erepo "github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

func (s *Service) runTask(ctx context.Context, task Task) error {
	var payload TaskPayload
	if err := json.Unmarshal([]byte(task.Payload), &payload); err != nil {
		return fmt.Errorf("invalid ramp task")
	}
	accountID := uuid.Nil
	if task.AccountID != nil {
		accountID = *task.AccountID
	}
	runtime, err := s.RuntimeMode(accountID, payload.Mode, nil)
	if err != nil {
		return err
	}
	switch task.Kind {
	case "kyc":
		var holder emodels.Customer
		if err := enginefacades.Orm().WithContext(ctx).Query().Where("id", payload.EngineCustomerID).Where("environment", payload.Mode).First(&holder); err != nil {
			return err
		}
		if holder.ID == 0 {
			return fmt.Errorf("ramp task holder environment mismatch")
		}
		if accountID != uuid.Nil && holder.TenantKey != accountID.String() {
			return fmt.Errorf("ramp task holder account mismatch")
		}
		return runtime.Kyc.ProcessStagedSubmission(payload.SubmissionID, payload.EngineCustomerID, payload.Provider)
	case "settlement":
		row, err := erepo.NewTransactionRepository().FindByID(payload.ID)
		if err != nil {
			return err
		}
		if row == nil {
			return fmt.Errorf("ramp settlement not found")
		}
		var holder emodels.Customer
		if err := enginefacades.Orm().WithContext(ctx).Query().Where("id", row.CustomerID).Where("environment", payload.Mode).First(&holder); err != nil {
			return err
		}
		if holder.ID == 0 || accountID != uuid.Nil && holder.TenantKey != accountID.String() {
			return fmt.Errorf("ramp settlement scope mismatch")
		}
		return runtime.Offramp.Settle(ctx, payload.ID)
	case "webhook":
		row, err := erepo.NewWebhookEventRepository().FindByID(payload.ID)
		if err != nil {
			return err
		}
		if row == nil || !strings.HasPrefix(row.EventID, payload.Mode+":") {
			return fmt.Errorf("ramp webhook scope mismatch")
		}
		if err := runtime.checkWebhookEnvironment(ctx, row); err != nil {
			return err
		}
		return runtime.Processor.Process(ctx, payload.ID)
	default:
		return fmt.Errorf("unknown ramp task kind")
	}
}

// Check every known binding before the legacy processor runs. A verified
// production event must never update an identically named sandbox object.
func (r *Runtime) checkWebhookEnvironment(ctx context.Context, row *emodels.WebhookEvent) error {
	adapter, err := r.Factory.Get(row.Provider)
	if err != nil {
		return err
	}
	event, err := adapter.DecodeWebhook(ctx, []byte(row.RawPayload))
	if err != nil {
		return err
	}
	queries := []struct{ table, column, value string }{{"customer_provider_integrations", "external_customer_id", event.ExternalCustomerID}, {"transactions", "gateway_order_id", event.ProviderOrderID}, {"customer_virtual_accounts", "external_account_id", event.ProviderAccountID}, {"user_liquidation_addresses", "external_address_id", event.ProviderAccountID}, {"customer_provider_integrations", "external_customer_id", event.ProviderCustomerID}, {"ramp_estimates", "provider_quote_id", event.ProviderQuoteID}}
	for _, query := range queries {
		if query.value == "" {
			continue
		}
		var found []struct{ Environment string }
		err := enginefacades.Orm().WithContext(ctx).Query().Raw("SELECT c.environment FROM "+query.table+" b JOIN customers c ON c.id=b.customer_id WHERE b.provider=? AND b."+query.column+"=?", row.Provider, query.value).Scan(&found)
		if err != nil {
			return err
		}
		for _, binding := range found {
			if binding.Environment != r.Mode {
				return fmt.Errorf("provider webhook environment does not match its stored binding")
			}
		}
	}
	return nil
}

// A reconciliation pass resolves each holder's own account environment before
// reading any provider. It never runs one host-wide provider mode over both
// the test and production ledger.
func (s *Service) Reconcile(ctx context.Context, limit int) (int, error) {
	rows, err := s.d.State.ReconciliationCustomers(ctx, limit)
	if err != nil {
		return 0, err
	}
	count := 0
	recovered := map[uuid.UUID]bool{}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		mode, err := ModeForAccount(&models.Account{Environment: row.Environment})
		if err != nil {
			return count, err
		}
		runtime, err := s.RuntimeMode(row.AccountID, mode, nil)
		if err != nil {
			return count, err
		}
		if !recovered[row.AccountID] {
			if _, err := runtime.Kyc.SweepIntentRecovery(ctx, limit); err != nil {
				return count, err
			}
			if _, err := runtime.Finalizations.Recover(ctx, limit); err != nil {
				return count, err
			}
			if _, err := runtime.Reconciler.Sweep(ctx, 2*time.Minute, limit); err != nil {
				return count, err
			}
			if _, err := runtime.AccountSyncer.Sync(ctx); err != nil {
				return count, err
			}
			recovered[row.AccountID] = true
		}
		_, _ = runtime.Kyc.RefreshEnabled(ctx, row.EngineCustomerID)
		var orders []emodels.Transaction
		if err := enginefacades.Orm().WithContext(ctx).Query().Where("customer_id", row.EngineCustomerID).Where("status IN (?)", []string{emodels.TxStatusPending, emodels.TxStatusAwaitingFunds, emodels.TxStatusProcessing, emodels.TxStatusFundsReceived}).Limit(100).Get(&orders); err != nil {
			return count, err
		}
		for _, order := range orders {
			if order.Type == emodels.TransactionTypeOnramp {
				_, _ = runtime.Onramp.Show(ctx, row.EngineCustomerID, order.ExternalID)
			} else {
				_, _ = runtime.Offramp.Show(ctx, row.EngineCustomerID, order.ExternalID)
			}
		}
		_ = s.d.State.TouchCustomer(ctx, row.ID)
		count++
	}
	return count, nil
}
