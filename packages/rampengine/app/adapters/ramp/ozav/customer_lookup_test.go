package ozav

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// exchange is one request the fake Ozav saw, in order. The duplicate-recovery
// path is three calls deep, so unlike newTestAdapter's single capture these
// tests assert on the whole sequence.
type exchange struct {
	method string
	path   string
	query  string
	body   map[string]any
	header http.Header
}

// scriptedAdapter serves each request from handler and records it. handler
// gets the request index so a test can answer the POST, the list and the
// PATCH differently.
func scriptedAdapter(t *testing.T, handler func(i int, ex exchange, w http.ResponseWriter)) (*Adapter, *[]exchange) {
	t.Helper()
	var seen []exchange
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ex := exchange{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, header: r.Header}
		_ = json.NewDecoder(r.Body).Decode(&ex.body)
		seen = append(seen, ex)
		w.Header().Set("Content-Type", "application/json")
		handler(len(seen)-1, ex, w)
	}))
	t.Cleanup(server.Close)

	cfg := Config{APIKey: "ozav_sk_test_abc123", Timeout: 2 * time.Second}
	client := NewClient(Config{APIKey: cfg.APIKey, Timeout: cfg.Timeout, BaseURL: server.URL})
	return &Adapter{clients: func(context.Context) (*Client, error) { return client, nil }}, &seen
}

// The failure that stranded a real customer on 02/09: Ozav materialised the
// record and then answered 500 INTERNAL_ERROR, so XIP never learned the id.
func writeInternalError(w http.ResponseWriter) {
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`{"error":{"type":"api_error","code":"INTERNAL_ERROR",` +
		`"message":"Something went wrong.","request_id":"req_internal"}}`))
}

// A refusal Ozav decided before writing anything.
func writeValidationRefusal(w http.ResponseWriter) {
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"INVALID_REQUEST",` +
		`"message":"Bad field.","param":"individual.legal_name","request_id":"req_bad"}}`))
}

func writeDuplicateConflict(w http.ResponseWriter) {
	w.WriteHeader(http.StatusConflict)
	_, _ = w.Write([]byte(`{"error":{"type":"conflict_error","code":"CUSTOMER_ALREADY_EXISTS",` +
		`"message":"A customer with this tax identity already exists.","request_id":"req_dup"}}`))
}

func writeCustomerPage(w http.ResponseWriter, nextCursor string, customers ...map[string]any) {
	page := map[string]any{"data": customers, "has_more": nextCursor != "", "next_cursor": nextCursor}
	_ = json.NewEncoder(w).Encode(page)
}

// customerWithTaxID is registered under the applicant's e-mail, as the orphan
// of their own failed create is.
func customerWithTaxID(id, taxID string) map[string]any {
	return map[string]any{
		"id": id, "object": "customer", "type": "individual", "status": "pending",
		"email": fullApplication().Profile.Email,
		"individual": map[string]any{
			"legal_name": "Someone Else",
			"tax_id":     map[string]any{"type": "cpf", "value": taxID, "country": "BR"},
		},
	}
}

func writeApprovedCustomer(w http.ResponseWriter, id string) {
	_, _ = w.Write([]byte(`{"id":"` + id + `","status":"approved","verification":{"mode":"api"},` +
		`"requirements":{"due":[],"missing_fields":[]}}`))
}

// The CPF fullApplication carries, which is what the lookup has to match on.
func applicationTaxID(t *testing.T) string {
	t.Helper()
	payload := taxIDFrom(fullApplication().IdentifyingInformation)
	require.NotNil(t, payload, "fullApplication must carry a CPF for these tests to mean anything")
	return payload.Value
}

// A customer Ozav already holds for this CPF must be adopted, not abandoned.
// Ozav enforces one customer per tax identity and its 409 does not name the
// existing id, so without this recovery a user whose customer was created by
// a submission that failed before we persisted the link is unreachable
// forever: every retry answers the same 409.
func TestSubmitApplicationAdoptsTheExistingCustomerOnDuplicate(t *testing.T) {
	cpf := applicationTaxID(t)
	adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		switch i {
		case 0:
			writeDuplicateConflict(w)
		case 1:
			writeCustomerPage(w, "", customerWithTaxID("ozav_cus_existing", cpf))
		default:
			writeApprovedCustomer(w, "ozav_cus_existing")
		}
	})

	state, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.NoError(t, err)
	require.Equal(t, "ozav_cus_existing", state.ExternalCustomerID)

	require.Len(t, *seen, 3)
	require.Equal(t, http.MethodPost, (*seen)[0].method)
	require.Equal(t, "/customers", (*seen)[0].path)
	require.Equal(t, http.MethodGet, (*seen)[1].method)
	require.Equal(t, "/customers", (*seen)[1].path)
	require.Equal(t, http.MethodPatch, (*seen)[2].method)
	require.Equal(t, "/customers/ozav_cus_existing", (*seen)[2].path)
}

// The adoption is an update, and an update must not name a custody model:
// Ozav cannot convert one, so a PATCH carrying it asks for something the API
// does not do (see customerRequestFrom).
func TestDuplicateAdoptionSendsNoCustodyModel(t *testing.T) {
	cpf := applicationTaxID(t)
	adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		switch i {
		case 0:
			writeDuplicateConflict(w)
		case 1:
			writeCustomerPage(w, "", customerWithTaxID("ozav_cus_existing", cpf))
		default:
			writeApprovedCustomer(w, "ozav_cus_existing")
		}
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.NoError(t, err)

	patch := (*seen)[2]
	require.NotContains(t, patch.body, "custody_model")
	require.Contains(t, (*seen)[0].body, "custody_model", "the create still names it")
}

// The idempotency key belongs to the create that failed. Replaying it on the
// adoption is a different operation with a different body, which is the
// definition of the reuse Ozav answers with IDEMPOTENCY_KEY_REUSED.
func TestDuplicateAdoptionDropsTheCreateIdempotencyKey(t *testing.T) {
	cpf := applicationTaxID(t)
	adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		switch i {
		case 0:
			writeDuplicateConflict(w)
		case 1:
			writeCustomerPage(w, "", customerWithTaxID("ozav_cus_existing", cpf))
		default:
			writeApprovedCustomer(w, "ozav_cus_existing")
		}
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.NoError(t, err)

	require.NotEmpty(t, (*seen)[0].header.Get("Idempotency-Key"))
	require.Empty(t, (*seen)[2].header.Get("Idempotency-Key"))
}

// The customer Ozav is holding may not be on the first page.
func TestDuplicateAdoptionWalksPagesUntilTheTaxIDMatches(t *testing.T) {
	cpf := applicationTaxID(t)
	adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		switch i {
		case 0:
			writeDuplicateConflict(w)
		case 1:
			writeCustomerPage(w, "cursor_page_2", customerWithTaxID("ozav_cus_other", "11122233344"))
		case 2:
			require.Contains(t, ex.query, "starting_after=cursor_page_2")
			writeCustomerPage(w, "", customerWithTaxID("ozav_cus_existing", cpf))
		default:
			writeApprovedCustomer(w, "ozav_cus_existing")
		}
	})

	state, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.NoError(t, err)
	require.Equal(t, "ozav_cus_existing", state.ExternalCustomerID)
	require.Equal(t, http.MethodPatch, (*seen)[3].method)
}

// A duplicate we cannot locate must surface the provider's own rejection, not
// a lookup failure: the 409 is the fact, and burying it behind "no customer
// for tax id" would send whoever reads the log after the wrong thing.
func TestDuplicateWithNoMatchReturnsTheConflict(t *testing.T) {
	adapter, _ := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		if i == 0 {
			writeDuplicateConflict(w)
			return
		}
		writeCustomerPage(w, "", customerWithTaxID("ozav_cus_other", "11122233344"))
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.Error(t, err)
	require.Contains(t, err.Error(), "CUSTOMER_ALREADY_EXISTS")
}

// A customer holding the CPF under another e-mail is another account's.
func TestDuplicateAdoptionLeavesAnotherAccountsCustomerAlone(t *testing.T) {
	cpf := applicationTaxID(t)
	adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		switch i {
		case 0:
			writeDuplicateConflict(w)
		case 1:
			victim := customerWithTaxID("ozav_cus_victim", cpf)
			victim["email"] = "victim@example.com"
			writeCustomerPage(w, "", victim)
		default:
			writeApprovedCustomer(w, "ozav_cus_victim")
		}
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.Error(t, err)
	require.Contains(t, err.Error(), "CUSTOMER_ALREADY_EXISTS")
	require.Len(t, *seen, 2, "the create and the lookup; no PATCH of the victim's customer")
}

// An application with no CPF has nothing to match on, so the walk must not
// even start — it could only ever adopt the wrong person's customer.
func TestDuplicateWithoutTaxIDDoesNotSearch(t *testing.T) {
	app := fullApplication()
	app.IdentifyingInformation = nil
	adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		writeDuplicateConflict(w)
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, app)
	require.Error(t, err)
	require.Len(t, *seen, 1, "only the create; no lookup")
}

// A resubmission already knows its customer. The 409 cannot mean "someone
// else holds this CPF" there, so the recovery has no business running.
func TestDuplicateRecoveryDoesNotRunOnAnUpdate(t *testing.T) {
	adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		writeDuplicateConflict(w)
	})

	_, err := adapter.SubmitApplication(context.Background(),
		ramp.CustomerRef{ExternalID: "ozav_cus_known"}, fullApplication())
	require.Error(t, err)
	require.Len(t, *seen, 1)
	require.Equal(t, http.MethodPatch, (*seen)[0].method)
}

// The walk is bounded. Ozav offers no filter on GET /customers, so an
// organization with more customers than the bound must fail with the
// provider's rejection rather than page through the whole book.
func TestDuplicateAdoptionStopsAfterThePageBudget(t *testing.T) {
	adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		if i == 0 {
			writeDuplicateConflict(w)
			return
		}
		writeCustomerPage(w, "cursor_next", customerWithTaxID("ozav_cus_other", "11122233344"))
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.Error(t, err)
	require.Contains(t, err.Error(), "CUSTOMER_ALREADY_EXISTS")
	require.Equal(t, maxCustomerLookupPages+1, len(*seen))
}

// The tax id is compared on digits: Ozav echoes back what it stored, and a
// CPF written with punctuation on one side is the same identity.
func TestDuplicateAdoptionMatchesTaxIDIgnoringPunctuation(t *testing.T) {
	cpf := applicationTaxID(t)
	formatted := cpf[:3] + "." + cpf[3:6] + "." + cpf[6:9] + "-" + cpf[9:]
	require.NotEqual(t, cpf, formatted)

	adapter, _ := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		switch i {
		case 0:
			writeDuplicateConflict(w)
		case 1:
			writeCustomerPage(w, "", customerWithTaxID("ozav_cus_existing", formatted))
		default:
			writeApprovedCustomer(w, "ozav_cus_existing")
		}
	})

	state, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.NoError(t, err)
	require.Equal(t, "ozav_cus_existing", state.ExternalCustomerID)
}

// The lookup asks for the largest page Ozav allows: the fewer round trips a
// recovery costs, the less a duplicate delays the user's submission.
func TestCustomerLookupAsksForFullPages(t *testing.T) {
	cpf := applicationTaxID(t)
	adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		switch i {
		case 0:
			writeDuplicateConflict(w)
		case 1:
			writeCustomerPage(w, "", customerWithTaxID("ozav_cus_existing", cpf))
		default:
			writeApprovedCustomer(w, "ozav_cus_existing")
		}
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.NoError(t, err)
	require.True(t, strings.Contains((*seen)[1].query, "limit=100"), "query was %q", (*seen)[1].query)
}

// A create that failed with Ozav's own internal error may still have created
// the customer — and on 02/09 it did: the record existed, "In review", while
// XIP held an integration row with no id at all. Nothing recovered it. The
// webhook could not: its processor resolves the user by external_customer_id,
// which is the very thing missing. And the 409 recovery never ran, because
// Ozav never answered 409 — it answered 500.
//
// So the lookup cannot be keyed on the conflict. It has to run whenever the
// request left and the answer does not prove the create was refused.
func TestInternalErrorAfterCreateAdoptsTheCustomerOzavKept(t *testing.T) {
	cpf := applicationTaxID(t)
	adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		switch i {
		case 0:
			writeInternalError(w)
		case 1:
			writeCustomerPage(w, "", customerWithTaxID("ozav_cus_orphan", cpf))
		default:
			writeApprovedCustomer(w, "ozav_cus_orphan")
		}
	})

	state, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.NoError(t, err)
	require.Equal(t, "ozav_cus_orphan", state.ExternalCustomerID)

	require.Len(t, *seen, 3)
	require.Equal(t, http.MethodPost, (*seen)[0].method)
	require.Equal(t, http.MethodGet, (*seen)[1].method)
	require.Equal(t, http.MethodPatch, (*seen)[2].method)
	require.Equal(t, "/customers/ozav_cus_orphan", (*seen)[2].path)
}

// Nothing was created: the walk finds nobody and the caller keeps Ozav's own
// error. Reporting success on a customer that does not exist would be worse
// than the failure it replaced.
func TestInternalErrorWithNoCustomerKeepsTheOriginalFailure(t *testing.T) {
	adapter, _ := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		if i == 0 {
			writeInternalError(w)
			return
		}
		writeCustomerPage(w, "")
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.Error(t, err)
	require.Contains(t, err.Error(), "INTERNAL_ERROR")
	require.ErrorIs(t, err, ramp.ErrRetryableUpstream)
}

// A refusal Ozav classified is a decision it made before writing: the field
// was bad, so no record exists to adopt. Searching would cost a walk of the
// whole customer book on every mistyped form.
func TestRefusedCreateDoesNotSearch(t *testing.T) {
	adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		writeValidationRefusal(w)
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.Error(t, err)
	require.Len(t, *seen, 1, "only the create; a refused body created nothing to find")
}

// A request that never left cannot have created anything. The distinction is
// the httptrace WroteRequest hook in the client, and this is the one caller
// that has to act on it.
func TestCreateThatNeverLeftIsNotSearchedFor(t *testing.T) {
	require.False(t, mayHaveCreatedCustomer(
		fmt.Errorf("ozav: POST /customers: %w: dial refused", ramp.ErrRequestNotSent)))
	require.True(t, mayHaveCreatedCustomer(
		fmt.Errorf("ozav: POST /customers: context deadline exceeded")))
}

// 23/10 customer 74 on the staging: the create answered 409 with a body that
// is not Ozav's error envelope (no error.code), the lookup was never tried and
// the user's Ozav row was left without a customer. A conflict on the create
// can only be about a customer Ozav already holds, so it is adopted just like
// the coded one.
func TestSubmitApplicationAdoptsTheExistingCustomerWhenTheConflictCarriesNoCode(t *testing.T) {
	cpf := applicationTaxID(t)
	for _, body := range []string{`{}`, `Conflict`, ``} {
		t.Run("body "+body, func(t *testing.T) {
			adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
				switch i {
				case 0:
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(body))
				case 1:
					writeCustomerPage(w, "", customerWithTaxID("ozav_cus_existing", cpf))
				default:
					writeApprovedCustomer(w, "ozav_cus_existing")
				}
			})

			state, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())

			require.NoError(t, err)
			require.Equal(t, "ozav_cus_existing", state.ExternalCustomerID)
			require.Len(t, *seen, 3)
			require.Equal(t, http.MethodPatch, (*seen)[2].method)
		})
	}
}

// The adoption stays fenced by the e-mail: a customer for this CPF registered
// to someone else is not taken over just because the refusal had no code, and
// without a code it is not a final refusal either.
func TestACodelessConflictOverAnotherAccountsCustomerIsNotAdoptedNorFinal(t *testing.T) {
	cpf := applicationTaxID(t)
	adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		switch i {
		case 0:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{}`))
		default:
			other := customerWithTaxID("ozav_cus_other", cpf)
			other["email"] = "someone.else@example.com"
			writeCustomerPage(w, "", other)
		}
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())

	require.Error(t, err)
	var refusal *ramp.KycRefusal
	require.NotErrorAs(t, err, &refusal, "an unreadable conflict is not a verdict")
	require.Len(t, *seen, 2, "the lookup ran, and nothing was patched")
	for _, ex := range *seen {
		require.NotEqual(t, http.MethodPatch, ex.method)
	}
}
