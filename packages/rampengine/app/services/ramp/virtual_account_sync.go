package ramp

import (
	"context"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// VirtualAccountSyncer reconciles standing deposit accounts against the
// provider, and pushes the rail's fee onto the ones that drifted.
//
// It exists because the fee stopped being a property of the order and became a
// property of the account. An order is priced when it is created, so a fee
// change reaches the next one for free; a standing account keeps charging what
// it was provisioned with until somebody updates it upstream. Without this
// sweep, editing the fee in the admin panel is a change that silently does not
// take effect on any account already in use. Spec §11.
//
// It resolves the adapter from the factory and the customer id off the
// integration row, and never through the router — the same conclusion
// ResolveDepositAdapter and WebhookProcessor.readVirtualAccount reach. The
// router answers "may this user transact on this rail", which is eligibility;
// comparing an account against the provider is bookkeeping. Gating it inverts
// the sweep's purpose: a user whose endorsement lapsed is exactly the user whose
// account may have been deactivated upstream, and refusing to look would leave
// the row saying `active` while funds sent to it are returned to the sender.
type VirtualAccountSyncer struct {
	factory   *ramp.Factory
	integRepo repositories.CustomerProviderIntegrationRepository
	accounts  repositories.CustomerVirtualAccountRepository
	config    *ramp.Config
}

func NewVirtualAccountSyncer(
	factory *ramp.Factory,
	integRepo repositories.CustomerProviderIntegrationRepository,
	accounts repositories.CustomerVirtualAccountRepository,
	config *ramp.Config,
) *VirtualAccountSyncer {
	return &VirtualAccountSyncer{
		factory: factory, integRepo: integRepo, accounts: accounts, config: config,
	}
}

// VirtualAccountSyncReport is what one sweep did.
type VirtualAccountSyncReport struct {
	Scanned int
	Updated int
	// FeePushed counts the accounts whose provider-side fee was rewritten to the
	// rail's. It is the number an operator watches after editing the fee.
	FeePushed int
	Failed    int
}

// virtualAccountSyncPage bounds one provider walk. The sweep is scheduled and
// re-enters; capping the load keeps a runaway table from pulling every active
// row into memory on one run. Ordered by id so the prefix is stable until a
// paginated walk is needed.
const virtualAccountSyncPage = 500

// Sync walks every active standing account and reconciles it.
//
// It is idempotent and safe on a schedule: an account matching the provider and
// the rail on status, destination and fee is left completely alone — no local
// write, no upstream call beyond the read. That is the contract
// `ramps:reconcile` carries, and the reason this can run every few minutes
// without generating a row of write traffic per user per run.
func (s *VirtualAccountSyncer) Sync(ctx context.Context) (*VirtualAccountSyncReport, error) {
	// Each payin rail is read once for the whole sweep: it is a single
	// configuration row, and re-reading it per account would make the fee it
	// pushes depend on when in the batch the account happened to fall. An
	// account is held to the rail of its own currency — a USD account to
	// usd_payin's fee, never pix_payin's.
	policies, err := s.payinPolicies()
	if err != nil {
		return nil, err
	}

	report := &VirtualAccountSyncReport{}
	// Which providers to walk comes from the factory rather than from a constant
	// here: a second provider implementing the port must not need this file
	// edited. A provider with no standing accounts costs one empty query.
	for _, provider := range s.factory.Codes() {
		// Each provider's accounts carry that provider's fee. Read once per
		// provider, like the rail itself, so the fee pushed never depends on
		// where in the batch an account fell.
		//
		// A provider with no fee on the rail (the MutualPay stub) collects
		// nothing through a standing account, so there is nothing to sync. It
		// is skipped rather than failing the sweep, which would leave every
		// other provider's accounts unsynced on every run.
		if !chargesFee(policies, provider) {
			continue
		}
		accounts, err := s.accounts.FindActiveByProvider(provider, virtualAccountSyncPage)
		if err != nil {
			return nil, fmt.Errorf("ramp sync accounts: list %s accounts: %w", provider, err)
		}
		// Grouped by the account's customer, never by the member who
		// provisioned it: a company's account is asked about at the company's
		// own provider customer (spec §5 F2).
		integrations, err := s.integRepo.FindByCustomerIDsAndProvider(customerIDsOf(accounts), provider)
		if err != nil {
			return nil, fmt.Errorf("ramp sync accounts: load %s integrations: %w", provider, err)
		}
		byCustomer := integrationsByCustomer(integrations)
		for i := range accounts {
			account := &accounts[i]
			report.Scanned++
			policy, open := policies[account.SourceCurrency]
			if !open {
				// The rail collecting this currency has no provider: there is no
				// fee to carry and no policy to compare against.
				continue
			}
			railFee, err := policy.FeeFor(provider)
			if err != nil {
				continue
			}
			changed, pushed, err := s.syncOne(ctx, account, railFee, byCustomer[account.CustomerID])
			// The push is counted before the error is: a fee that reached the
			// provider and then failed to persist locally did reach the provider,
			// and an operator watching this number to confirm propagation would be
			// misled by a count that hid it.
			if pushed {
				report.FeePushed++
			}
			if err != nil {
				report.Failed++
				// One account being unreachable must not abort the batch: the next
				// run picks it up, and the rest of the users are not held hostage
				// to it. The message names the row, never the payer.
				facades.Log().Warningf("ramp sync accounts: account %d: %v", account.ID, err)
				continue
			}
			if changed {
				report.Updated++
			}
		}
	}
	ramp.LogVirtualAccountSync(report.Scanned, report.Updated, report.FeePushed, report.Failed)
	return report, nil
}

// payinPolicies is the policy of every payin rail that has a provider, by the
// currency it collects. A sweep with no payin rail open has no fee to propagate
// and no policy to compare against: failing loudly beats sweeping every account
// to conclude nothing.
func (s *VirtualAccountSyncer) payinPolicies() (map[string]ramp.RailPolicy, error) {
	policies := map[string]ramp.RailPolicy{}
	for _, currency := range []string{models.FiatCurrencyBRL, models.FiatCurrencyUSD} {
		rail, _ := ramp.PayinRailFor(currency)
		policy, err := s.config.Rail(rail)
		if err != nil {
			return nil, fmt.Errorf("ramp sync accounts: load %s settings: %w", rail, err)
		}
		if len(policy.Providers) > 0 {
			policies[currency] = policy
		}
	}
	if len(policies) == 0 {
		return nil, fmt.Errorf("ramp sync accounts: no payin rail has a provider: %w", ramp.ErrNoProviderAvailable)
	}
	return policies, nil
}

// chargesFee reports whether provider has a fee on any open payin rail.
func chargesFee(policies map[string]ramp.RailPolicy, provider ramp.ProviderCode) bool {
	for _, policy := range policies {
		if _, err := policy.FeeFor(provider); err == nil {
			return true
		}
	}
	return false
}

// syncOne reconciles a single account and reports whether it wrote and whether
// it pushed a fee.
func (s *VirtualAccountSyncer) syncOne(
	ctx context.Context,
	account *models.CustomerVirtualAccount,
	railFee string,
	integration *models.CustomerProviderIntegration,
) (changed, pushed bool, err error) {
	standing, ref, err := s.adapterFor(account, integration)
	if err != nil {
		return false, false, err
	}

	current, err := standing.GetVirtualAccount(ctx, ref, account.ExternalAccountID)
	if err != nil {
		return false, false, fmt.Errorf("read virtual account: %w", err)
	}
	if current == nil {
		return false, false, nil
	}

	// The fee is the one field where the provider is not the authority: the rail
	// setting is, and the whole point of the sweep is carrying it upstream. An
	// inactive account is skipped because it collects nothing — a fee on it would
	// be a write with no deposit left to apply it to.
	if current.Status == models.VirtualAccountStatusActive && !decimalEqual(current.FeePercent, railFee) {
		updated, err := standing.UpdateVirtualAccount(ctx, ref, account.ExternalAccountID,
			ramp.VirtualAccountUpdate{FeePercent: railFee})
		if err != nil {
			return false, false, fmt.Errorf("push rail fee: %w", err)
		}
		pushed = true
		if updated != nil {
			// The provider's echo replaces the read: it is what the account now
			// is, and persisting the pre-update view would store a fee we just
			// changed.
			current = updated
		}
	}

	if !pushed && !diverged(account, current) {
		return false, false, nil
	}

	// MergeVirtualAccount is shared with the webhook path deliberately: two
	// copies of this fold is how the sweep and the event stream end up
	// disagreeing about one account. It writes only what the provider answered,
	// so the deposit instructions are persisted as returned rather than assumed
	// to have changed — the user has that code saved in their bank app.
	MergeVirtualAccount(account, current)
	synced := time.Now()
	account.LastSyncedAt = &synced
	if err := s.accounts.Update(account); err != nil {
		return false, pushed, fmt.Errorf("persist virtual account: %w", err)
	}
	return true, pushed, nil
}

// adapterFor resolves the provider port and the customer to ask about.
//
// The integration comes from the batch Sync loaded once per provider — the same
// memoization shape reconciler.routeCache applies so a walk does not re-ask
// FindByCustomerAndProvider per account.
func (s *VirtualAccountSyncer) adapterFor(
	account *models.CustomerVirtualAccount,
	integration *models.CustomerProviderIntegration,
) (ramp.VirtualAccountAdapter, ramp.CustomerRef, error) {
	standing, err := virtualAccountAdapter(s.factory, account.Provider)
	if err != nil {
		return nil, ramp.CustomerRef{}, err
	}

	if integration == nil || integration.ExternalCustomerID == nil || *integration.ExternalCustomerID == "" {
		return nil, ramp.CustomerRef{}, fmt.Errorf("no %s customer to ask about account %d",
			account.Provider, account.ID)
	}
	// The integration's *status* is deliberately not checked. See the note on
	// VirtualAccountSyncer: eligibility is the router's question, and this sweep
	// is not asking it.
	return standing, ramp.NewCustomerRef(account.CustomerID, integration.ExternalCustomerID), nil
}

// customerIDsOf collects the distinct customer ids in a page of accounts.
func customerIDsOf(accounts []models.CustomerVirtualAccount) []uint {
	if len(accounts) == 0 {
		return nil
	}
	seen := make(map[uint]struct{}, len(accounts))
	ids := make([]uint, 0, len(accounts))
	for i := range accounts {
		id := accounts[i].CustomerID
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// integrationsByCustomer indexes a batch by customer id for O(1) lookup in the
// walk.
func integrationsByCustomer(rows []models.CustomerProviderIntegration) map[uint]*models.CustomerProviderIntegration {
	byCustomer := make(map[uint]*models.CustomerProviderIntegration, len(rows))
	for i := range rows {
		row := &rows[i]
		byCustomer[row.CustomerID] = row
	}
	return byCustomer
}

// diverged reports whether the provider's view contradicts the stored row on
// anything the sweep tracks: status, destination and fee.
//
// A field the provider did not answer is not a divergence — it is silence, and
// MergeVirtualAccount would not write it either.
func diverged(row *models.CustomerVirtualAccount, current *ramp.VirtualAccount) bool {
	if current.Status != "" && current.Status != row.Status {
		return true
	}
	if current.Destination.Currency != "" && current.Destination.Currency != row.DestinationCurrency {
		return true
	}
	if current.Destination.PaymentRail != "" && current.Destination.PaymentRail != row.DestinationChain {
		return true
	}
	if current.Destination.Address != "" && current.Destination.Address != row.DestinationAddress {
		return true
	}
	if current.FeePercent == "" {
		return false
	}
	stored := ""
	if row.FeePercent != nil {
		stored = *row.FeePercent
	}
	return !decimalEqual(current.FeePercent, stored)
}
