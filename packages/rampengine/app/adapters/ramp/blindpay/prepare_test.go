package blindpay_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// The sandbox values BlindPay's prepare answered with: the USDB test mint and
// the delegate its funding approves. Public addresses; the adapter reads them
// from the client's config, never from code.
const (
	sandboxMint     = "oJSVoRMCyyPg8Y5TNt6t6o5NmVdRiwtis3B8mjZmUpc"
	sandboxDelegate = "AD7Gb17nRVZ9CnCykBmiwj8DNfVMksUVBGzCU8SzeYy6"
	// prepareAmount is the quote's source amount: 10_120_000 base units at 6.
	prepareAmount = "10.12"
)

var prepareNow = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// testKey is a fixed private key derived from a label.
func testKey(label string) solana.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, label)
	return solana.PrivateKey(ed25519.NewKeyFromSeed(seed))
}

var (
	prepareOwner = testKey("xip blindpay prepare owner")
	otherSigner  = testKey("xip blindpay someone else")
)

// delegationTx builds the transaction the way BlindPay does (legacy, one
// signer, header {1,0,3}, five keys, one ApproveChecked), changed by edit.
type delegationTx struct {
	program  solana.PublicKey
	amount   uint64
	decimals uint8
	extra    []solana.Instruction
}

func validDelegation() delegationTx {
	return delegationTx{program: solana.TokenProgramID, amount: 10_120_000, decimals: 6}
}

func (d delegationTx) build(t *testing.T) *solana.Transaction {
	t.Helper()
	owner := prepareOwner.PublicKey()
	mint := solana.MustPublicKeyFromBase58(sandboxMint)
	source, _, err := solana.FindProgramAddress([][]byte{owner[:], d.program[:], mint[:]}, solana.SPLAssociatedTokenAccountProgramID)
	require.NoError(t, err)
	data := []byte{13}
	data = binary.LittleEndian.AppendUint64(data, d.amount)
	data = append(data, d.decimals)
	approve := solana.NewInstruction(d.program, solana.AccountMetaSlice{
		solana.Meta(source).WRITE(),
		solana.Meta(mint),
		solana.Meta(solana.MustPublicKeyFromBase58(sandboxDelegate)),
		solana.Meta(owner).SIGNER(),
	}, data)
	builder := solana.NewTransactionBuilder().
		AddInstruction(approve).
		SetRecentBlockHash(solana.Hash(sha256.Sum256([]byte("xip blindpay blockhash")))).
		SetFeePayer(owner)
	for _, ix := range d.extra {
		builder = builder.AddInstruction(ix)
	}
	tx, err := builder.Build()
	require.NoError(t, err)
	return tx
}

func encodeTx(t *testing.T, tx *solana.Transaction) string {
	t.Helper()
	raw, err := tx.MarshalBinary()
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(raw)
}

func prepareAnswer(t *testing.T, tx *solana.Transaction) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"success": true, "transaction": encodeTx(t, tx), "debug": map[string]any{"slot": 1}})
	require.NoError(t, err)
	return string(body)
}

func sandboxMints() map[string]blindpay.MintConfig {
	usdb := blindpay.MintConfig{Address: sandboxMint, Decimals: 6}
	return map[string]blindpay.MintConfig{"USDC": usdb, "USDT": usdb}
}

// prepareServer answers POST prepare-delegate-solana with answer and records
// every call. cfg edits the client config before the client is built.
func prepareServer(t *testing.T, answer stubAnswer, cfg func(*blindpay.Config)) (*blindpay.Adapter, func() []feeCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []feeCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, feeCall{r.Method, r.URL.EscapedPath(), r.Header.Get("Idempotency-Key"), body})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method+" "+strings.TrimPrefix(r.URL.EscapedPath(), instancePrefix) != "POST prepare-delegate-solana" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":"NOT_ROUTED"}`)
			return
		}
		w.WriteHeader(answer.status)
		_, _ = io.WriteString(w, answer.body)
	}))
	t.Cleanup(server.Close)
	config := blindpay.Config{
		APIKey: "bp_test_key", InstanceID: "in_000000000001", Mode: models.ProviderModeSandbox, BaseURL: server.URL + "/v1",
		Mints: sandboxMints(), Delegate: sandboxDelegate,
	}
	if cfg != nil {
		cfg(&config)
	}
	client := blindpay.NewClient(config)
	adapter := blindpay.New(func(context.Context) (*blindpay.Client, error) { return client, nil }, nil, nil)
	blindpay.SetClock(adapter, func() time.Time { return prepareNow })
	return adapter, func() []feeCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]feeCall(nil), calls...)
	}
}

func prepareRequest() ramp.OfframpPrepareRequest {
	return ramp.OfframpPrepareRequest{
		ProviderQuoteID: quoteID,
		OwnerAddress:    prepareOwner.PublicKey().String(),
		Token:           "USDC",
		Chain:           "solana",
		SourceAmount:    prepareAmount,
		QuoteExpiresAt:  prepareNow.Add(5 * time.Minute),
	}
}

func messageFingerprint(t *testing.T, tx *solana.Transaction) string {
	t.Helper()
	message, err := tx.Message.MarshalBinary()
	require.NoError(t, err)
	sum := sha256.Sum256(message)
	return hex.EncodeToString(sum[:])
}

func TestPrepareOfframp_AcceptsTheExactDelegationAndRecordsIt(t *testing.T) {
	tx := validDelegation().build(t)
	adapter, calls := prepareServer(t, stubAnswer{200, prepareAnswer(t, tx)}, nil)

	prepared, err := adapter.PrepareOfframp(context.Background(), payinRef, prepareRequest())
	require.NoError(t, err)

	got := calls()
	require.Len(t, got, 1)
	assert.Equal(t, "POST", got[0].method)
	assert.Equal(t, instancePrefix+"prepare-delegate-solana", got[0].path)
	assert.Empty(t, got[0].idempotency, "a prepare creates nothing to deduplicate")
	assert.JSONEq(t, `{"owner_address":"`+prepareOwner.PublicKey().String()+`","quote_id":"`+quoteID+`"}`, string(got[0].body))

	// The shape the sandbox answers.
	require.Equal(t, solana.MessageVersionLegacy, tx.Message.GetVersion())
	require.Equal(t, solana.MessageHeader{NumRequiredSignatures: 1, NumReadonlySignedAccounts: 0, NumReadonlyUnsignedAccounts: 3}, tx.Message.Header)
	require.Len(t, tx.Message.AccountKeys, 5)

	require.NoError(t, prepared.Action.Validate())
	assert.Equal(t, ramp.FundingActionSolanaTransaction, prepared.Action.Type)
	assert.Equal(t, ramp.SubmissionBackendFinalize, prepared.Action.SubmissionMode)
	assert.Equal(t, ramp.ContentTypeSolanaTransaction, prepared.Action.Payload.ContentType)
	assert.Equal(t, encodeTx(t, tx), prepared.Action.Payload.UnsignedTransaction, "the payload is what BlindPay sent")
	assert.Equal(t, prepareNow.Add(30*time.Second), prepared.Action.ExpiresAt, "the blockhash window, the quote living longer")
	assert.Equal(t, messageFingerprint(t, tx), prepared.MessageFingerprint)

	var state map[string]any
	require.NoError(t, json.Unmarshal(prepared.CorrelationState, &state))
	assert.Equal(t, map[string]any{
		"quote_id":          quoteID,
		"owner_address":     prepareOwner.PublicKey().String(),
		"mint":              sandboxMint,
		"amount_base_units": float64(10_120_000),
		"decimals":          float64(6),
		"delegate":          sandboxDelegate,
		"token_program":     solana.TokenProgramID.String(),
	}, state)
}

func TestPrepareOfframp_ActionExpiresWithAQuoteDyingFirst(t *testing.T) {
	adapter, _ := prepareServer(t, stubAnswer{200, prepareAnswer(t, validDelegation().build(t))}, nil)
	req := prepareRequest()
	req.QuoteExpiresAt = prepareNow.Add(10 * time.Second)

	prepared, err := adapter.PrepareOfframp(context.Background(), payinRef, req)
	require.NoError(t, err)
	assert.Equal(t, req.QuoteExpiresAt, prepared.Action.ExpiresAt)
}

func TestPrepareOfframp_ProductionUsesTheTokensOwnMint(t *testing.T) {
	adapter, calls := prepareServer(t, stubAnswer{200, prepareAnswer(t, validDelegation().build(t))}, func(c *blindpay.Config) {
		c.Mode = models.ProviderModeProduction
		c.Mints = map[string]blindpay.MintConfig{"USDT": {Address: sandboxMint, Decimals: 6}}
	})
	req := prepareRequest()

	_, err := adapter.PrepareOfframp(context.Background(), payinRef, req)
	require.ErrorIs(t, err, ramp.ErrProviderNotImplemented, "USDC has no mint of its own here")
	assert.Empty(t, calls())

	req.Token = "usdt"
	_, err = adapter.PrepareOfframp(context.Background(), payinRef, req)
	require.NoError(t, err)
}

// A transaction that is anything but the quote's delegation is refused as the
// provider's failure, and an operator is told why, without an address or an
// amount in the log.
func TestPrepareOfframp_RefusesATransactionThatIsNotTheDelegation(t *testing.T) {
	transfer := system.NewTransferInstruction(1, prepareOwner.PublicKey(), otherSigner.PublicKey()).Build()
	cases := map[string]delegationTx{
		"an extra instruction":   func() delegationTx { d := validDelegation(); d.extra = []solana.Instruction{transfer}; return d }(),
		"the Token-2022 program": func() delegationTx { d := validDelegation(); d.program = solana.Token2022ProgramID; return d }(),
		"another amount":         func() delegationTx { d := validDelegation(); d.amount = 10_120_001; return d }(),
		"other decimals":         func() delegationTx { d := validDelegation(); d.decimals = 9; return d }(),
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			logger := captureLog(t)
			expectAttention(t, logger)
			adapter, _ := prepareServer(t, stubAnswer{200, prepareAnswer(t, spec.build(t))}, nil)

			prepared, err := adapter.PrepareOfframp(context.Background(), payinRef, prepareRequest())
			require.ErrorIs(t, err, ramp.ErrProviderFailed)
			assert.Nil(t, prepared)
			assertNoSecretsIn(t, err.Error())
			logger.AssertExpectations(t)
		})
	}
}

func TestPrepareOfframp_RefusesAnAnswerThatIsNotATransaction(t *testing.T) {
	cases := map[string]string{
		"success false":  `{"success":false,"transaction":"` + encodeTx(t, validDelegation().build(t)) + `"}`,
		"no transaction": `{"success":true}`,
		"not base64":     `{"success":true,"transaction":"not base64!"}`,
		"a signed one":   `{"success":true,"transaction":"` + signed(t, validDelegation().build(t), prepareOwner) + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			logger := captureLog(t)
			expectAttention(t, logger)
			adapter, _ := prepareServer(t, stubAnswer{200, body}, nil)

			_, err := adapter.PrepareOfframp(context.Background(), payinRef, prepareRequest())
			require.ErrorIs(t, err, ramp.ErrProviderFailed)
			logger.AssertExpectations(t)
		})
	}
}

func expectAttention(t *testing.T, logger interface {
	On(string, ...any) *testifymock.Call
}) {
	t.Helper()
	logger.On("Errorf", "ramp.attention: blindpay prepare for quote %s refused: %s", quoteID,
		testifymock.MatchedBy(func(reason string) bool {
			for _, secret := range []string{prepareOwner.PublicKey().String(), sandboxMint, sandboxDelegate, "10120000", "10.12"} {
				if strings.Contains(reason, secret) {
					return false
				}
			}
			return reason != ""
		})).Return().Once()
}

func assertNoSecretsIn(t *testing.T, text string) {
	t.Helper()
	for _, secret := range []string{prepareOwner.PublicKey().String(), sandboxMint, sandboxDelegate, "10120000", "10.12"} {
		assert.NotContains(t, text, secret)
	}
}

func TestPrepareOfframp_UpstreamRejectionKeepsItsSentinel(t *testing.T) {
	adapter, _ := prepareServer(t, stubAnswer{400, `{"success":false,"code":"QUOTES_EXPIRED","message":"quote_expired"}`}, nil)

	_, err := adapter.PrepareOfframp(context.Background(), payinRef, prepareRequest())
	require.ErrorIs(t, err, ramp.ErrEstimateExpired)
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
}

func TestPrepareOfframp_RefusesBeforeAnyRequest(t *testing.T) {
	cases := map[string]struct {
		edit func(*ramp.OfframpPrepareRequest)
		cfg  func(*blindpay.Config)
		want error
	}{
		"an owner that is not an address": {edit: func(r *ramp.OfframpPrepareRequest) { r.OwnerAddress = "not-an-address" }, want: ramp.ErrInvalidWallet},
		"an owner of the wrong length":    {edit: func(r *ramp.OfframpPrepareRequest) { r.OwnerAddress = "1111" }, want: ramp.ErrInvalidWallet},
		"no owner":                        {edit: func(r *ramp.OfframpPrepareRequest) { r.OwnerAddress = "" }, want: ramp.ErrInvalidWallet},
		"an expired quote":                {edit: func(r *ramp.OfframpPrepareRequest) { r.QuoteExpiresAt = prepareNow }, want: ramp.ErrEstimateExpired},
		"a quote without an expiry":       {edit: func(r *ramp.OfframpPrepareRequest) { r.QuoteExpiresAt = time.Time{} }, want: ramp.ErrEstimateExpired},
		"more places than the decimals":   {edit: func(r *ramp.OfframpPrepareRequest) { r.SourceAmount = "10.1234567" }, want: ramp.ErrProviderFailed},
		"an amount that is not a number":  {edit: func(r *ramp.OfframpPrepareRequest) { r.SourceAmount = "-10.12" }, want: ramp.ErrProviderFailed},
		"another chain":                   {edit: func(r *ramp.OfframpPrepareRequest) { r.Chain = "ethereum" }, want: ramp.ErrNoProviderAvailable},
		"no mint":                         {cfg: func(c *blindpay.Config) { c.Mints = nil }, want: ramp.ErrProviderNotImplemented},
		"a mint that is not an address": {cfg: func(c *blindpay.Config) {
			c.Mints = map[string]blindpay.MintConfig{"USDC": {Address: "x", Decimals: 6}}
		}, want: ramp.ErrProviderNotImplemented},
		"no delegate": {cfg: func(c *blindpay.Config) { c.Delegate = "" }, want: ramp.ErrProviderNotImplemented},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, calls := prepareServer(t, stubAnswer{200, prepareAnswer(t, validDelegation().build(t))}, c.cfg)
			req := prepareRequest()
			if c.edit != nil {
				c.edit(&req)
			}
			_, err := adapter.PrepareOfframp(context.Background(), payinRef, req)
			require.ErrorIs(t, err, c.want)
			assert.Empty(t, calls(), "nothing is sent")
		})
	}
}

// signed answers tx signed by key, as base64.
func signed(t *testing.T, tx *solana.Transaction, key solana.PrivateKey) string {
	t.Helper()
	copied := *tx
	copied.Signatures = nil
	message, err := copied.Message.MarshalBinary()
	require.NoError(t, err)
	signature, err := key.Sign(message)
	require.NoError(t, err)
	copied.Signatures = []solana.Signature{signature}
	return encodeTx(t, &copied)
}

func preparedFor(t *testing.T, tx *solana.Transaction) *ramp.PreparedOfframpFunding {
	t.Helper()
	adapter, _ := prepareServer(t, stubAnswer{200, prepareAnswer(t, tx)}, nil)
	prepared, err := adapter.PrepareOfframp(context.Background(), payinRef, prepareRequest())
	require.NoError(t, err)
	return prepared
}

func TestValidateSignedOfframpAction(t *testing.T) {
	tx := validDelegation().build(t)
	prepared := preparedFor(t, tx)
	adapter := blindpay.New(nil, nil, nil)
	validate := func(signedTx string, state []byte) error {
		return adapter.ValidateSignedOfframpAction(context.Background(), payinRef, ramp.SignedOfframpActionValidationRequest{
			SignedTransaction: signedTx, MessageFingerprint: prepared.MessageFingerprint, CorrelationState: state,
		})
	}

	t.Run("signed by the owner", func(t *testing.T) {
		assert.NoError(t, validate(signed(t, tx, prepareOwner), prepared.CorrelationState))
	})
	t.Run("signed by someone else", func(t *testing.T) {
		err := validate(signed(t, tx, otherSigner), prepared.CorrelationState)
		require.ErrorIs(t, err, ramp.ErrInvalidSignedActionPayload)
		assertNoSecretsIn(t, err.Error())
		assert.NotContains(t, err.Error(), otherSigner.PublicKey().String())
	})
	t.Run("left unsigned", func(t *testing.T) {
		require.ErrorIs(t, validate(prepared.Action.Payload.UnsignedTransaction, prepared.CorrelationState), ramp.ErrInvalidSignedActionPayload)
	})
	t.Run("another message", func(t *testing.T) {
		other := validDelegation()
		other.amount = 1
		require.ErrorIs(t, validate(signed(t, other.build(t), prepareOwner), prepared.CorrelationState), ramp.ErrInvalidSignedActionPayload)
	})
	t.Run("a state naming another owner", func(t *testing.T) {
		var state map[string]any
		require.NoError(t, json.Unmarshal(prepared.CorrelationState, &state))
		state["owner_address"] = otherSigner.PublicKey().String()
		foreign, err := json.Marshal(state)
		require.NoError(t, err)
		require.ErrorIs(t, validate(signed(t, tx, prepareOwner), foreign), ramp.ErrInvalidSignedActionPayload)
	})
	// The revalidation holds the signed transaction to the recorded terms
	// again, with the token program from code: a state that names Token-2022
	// (a tampered one) cannot make a Token-2022 delegation acceptable.
	t.Run("a transaction on another token program, named by the state", func(t *testing.T) {
		other := validDelegation()
		other.program = solana.Token2022ProgramID
		otherTx := other.build(t)
		var state map[string]any
		require.NoError(t, json.Unmarshal(prepared.CorrelationState, &state))
		state["token_program"] = solana.Token2022ProgramID.String()
		tampered, err := json.Marshal(state)
		require.NoError(t, err)
		err = adapter.ValidateSignedOfframpAction(context.Background(), payinRef, ramp.SignedOfframpActionValidationRequest{
			SignedTransaction: signed(t, otherTx, prepareOwner), MessageFingerprint: messageFingerprint(t, otherTx), CorrelationState: tampered,
		})
		require.ErrorIs(t, err, ramp.ErrInvalidSignedActionPayload)
		assertNoSecretsIn(t, err.Error())
	})
	for name, edit := range map[string]func(map[string]any){
		"another amount":   func(s map[string]any) { s["amount_base_units"] = float64(1) },
		"another delegate": func(s map[string]any) { s["delegate"] = otherSigner.PublicKey().String() },
		"another mint":     func(s map[string]any) { s["mint"] = otherSigner.PublicKey().String() },
		"other decimals":   func(s map[string]any) { s["decimals"] = float64(9) },
	} {
		t.Run("a state recording "+name, func(t *testing.T) {
			var state map[string]any
			require.NoError(t, json.Unmarshal(prepared.CorrelationState, &state))
			edit(state)
			tampered, err := json.Marshal(state)
			require.NoError(t, err)
			require.ErrorIs(t, validate(signed(t, tx, prepareOwner), tampered), ramp.ErrInvalidSignedActionPayload)
		})
	}
	t.Run("a state whose mint is not an address is retryable", func(t *testing.T) {
		var state map[string]any
		require.NoError(t, json.Unmarshal(prepared.CorrelationState, &state))
		state["mint"] = "not-an-address"
		broken, err := json.Marshal(state)
		require.NoError(t, err)
		err = validate(signed(t, tx, prepareOwner), broken)
		require.Error(t, err)
		assert.NotErrorIs(t, err, ramp.ErrInvalidSignedActionPayload)
	})
	for name, state := range map[string][]byte{
		"a corrupted state":     []byte("{not json"),
		"an empty state":        nil,
		"a state without owner": []byte(`{"quote_id":"` + quoteID + `"}`),
	} {
		t.Run(name+" is retryable", func(t *testing.T) {
			err := validate(signed(t, tx, prepareOwner), state)
			require.Error(t, err)
			assert.NotErrorIs(t, err, ramp.ErrInvalidSignedActionPayload)
		})
	}
}
