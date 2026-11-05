package ramps_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support/race"
)

const pixRecipient = "ANA SOUZA"

// pixCreateHarness is the prepared create harness over a Pix payout BlindPay
// serves, with the quote a BlindPay Pix estimate persists.
type pixCreateHarness struct{ *bankCreateHarness }

func newPixCreateHarness(t *testing.T, withPrepare bool) *pixCreateHarness {
	t.Helper()
	base := buildPreparedCreateHarness(t, ramp.RailPixPayout,
		func(h *bankHarness, prepare *mocks.MockOfframpPrepareAdapter) ramp.ProviderAdapter {
			if withPrepare {
				return &preparingAdapter{MockProviderAdapter: h.blindpay, MockOfframpPrepareAdapter: prepare}
			}
			return h.blindpay
		})
	base.estimate = pixPreparedEstimate(time.Now().Add(5 * time.Minute))
	return &pixCreateHarness{base}
}

// pixPreparedEstimate is a binding quote of 100.00 USDC to a CPF key, as the
// prepared Pix estimate persists it.
func pixPreparedEstimate(expires time.Time) models.RampEstimate {
	snapshot, err := json.Marshal(map[string]any{
		"rail": "pix_payout", "source_amount": "100.00", "source_currency": "usdc",
		"destination_currency": "brl", "destination_payment_rail": "pix",
		"pix_key": pixKey, "pix_key_type": "cpf", "recipient_name": pixRecipient,
		"chain": "solana", "from_address": wallet,
		"rate": "5.40", "fee_percent": "0.50", "fee_amount": "0.50", "fee_currency": "usdc",
		"estimated_receive": "537.30", "beneficiary_revision": 0,
		"bank_account_id": pixBankID, "customer_external_id": blindpayCusID,
		"funding_mode": models.FundingModePrepared,
	})
	if err != nil {
		panic(err)
	}
	return models.RampEstimate{
		Model:      orm.Model{ID: bankEstimatePK},
		ExternalID: estimateID, CustomerID: userID, UserID: userID,
		Provider: models.ProviderBlindPay, Rail: "pix_payout", Binding: true,
		ExpiresAt: &expires, ProviderQuoteID: ptr("qu_1"), FeePercent: "0.50",
		RawRequest: "{}", RawResponse: string(snapshot),
	}
}

func pixCreateInput() dto.CreateOfframpDTO {
	return dto.CreateOfframpDTO{
		EstimateOfframpDTO: dto.EstimateOfframpDTO{
			AmountToken: "100.00", Token: "usdc", Chain: "solana", FromAddress: wallet,
			PixKey: pixKey, PixKeyType: "cpf", RecipientName: pixRecipient,
		},
		EstimateID: estimateID,
	}
}

func (h *pixCreateHarness) create() (*ramps.OrderView, bool, error) {
	return h.offramp.Create(context.Background(), user, person, pixCreateInput())
}

func TestCreatePixWithABlindPayQuoteReturnsTheFundingActionAndNoDepositAddress(t *testing.T) {
	h := newPixCreateHarness(t, true)

	view, created, err := h.create()

	require.NoError(t, err)
	require.True(t, created)
	rows := h.storedRows()
	require.Len(t, rows, 1)
	tx := rows[0]
	require.Equal(t, models.TxStatusAwaitingFunds, tx.Status)
	require.Equal(t, "pix_payout", *tx.Rail)
	require.Equal(t, models.ProviderBlindPay, tx.Provider)
	require.Equal(t, models.FundingModePrepared, *tx.FundingMode)
	require.Equal(t, ramps.DepositModePrepared, *tx.DepositMode)
	require.Equal(t, "537.30", tx.AmountFiat)
	require.Equal(t, "0", *tx.FeePercent, "the fee of the rail the quote was taken on")
	require.Empty(t, tx.GatewayOrderID, "the provider order is born at finalize")

	require.NotNil(t, view.FundingAction)
	require.Equal(t, ramps.DepositModePrepared, view.DepositMode)
	require.Empty(t, view.DepositInstructions)
	require.Equal(t, "537.30", view.AmountBRL)
	require.Equal(t, "***.533.447-**", view.PixKey, "a CPF key is masked as in every order view")
	require.Len(t, h.storedAuths(), 1)
	require.True(t, h.consumed())
	require.Equal(t, int32(1), h.prepares.Load())
}

func TestPreparedPixOrderCarriesNoBankFieldsAndARealBRLAmount(t *testing.T) {
	h := newPixCreateHarness(t, true)

	view, _, err := h.create()

	require.NoError(t, err)
	tx := h.storedRows()[0]
	require.Equal(t, models.FiatCurrencyBRL, tx.FiatCurrency)
	require.Nil(t, tx.BankBeneficiaryID)
	require.Nil(t, tx.BankRail)
	require.Nil(t, tx.BankAccountType)
	require.Nil(t, tx.BankAccountLast4)
	require.Nil(t, tx.BankRoutingLast4)
	require.NotEqual(t, "0", tx.AmountFiat)
	require.Equal(t, models.FiatCurrencyBRL, view.FiatCurrency)
	require.Empty(t, view.BankRail)
	require.Empty(t, view.BankAccountLast4)
}

func TestCreatePixReplayOfAConsumedQuoteAnswersTheSameOrderAndAction(t *testing.T) {
	h := newPixCreateHarness(t, true)

	first, created, err := h.create()
	require.NoError(t, err)
	require.True(t, created)
	second, created, err := h.create()

	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, first.FundingAction, second.FundingAction)
	require.Equal(t, int32(1), h.prepares.Load())
	require.Len(t, h.storedRows(), 1)
}

func TestCreatePixReplayWithAnotherKeyIsStillAMismatch(t *testing.T) {
	h := newPixCreateHarness(t, true)
	_, _, err := h.create()
	require.NoError(t, err)

	in := pixCreateInput()
	in.PixKey = "52998224725"
	_, _, err = h.offramp.Create(context.Background(), user, person, in)

	require.ErrorIs(t, err, ramp.ErrEstimateMismatch)
	require.Len(t, h.storedRows(), 1)
}

func TestCreatePixDoubleTapPreparesOnce(t *testing.T) {
	for name, c := range map[string]struct {
		serialize bool
		want      int32
	}{
		"serialized":               {serialize: true, want: 1},
		"control: lock admits all": {serialize: false, want: 2},
	} {
		t.Run(name, func(t *testing.T) {
			h := newPixCreateHarness(t, true)
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

func TestCreatePixStaysWithTheQuotesProviderWhenTheProviderOrderChanged(t *testing.T) {
	h := newPixCreateHarness(t, true)
	// Bridge became the only provider of the rail after the quote was taken.
	h.bridge.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()

	view, _, err := h.create()

	require.NoError(t, err)
	require.Equal(t, models.ProviderBlindPay, h.storedRows()[0].Provider)
	require.NotNil(t, view.FundingAction)
	h.bridge.AssertNotCalled(t, "CreateOfframp", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

func TestCreatePixBridgeQuoteStillPlacesADepositOrderAndAnswersEstimateConsumedOnReplay(t *testing.T) {
	h := newPixCreateHarness(t, true)
	// A deposit quote carries no funding mode: it is consumed by the provider's
	// own create, and a replay of it answers estimate_consumed as before.
	h.estimate.Provider = models.ProviderBridge
	consumed := time.Now()
	h.estimate.ConsumedAt = &consumed
	var snapshot map[string]any
	require.NoError(t, json.Unmarshal([]byte(h.estimate.RawResponse), &snapshot))
	delete(snapshot, "funding_mode")
	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	h.estimate.RawResponse = string(raw)

	_, _, err = h.create()

	require.ErrorIs(t, err, ramp.ErrEstimateConsumed)
	require.Empty(t, h.storedRows())
	require.Zero(t, h.prepares.Load())
}

func TestCreatePixAnyPrepareFailureFreesTheReservationAndLeavesTheQuoteRedeemable(t *testing.T) {
	h := newPixCreateHarness(t, true)
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
	require.True(t, h.consumed())
}

func TestCreatePixMismatchedKeyOrNameOrAmountIsRefusedBeforeAnyReservation(t *testing.T) {
	for name, mutate := range map[string]func(*dto.CreateOfframpDTO){
		"key":    func(in *dto.CreateOfframpDTO) { in.PixKey = "52998224725" },
		"name":   func(in *dto.CreateOfframpDTO) { in.RecipientName = "OUTRA PESSOA" },
		"amount": func(in *dto.CreateOfframpDTO) { in.AmountToken = "101.00" },
		"wallet": func(in *dto.CreateOfframpDTO) { in.FromAddress = "another" },
	} {
		t.Run(name, func(t *testing.T) {
			h := newPixCreateHarness(t, true)
			in := pixCreateInput()
			mutate(&in)

			_, _, err := h.offramp.Create(context.Background(), user, person, in)

			require.ErrorIs(t, err, ramp.ErrEstimateMismatch)
			require.Empty(t, h.storedRows())
			require.Zero(t, h.prepares.Load())
			require.False(t, h.consumed())
		})
	}
}

func TestCreatePixOfACompanyRequiresItsOwnRulesAndTheStepUp(t *testing.T) {
	h := newPixCreateHarness(t, true)
	h.mfa.ExpectedCalls = nil
	company := theCompany(models.CustomerStatusActive)
	businesses := mocks.NewMockCustomerBusinessProfileRepository(t)
	businesses.EXPECT().FindByCustomers(testifymock.Anything).
		Return(map[uint]models.BusinessProfile{businessID: {LegalName: companyLegalName}}, nil).Maybe()
	h.deps.Businesses = businesses
	h.rebuild(t, h.deps)
	h.estimate.CustomerID = company.ID
	h.estimateRepo.ExpectedCalls = nil
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(company.ID, estimateID).
		RunAndReturn(func(uint, string) (*models.RampEstimate, error) { row := h.estimate; return &row, nil }).Maybe()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(company.ID, models.ProviderBlindPay, bankEstimatePK, models.TransactionTypeOfframp).
		Return(nil, nil).Maybe()
	in := pixCreateInput()
	in.PixKey, in.PixKeyType, in.RecipientName = companyCNPJ, "cnpj", companyLegalName
	var snapshot map[string]any
	require.NoError(t, json.Unmarshal([]byte(h.estimate.RawResponse), &snapshot))
	snapshot["pix_key"], snapshot["pix_key_type"], snapshot["recipient_name"] = companyCNPJ, "cnpj", companyLegalName
	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	h.estimate.RawResponse = string(raw)

	_, _, err = h.offramp.Create(context.Background(), user, company, in)

	require.ErrorIs(t, err, ramps.ErrWalletNotRegistered)
	h.mfa.AssertNotCalled(t, "Enrolled", testifymock.Anything)
	require.Empty(t, h.storedRows())

	h.registered[wallet] = businessID
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(true, nil).Once()

	_, _, err = h.offramp.Create(context.Background(), user, company, in)

	require.ErrorIs(t, err, ramp.ErrMfaStepUpRequired)
	require.Empty(t, h.storedRows())
}

func TestCreatePixWithoutThePreparePortIsUnavailable(t *testing.T) {
	h := newPixCreateHarness(t, false)

	_, _, err := h.create()

	require.ErrorIs(t, err, ramp.ErrOfframpCreateUnavailable)
	require.Empty(t, h.storedRows())
}
