package ramps_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	bankdto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/bank"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support/race"
)

const (
	bankEstimatePK     = uint(9)
	bankBeneficiaryPK  = uint(42)
	unsignedTx         = "unsigned-solana-transaction-bytes"
	correlationState   = `{"quote_id":"qu_1","owner_address":"8nWZBsolanawallet"}`
	messageFingerprint = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
)

// preparingAdapter is BlindPay with the prepare port the bank create
// type-asserts for.
type preparingAdapter struct {
	*mocks.MockProviderAdapter
	*mocks.MockOfframpPrepareAdapter
}

// bankCreateHarness is the bank harness with stateful fakes behind the
// create: the quote, the reserved rows, the authorizations and the cipher
// each read back what the create wrote, so a second create sees the first.
type bankCreateHarness struct {
	*bankHarness
	prepare       *mocks.MockOfframpPrepareAdapter
	txRepo        *mocks.MockTransactionRepository
	funding       *mocks.MockOfframpFundingAuthorizationRepository
	beneficiaries *mocks.MockBankBeneficiaryReader

	mu       sync.Mutex
	estimate models.RampEstimate
	rows     []*models.Transaction
	auths    []*models.OfframpFundingAuthorization
	// sealed maps each ciphertext the fake cipher produced to its plaintext.
	sealed map[string]string
	// revision is the beneficiary's current revision; the quote priced 3.
	revision int
	// reserveHook runs before a reservation is stored (the race's barrier).
	reserveHook func()
	// prepares counts PrepareOfframp calls; prepareErr fails the next one.
	prepares   atomic.Int32
	prepareErr error
	// prepared, when set, is what the next prepares answer instead of
	// preparedFunding().
	prepared func() *ramp.PreparedOfframpFunding
	// published counts the order events broadcast.
	published atomic.Int32
}

func newBankCreateHarness(t *testing.T, withPrepare bool) *bankCreateHarness {
	t.Helper()
	return buildBankCreateHarness(t, func(h *bankHarness, prepare *mocks.MockOfframpPrepareAdapter) ramp.ProviderAdapter {
		if withPrepare {
			return &preparingAdapter{MockProviderAdapter: h.blindpay, MockOfframpPrepareAdapter: prepare}
		}
		return h.blindpay
	})
}

// buildBankCreateHarness builds the harness with first as the adapter BlindPay
// is served by, handed the prepare mock: the optional ports it implements are
// the test's to choose.
func buildBankCreateHarness(
	t *testing.T, first func(*bankHarness, *mocks.MockOfframpPrepareAdapter) ramp.ProviderAdapter,
) *bankCreateHarness {
	t.Helper()
	return buildPreparedCreateHarness(t, ramp.RailACHPayout, first)
}

// buildPreparedCreateHarness is buildBankCreateHarness for a rail whose
// payout is funded by a prepared action: ACH, wire, or a Pix payout BlindPay
// serves.
func buildPreparedCreateHarness(
	t *testing.T, rail ramp.Rail, first func(*bankHarness, *mocks.MockOfframpPrepareAdapter) ramp.ProviderAdapter,
) *bankCreateHarness {
	t.Helper()
	prepare := mocks.NewMockOfframpPrepareAdapter(t)
	base := buildBankHarness(t, rail, func(h *bankHarness) ramp.ProviderAdapter { return first(h, prepare) })
	h := &bankCreateHarness{
		bankHarness: base, prepare: prepare,
		txRepo:        mocks.NewMockTransactionRepository(t),
		funding:       mocks.NewMockOfframpFundingAuthorizationRepository(t),
		beneficiaries: mocks.NewMockBankBeneficiaryReader(t),
		estimate:      bankEstimate(rail, time.Now().Add(5*time.Minute)),
		sealed:        map[string]string{},
		revision:      3,
	}
	h.fakeCipher()
	h.fakeEstimates()
	h.fakeTransactions()
	h.fakeFunding()
	h.beneficiaries.EXPECT().GetForPayout(testifymock.Anything, beneficiaryID).
		RunAndReturn(func(uint, string) (*bankdto.PayoutBeneficiaryDTO, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return &bankdto.PayoutBeneficiaryDTO{
				ID: bankBeneficiaryPK,
				BeneficiaryRecordDTO: bankdto.BeneficiaryRecordDTO{
					ExternalID: beneficiaryID, Revision: h.revision,
					AccountClass: "individual", AccountType: "checking", AccountLast4: "6789", RoutingLast4: "0021",
				},
			}, nil
		}).Maybe()
	prepare.EXPECT().PrepareOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(context.Context, ramp.CustomerRef, ramp.OfframpPrepareRequest) (*ramp.PreparedOfframpFunding, error) {
			h.prepares.Add(1)
			h.mu.Lock()
			err := h.prepareErr
			h.prepareErr = nil
			h.mu.Unlock()
			if err != nil {
				return nil, err
			}
			if h.prepared != nil {
				return h.prepared(), nil
			}
			return preparedFunding(), nil
		}).Maybe()
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Maybe()

	broadcaster := mocks.NewMockBroadcaster(t)
	broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).
		Run(func(uint, broadcast.TransactionEvent) { h.published.Add(1) }).Maybe()
	h.deps.Transactions = h.txRepo
	h.deps.FundingAuthorizations = h.funding
	h.deps.Beneficiaries = h.beneficiaries
	h.deps.Broadcaster = broadcaster
	h.rebuild(t, h.deps)
	return h
}

// bankEstimate is a binding ACH quote of 100.00 USDC to the beneficiary at
// revision 3, as estimateBank persists it.
func bankEstimate(rail ramp.Rail, expires time.Time) models.RampEstimate {
	snapshot, err := json.Marshal(map[string]any{
		"rail": rail.String(), "source_amount": "100.00", "source_currency": "usdc",
		"destination_currency": "usd", "destination_payment_rail": "ach",
		"chain": "solana", "from_address": wallet,
		"rate": "0.998000", "fee_percent": "0", "fee_amount": "0.00", "fee_currency": "usdc",
		"estimated_receive": "97.30", "provider_fee_amount": "2.70",
		"bank_beneficiary_id": beneficiaryID, "beneficiary_revision": 3,
		"bank_account_id": blindpayBankAccountID, "customer_external_id": blindpayCusID,
	})
	if err != nil {
		panic(err)
	}
	return models.RampEstimate{
		Model:      orm.Model{ID: bankEstimatePK},
		ExternalID: estimateID, CustomerID: userID, UserID: userID,
		Provider: models.ProviderBlindPay, Rail: rail.String(), Binding: true,
		ExpiresAt: &expires, ProviderQuoteID: ptr("qu_1"), FeePercent: "0",
		RawRequest: "{}", RawResponse: string(snapshot),
	}
}

func ptr[T any](v T) *T { return &v }

func preparedFunding() *ramp.PreparedOfframpFunding {
	return &ramp.PreparedOfframpFunding{
		Action: ramp.FundingAction{
			Type:           ramp.FundingActionSolanaTransaction,
			SubmissionMode: ramp.SubmissionBackendFinalize,
			Payload: ramp.FundingActionPayload{
				ContentType:         ramp.ContentTypeSolanaTransaction,
				UnsignedTransaction: base64.StdEncoding.EncodeToString([]byte(unsignedTx)),
			},
			ExpiresAt: time.Now().Add(time.Minute),
		},
		CorrelationState:   []byte(correlationState),
		MessageFingerprint: messageFingerprint,
	}
}

func bankCreateInput() dto.CreateOfframpDTO {
	return dto.CreateOfframpDTO{EstimateOfframpDTO: bankInput(ramp.RailACHPayout), EstimateID: estimateID}
}

func (h *bankCreateHarness) create() (*ramps.OrderView, bool, error) {
	return h.offramp.Create(context.Background(), user, person, bankCreateInput())
}

// fakeCipher stands in for facades.Crypt(): a ciphertext is an opaque
// handle, so whatever reaches the repository holds nothing in the clear.
func (h *bankCreateHarness) fakeCipher() {
	crypt := h.crypt
	crypt.EXPECT().EncryptString(testifymock.Anything).RunAndReturn(func(plain string) (string, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		sealed := fmt.Sprintf("sealed-%d", len(h.sealed))
		h.sealed[sealed] = plain
		return sealed, nil
	}).Maybe()
	crypt.EXPECT().DecryptString(testifymock.Anything).RunAndReturn(func(sealed string) (string, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		plain, ok := h.sealed[sealed]
		if !ok {
			return "", errors.New("not a ciphertext of this cipher")
		}
		return plain, nil
	}).Maybe()
}

func (h *bankCreateHarness) fakeEstimates() {
	read := func() *models.RampEstimate {
		h.mu.Lock()
		defer h.mu.Unlock()
		row := h.estimate
		return &row
	}
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).
		RunAndReturn(func(uint, string) (*models.RampEstimate, error) { return read(), nil }).Maybe()
	h.estimateRepo.EXPECT().FindByCustomerAndID(userID, bankEstimatePK).
		RunAndReturn(func(uint, uint) (*models.RampEstimate, error) { return read(), nil }).Maybe()
}

// fakeTransactions keeps the reserved rows: the dedup finds the one still
// holding the quote, as the unique on (customer, provider, estimate) does.
func (h *bankCreateHarness) fakeTransactions() {
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderBlindPay, bankEstimatePK, models.TransactionTypeOfframp).
		RunAndReturn(func(uint, string, uint, string) (*models.Transaction, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			for _, row := range h.rows {
				if row.EstimateID != nil && *row.EstimateID == bankEstimatePK {
					found := *row
					return &found, nil
				}
			}
			return nil, nil
		}).Maybe()
	h.txRepo.EXPECT().Create(testifymock.Anything).RunAndReturn(func(tx *models.Transaction) error {
		if h.reserveHook != nil {
			h.reserveHook()
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		tx.ID = uint(len(h.rows) + 1)
		stored := *tx
		h.rows = append(h.rows, &stored)
		return nil
	}).Maybe()
	h.txRepo.EXPECT().Update(testifymock.Anything).RunAndReturn(func(tx *models.Transaction) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		for i, row := range h.rows {
			if row.ID == tx.ID {
				stored := *tx
				h.rows[i] = &stored
			}
		}
		return nil
	}).Maybe()
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, testifymock.Anything).
		RunAndReturn(func(_ uint, externalID string) (*models.Transaction, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			for _, row := range h.rows {
				if row.ExternalID == externalID {
					found := *row
					return &found, nil
				}
			}
			return nil, nil
		}).Maybe()
}

// fakeFunding has the repository's semantics: the authorization and the
// quote's consumption land together, and a consumed quote or a reservation
// no longer pending refuses both.
func (h *bankCreateHarness) fakeFunding() {
	h.funding.EXPECT().CreatePreparedConsumingEstimate(testifymock.Anything, bankEstimatePK, testifymock.Anything).
		RunAndReturn(func(a *models.OfframpFundingAuthorization, _ uint, fundableUntil time.Time) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.estimate.ConsumedAt != nil {
				return errors.New("estimate already consumed")
			}
			for _, row := range h.rows {
				if row.ID == a.TransactionID && row.Status != models.TxStatusPending {
					return errors.New("transaction no longer pending")
				}
			}
			a.ID, a.State = uint(len(h.auths)+1), models.FundingAuthPrepared
			stored := *a
			h.auths = append(h.auths, &stored)
			now := time.Now()
			h.estimate.ConsumedAt = &now
			for _, row := range h.rows {
				if row.ID == a.TransactionID {
					row.Status, row.ExpiresAt = models.TxStatusAwaitingFunds, &fundableUntil
				}
			}
			return nil
		}).Maybe()
	h.funding.EXPECT().FindByTransaction(testifymock.Anything).
		RunAndReturn(func(txID uint) (*models.OfframpFundingAuthorization, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			for _, a := range h.auths {
				if a.TransactionID == txID {
					found := *a
					return &found, nil
				}
			}
			return nil, nil
		}).Maybe()
}

func (h *bankCreateHarness) storedRows() []models.Transaction {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]models.Transaction, len(h.rows))
	for i, row := range h.rows {
		out[i] = *row
	}
	return out
}

func (h *bankCreateHarness) storedAuths() []models.OfframpFundingAuthorization {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]models.OfframpFundingAuthorization, len(h.auths))
	for i, a := range h.auths {
		out[i] = *a
	}
	return out
}

func (h *bankCreateHarness) consumed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.estimate.ConsumedAt != nil
}

// The order awaits its funds with the bank leg the quote priced, the
// prepared action is stored encrypted, the quote is spent with it, and the
// order answers the action the wallet signs.
func TestCreateBankPlacesAnOrderAwaitingASignedFunding(t *testing.T) {
	h := newBankCreateHarness(t, true)
	quoteExpiry := *h.estimate.ExpiresAt

	view, created, err := h.create()

	require.NoError(t, err)
	require.True(t, created)
	rows := h.storedRows()
	require.Len(t, rows, 1)
	tx := rows[0]
	require.Equal(t, models.TxStatusAwaitingFunds, tx.Status)
	require.Equal(t, models.TransactionTypeOfframp, tx.Type)
	require.Equal(t, models.ProviderBlindPay, tx.Provider)
	require.Equal(t, "ach_payout", *tx.Rail)
	require.Equal(t, "100.00", tx.AmountToken)
	require.Equal(t, "usdc", tx.TargetToken)
	require.Equal(t, wallet, *tx.FromAddress)
	require.Equal(t, "solana", *tx.Chain)
	require.Equal(t, "97.30", tx.AmountFiat)
	require.Equal(t, "usd", tx.FiatCurrency)
	require.Equal(t, bankBeneficiaryPK, *tx.BankBeneficiaryID)
	require.Equal(t, "ach_payout", *tx.BankRail)
	require.Equal(t, "checking", *tx.BankAccountType)
	require.Equal(t, "6789", *tx.BankAccountLast4)
	require.Equal(t, "0021", *tx.BankRoutingLast4)
	require.Equal(t, "0", *tx.FeePercent)
	require.Equal(t, bankEstimatePK, *tx.EstimateID)
	require.Empty(t, tx.GatewayOrderID, "the provider order is born at finalize (E7)")
	require.WithinDuration(t, quoteExpiry, *tx.ExpiresAt, time.Second, "fundable while the quote lives")
	h.prepare.AssertCalled(t, "PrepareOfframp", testifymock.Anything,
		ramp.CustomerRef{CustomerID: userID, ExternalID: blindpayCusID},
		ramp.OfframpPrepareRequest{
			ProviderQuoteID: "qu_1", OwnerAddress: wallet, Token: "usdc", Chain: "solana",
			SourceAmount: "100.00", QuoteExpiresAt: quoteExpiry,
		})

	auths := h.storedAuths()
	require.Len(t, auths, 1)
	auth := auths[0]
	require.Equal(t, tx.ID, auth.TransactionID)
	require.Equal(t, models.ProviderBlindPay, auth.Provider)
	require.Equal(t, models.FundingAuthPrepared, auth.State)
	require.Equal(t, 1, auth.ActionRevision)
	require.Equal(t, ramp.FundingActionSolanaTransaction, auth.ActionType)
	require.Equal(t, ramp.SubmissionBackendFinalize, auth.SubmissionMode)
	require.Equal(t, messageFingerprint, auth.UnsignedActionFingerprint)
	encoded := base64.StdEncoding.EncodeToString([]byte(unsignedTx))
	for _, sealed := range []string{auth.ActionPayloadCiphertext, auth.ProviderStateCiphertext} {
		require.NotContains(t, sealed, encoded, "the transaction never reaches the database in the clear")
		require.NotContains(t, sealed, "qu_1")
	}
	require.Contains(t, h.sealed[auth.ActionPayloadCiphertext], encoded)
	state, err := base64.StdEncoding.DecodeString(h.sealed[auth.ProviderStateCiphertext])
	require.NoError(t, err)
	require.Equal(t, correlationState, string(state))
	require.True(t, h.consumed())

	require.Equal(t, tx.ExternalID, view.ID)
	require.Equal(t, models.TxStatusAwaitingFunds, view.Status)
	require.Empty(t, view.AmountBRL, "a bank payout has no BRL leg")
	require.Equal(t, "97.30", view.AmountFiat)
	require.Equal(t, "usd", view.FiatCurrency)
	require.Equal(t, "ach_payout", view.BankRail)
	require.Equal(t, "6789", view.BankAccountLast4)
	require.Equal(t, "0021", view.BankRoutingLast4)
	require.Equal(t, &ramps.FundingActionView{
		Type: ramp.FundingActionSolanaTransaction, SubmissionMode: ramp.SubmissionBackendFinalize,
		ContentType: ramp.ContentTypeSolanaTransaction, UnsignedTransaction: encoded,
		ExpiresAt: auth.ExpiresAt, ActionRevision: 1,
	}, view.FundingAction)
}

// A double tap lands on the same order and the same action: the provider is
// asked to prepare once.
func TestCreateBankTwiceAnswersTheSameOrderAndAction(t *testing.T) {
	h := newBankCreateHarness(t, true)

	first, created, err := h.create()
	require.NoError(t, err)
	require.True(t, created)
	second, created, err := h.create()

	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, second.ID)
	require.NotNil(t, second.FundingAction)
	require.Equal(t, first.FundingAction, second.FundingAction)
	require.Equal(t, int32(1), h.prepares.Load())
	require.Len(t, h.storedRows(), 1)
}

// Two creates at once reserve one order and prepare it once: the create lock
// is what serializes them, and without it both would.
func TestCreateBankConcurrentCreatesPrepareOnce(t *testing.T) {
	for name, c := range map[string]struct {
		serialize bool
		want      int32
	}{
		"serialized":               {serialize: true, want: 1},
		"control: lock admits all": {serialize: false, want: 2},
	} {
		t.Run(name, func(t *testing.T) {
			h := newBankCreateHarness(t, true)
			pair := race.New()
			h.reserveHook = pair.Write
			h.deps.Locker = pair.Locker(t, c.serialize)
			h.rebuild(t, h.deps)

			views := make([]*ramps.OrderView, 2)
			var next atomic.Int32
			errs := pair.Run(2, func() error {
				view, _, err := h.create()
				views[next.Add(1)-1] = view
				return err
			})

			require.Equal(t, c.want, h.prepares.Load())
			require.Len(t, h.storedRows(), int(c.want))
			if c.serialize {
				for _, err := range errs {
					require.NoError(t, err)
				}
				require.Equal(t, views[0].ID, views[1].ID)
			}
		})
	}
}

// A beneficiary edited since the quote is not paid at the quoted terms, and
// nothing is reserved or prepared.
func TestCreateBankWithAnEditedBeneficiaryIsRefused(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.revision = 4

	_, _, err := h.create()

	require.ErrorIs(t, err, ramp.ErrBeneficiaryChanged)
	require.Zero(t, h.prepares.Load())
	require.Empty(t, h.storedRows())
	require.False(t, h.consumed())
}

func TestCreateBankWithAnExpiredQuoteIsRefused(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.estimate = bankEstimate(ramp.RailACHPayout, time.Now().Add(-time.Second))

	_, _, err := h.create()

	require.ErrorIs(t, err, ramp.ErrEstimateExpired)
	require.Zero(t, h.prepares.Load())
	require.Empty(t, h.storedRows())
}

// A Pix quote is not a quote for a bank payout, and reads as absent.
func TestCreateBankWithAPixQuoteIsNotFound(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.estimate = bankEstimate(ramp.RailPixPayout, time.Now().Add(5*time.Minute))

	_, _, err := h.create()

	require.ErrorIs(t, err, ramps.ErrEstimateNotFound)
	require.Empty(t, h.storedRows())
}

// A body that differs from the quote is refused before the beneficiary is
// read.
func TestCreateBankDivergingFromTheQuoteIsRefused(t *testing.T) {
	h := newBankCreateHarness(t, true)
	in := bankCreateInput()
	in.AmountToken = "100.01"

	_, _, err := h.offramp.Create(context.Background(), user, person, in)

	require.ErrorIs(t, err, ramp.ErrEstimateMismatch)
	h.beneficiaries.AssertNotCalled(t, "GetForPayout", testifymock.Anything, testifymock.Anything)
	require.Empty(t, h.storedRows())
}

// A prepare that failed moved no money: the reservation is freed, the quote
// is not spent, and the next create goes through.
func TestCreateBankWhosePrepareFailedLeavesTheQuoteRedeemable(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.prepareErr = fmt.Errorf("blindpay: prepare: %w", ramp.ErrProviderFailed)

	_, _, err := h.create()

	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	rows := h.storedRows()
	require.Len(t, rows, 1)
	require.Equal(t, models.TxStatusFailed, rows[0].Status)
	require.Nil(t, rows[0].EstimateID, "the quote is released")
	require.Empty(t, h.storedAuths())
	require.False(t, h.consumed())

	view, created, err := h.create()

	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, view.FundingAction)
	require.Len(t, h.storedRows(), 2)
	require.NotEqual(t, rows[0].ExternalID, view.ID)
	require.True(t, h.consumed())
}

// A prepare answer the contract does not carry is the provider's failure,
// and frees the reservation like any other.
func TestCreateBankRefusesAnUnusablePreparedAction(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.prepare.ExpectedCalls = nil
	bad := preparedFunding()
	bad.Action.SubmissionMode = "wallet_submits"
	h.prepare.EXPECT().PrepareOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).Return(bad, nil).Once()

	_, _, err := h.create()

	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	require.Equal(t, models.TxStatusFailed, h.storedRows()[0].Status)
	require.Empty(t, h.storedAuths())
	require.False(t, h.consumed())
}

// A provider that cannot prepare a funding action cannot place a bank
// payout; nothing is reserved.
func TestCreateBankWithoutThePreparePortIsUnavailable(t *testing.T) {
	h := newBankCreateHarness(t, false)

	_, _, err := h.create()

	require.ErrorIs(t, err, ramp.ErrOfframpCreateUnavailable)
	require.Empty(t, h.storedRows())
}

// A service built without the bank ports is a wiring fault, not an outage:
// it answers 500, never the 503 that tells the app to try later.
func TestCreateBankWithoutTheBankPortsIsAWiringFault(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.deps.FundingAuthorizations = nil
	h.rebuild(t, h.deps)

	_, _, err := h.create()

	require.ErrorIs(t, err, ramp.ErrOfframpCreateUnavailable)
	require.NotErrorIs(t, err, ramp.ErrNoProviderAvailable)
	require.Empty(t, h.storedRows())
}

// A person with MFA on proves presence before anything else is read.
func TestCreateBankWithoutTheStepUpIsRefusedFirst(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.mfa.ExpectedCalls = nil
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(true, nil).Once()

	_, _, err := h.create()

	require.ErrorIs(t, err, ramp.ErrMfaStepUpRequired)
	h.estimateRepo.AssertNotCalled(t, "FindByCustomerAndExternalID", testifymock.Anything, testifymock.Anything)
	h.beneficiaries.AssertNotCalled(t, "GetForPayout", testifymock.Anything, testifymock.Anything)
	require.Zero(t, h.prepares.Load())
}

// A business owes its money rules before its step-up, so a wallet it did not
// register never spends an MFA attempt.
func TestCreateBankForABusinessRunsItsRulesBeforeTheStepUp(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.mfa.ExpectedCalls = nil
	company := theCompany(models.CustomerStatusActive)
	h.estimate.CustomerID = company.ID
	h.estimateRepo.ExpectedCalls = nil
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(company.ID, estimateID).
		RunAndReturn(func(uint, string) (*models.RampEstimate, error) { row := h.estimate; return &row, nil }).Maybe()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(company.ID, models.ProviderBlindPay, bankEstimatePK, models.TransactionTypeOfframp).
		Return(nil, nil).Maybe()

	_, _, err := h.offramp.Create(context.Background(), user, company, bankCreateInput())

	require.ErrorIs(t, err, ramps.ErrWalletNotRegistered)
	h.mfa.AssertNotCalled(t, "Enrolled", testifymock.Anything)
	require.Empty(t, h.storedRows())

	h.registered[wallet] = businessID
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(true, nil).Once()

	_, _, err = h.offramp.Create(context.Background(), user, company, bankCreateInput())

	require.ErrorIs(t, err, ramp.ErrMfaStepUpRequired)
	require.Empty(t, h.storedRows())
}

// The order shows its action only while it is valid: past its expiry the
// app refreshes it (Task 7) instead of signing a dead one.
func TestBankOrderShowsNoExpiredFundingAction(t *testing.T) {
	h := newBankCreateHarness(t, true)
	view, _, err := h.create()
	require.NoError(t, err)

	h.mu.Lock()
	h.auths[0].ExpiresAt = time.Now().Add(-time.Second)
	h.mu.Unlock()
	shown, err := h.offramp.Show(context.Background(), userID, view.ID)

	require.NoError(t, err)
	require.Equal(t, view.ID, shown.ID)
	require.Nil(t, shown.FundingAction)
}

// The plaintext the cipher was handed for the action names no field beyond
// the payload's own.
func TestCreateBankSealsOnlyThePayload(t *testing.T) {
	h := newBankCreateHarness(t, true)
	_, _, err := h.create()
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(h.sealed[h.storedAuths()[0].ActionPayloadCiphertext]), &payload))
	require.Equal(t, map[string]any{
		"content_type":         ramp.ContentTypeSolanaTransaction,
		"unsigned_transaction": base64.StdEncoding.EncodeToString([]byte(unsignedTx)),
	}, payload)
	require.False(t, strings.Contains(h.storedAuths()[0].ActionPayloadCiphertext, "content_type"))
}
