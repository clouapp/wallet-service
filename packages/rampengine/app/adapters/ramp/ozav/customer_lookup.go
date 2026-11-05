package ozav

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/support/digits"
)

// customerListResponse is Ozav's cursor page of customers.
type customerListResponse struct {
	Data       []customerResponse `json:"data"`
	HasMore    bool               `json:"has_more"`
	NextCursor string             `json:"next_cursor"`
}

// Ozav holds one customer per tax identity, and its 409
// CUSTOMER_ALREADY_EXISTS does not name the one it collided with. GET
// /customers has no filter, only newest-first cursor pages, so finding it is a
// bounded walk: the largest page Ozav allows, and a budget past which the
// original refusal stands. The customer this recovers was just created, so it
// is on the first page in practice.
const (
	customerLookupPageSize = 100
	maxCustomerLookupPages = 10
)

// errCustomerNotAdoptable is a failed create whose customer this user cannot
// take over: there is none that could have been ours, it is registered to
// another e-mail, or the walk cannot reach it. It is a final answer, where a
// lookup that failed on the way is not.
var errCustomerNotAdoptable = errors.New("ozav: existing customer cannot be adopted")

// findCustomerID finds the customer holding taxID, only when it is registered
// under email: the CPF is typed by the applicant, the e-mail is the account's.
// Nothing is logged with a value.
func (a *Adapter) findCustomerID(ctx context.Context, taxID, email string) (string, error) {
	// Compared on digits: the same CPF punctuated is the same person.
	wanted := digits.Only(taxID)
	if wanted == "" || email == "" {
		return "", fmt.Errorf("ozav: customer lookup needs a tax id and an e-mail: %w", errCustomerNotAdoptable)
	}

	cursor := ""
	for page := 0; page < maxCustomerLookupPages; page++ {
		query := url.Values{}
		query.Set("limit", fmt.Sprint(customerLookupPageSize))
		if cursor != "" {
			query.Set("starting_after", cursor)
		}

		var resp customerListResponse
		req := Request{Method: http.MethodGet, Path: "/customers", Query: query}
		if err := a.do(ctx, req, &resp); err != nil {
			return "", err
		}
		for _, customer := range resp.Data {
			if customer.ID == "" || customer.Individual == nil || customer.Individual.TaxID == nil {
				continue
			}
			if digits.Only(customer.Individual.TaxID.Value) != wanted {
				continue
			}
			if !strings.EqualFold(customer.Email, email) {
				return "", fmt.Errorf("ozav: customer for tax id belongs to another e-mail: %w", errCustomerNotAdoptable)
			}
			return customer.ID, nil
		}
		if !resp.HasMore || resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	return "", fmt.Errorf("ozav: no customer for tax id: %w", errCustomerNotAdoptable)
}

// mayHaveCreatedCustomer reports whether a failed create could still have made
// a customer at Ozav. "The call failed" is not "nothing happened": on 02/09
// Ozav created the record and answered 500, leaving an orphan. So:
//
//   - the request never left (ErrRequestNotSent): nothing was created;
//   - Ozav classified a refusal: decided before anything was written;
//   - anything else (an api_error such as INTERNAL_ERROR, a timeout, a reset
//     after the bytes left): only asking can settle it.
func mayHaveCreatedCustomer(err error) bool {
	if errors.Is(err, ramp.ErrRequestNotSent) {
		return false
	}
	if errors.Is(err, ramp.ErrRetryableUpstream) {
		return true
	}
	var upstream *UpstreamError
	return !errors.As(err, &upstream)
}

// adoptExistingCustomer turns a failed create into an update of the customer
// Ozav already holds for this tax id. When it cannot, the error says why:
// errCustomerNotAdoptable when no customer of this user's can be there (see
// createRefusal), anything else when the answer is not in yet. The caller
// keeps the original error either way.
func (a *Adapter) adoptExistingCustomer(
	ctx context.Context, app ramp.Application, cause error,
) (*ramp.KycState, error) {
	if !isDuplicateCustomerError(cause) && !mayHaveCreatedCustomer(cause) {
		return nil, errCustomerNotAdoptable
	}
	taxID := taxIDFrom(app.IdentifyingInformation)
	if taxID == nil || taxID.Value == "" {
		return nil, errCustomerNotAdoptable
	}
	externalID, err := a.findCustomerID(ctx, taxID.Value, strings.TrimSpace(app.Profile.Email))
	if err != nil {
		return nil, err
	}

	// Rebuilt as an update, without the create-only fields.
	body, err := customerRequestFrom(app, false)
	if err != nil {
		return nil, err
	}
	// No Idempotency-Key: the create's key on another body is
	// IDEMPOTENCY_KEY_REUSED, and a PATCH needs no replay protection.
	return a.customerState(ctx, Request{
		Method: http.MethodPatch, Path: customerPath(externalID), Body: body,
	})
}

// isDuplicateCustomerError matches on the code: a 409 also carries
// WALLET_ALREADY_MANAGED and other conflicts. The exception is the create's
// own 409 with a body that is not Ozav's error envelope (classify names it
// http_409): a conflict on POST /customers can only be about a customer Ozav
// already holds, and the lookup below is fenced by the CPF and the e-mail, so
// trying it costs a read. Seen on the staging on 23/10/2026.
func isDuplicateCustomerError(err error) bool {
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		return false
	}
	if upstream.Code == codeCustomerAlreadyExists {
		return true
	}
	return upstream.Method == http.MethodPost && upstream.Path == "/customers" && upstream.Code == codeUnreadableConflict
}

const (
	codeCustomerAlreadyExists = "CUSTOMER_ALREADY_EXISTS"
	// codeUnreadableConflict is the code classify gives a 409 whose body
	// carries no error.code.
	codeUnreadableConflict = "http_409"
)
