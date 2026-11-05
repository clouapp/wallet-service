package blindpay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

var _ ramp.BankAccountRegistrar = (*Adapter)(nil)

// bankAccountRequest is the create body. The field order is fixed, so the
// same registration always encodes to the same bytes, which is what a replay
// under the same Idempotency-Key has to send.
type bankAccountRequest struct {
	Type                  string `json:"type"`
	Name                  string `json:"name"`
	RecipientRelationship string `json:"recipient_relationship"`
	BeneficiaryName       string `json:"beneficiary_name"`
	RoutingNumber         string `json:"routing_number"`
	AccountNumber         string `json:"account_number"`
	AccountClass          string `json:"account_class"`
	AccountType           string `json:"account_type,omitempty"`
	AddressLine1          string `json:"address_line_1"`
	AddressLine2          string `json:"address_line_2,omitempty"`
	City                  string `json:"city"`
	StateProvinceRegion   string `json:"state_province_region"`
	PostalCode            string `json:"postal_code"`
	Country               string `json:"country"`
	BusinessIndustry      string `json:"business_industry,omitempty"`
}

// bankAccountResponse is what the echo check reads. The full numbers it
// decodes are compared by their last four digits and dropped.
type bankAccountResponse struct {
	ID            string  `json:"id"`
	Type          string  `json:"type"`
	Name          string  `json:"name"`
	Status        *string `json:"status"`
	AccountNumber *string `json:"account_number"`
	RoutingNumber *string `json:"routing_number"`
}

func bankAccountType(rail ramp.Rail) (string, error) {
	switch rail {
	case ramp.RailACHPayout:
		return "ach", nil
	case ramp.RailWirePayout:
		return "wire", nil
	}
	return "", fmt.Errorf("blindpay: rail %s has no bank account type: %w", rail, ramp.ErrRequestNotSent)
}

func encodeBankAccount(reg ramp.BeneficiaryAccountRegistration) ([]byte, error) {
	kind, err := bankAccountType(reg.Rail)
	if err != nil {
		return nil, err
	}
	req := bankAccountRequest{
		Type: kind, Name: reg.Name, RecipientRelationship: reg.Relationship, BeneficiaryName: reg.BeneficiaryName,
		RoutingNumber: reg.RoutingNumber, AccountNumber: reg.AccountNumber, AccountClass: reg.AccountClass,
		AddressLine1: reg.AddressLine1, AddressLine2: reg.AddressLine2, City: reg.City,
		StateProvinceRegion: reg.StateCode, PostalCode: reg.PostalCode, Country: "US",
	}
	if kind == "ach" {
		req.AccountType = reg.AccountType
	}
	if reg.AccountClass == "business" {
		req.BusinessIndustry = reg.BusinessIndustry
	}
	return json.Marshal(req)
}

func last4(s *string) string {
	if s == nil {
		return ""
	}
	v := strings.TrimSpace(*s)
	if len(v) <= 4 {
		return v
	}
	return v[len(v)-4:]
}

func bankAccountStatus(s *string) ramp.BankAccountStatus {
	if s == nil {
		return ramp.BankAccountVerifying
	}
	switch ramp.BankAccountStatus(*s) {
	case ramp.BankAccountApproved, ramp.BankAccountRejected, ramp.BankAccountDeprecated, ramp.BankAccountVerifying:
		return ramp.BankAccountStatus(*s)
	}
	return ramp.BankAccountVerifying
}

func bankAccountResult(r bankAccountResponse) (*ramp.BankAccountResult, error) {
	if !strings.HasPrefix(r.ID, "ba_") {
		return nil, fmt.Errorf("blindpay: bank account echo: id has no ba_ prefix: %w", ramp.ErrBankAccountEchoMismatch)
	}
	return &ramp.BankAccountResult{
		ExternalAccountID: r.ID, Status: bankAccountStatus(r.Status),
		AccountLast4: last4(r.AccountNumber), RoutingLast4: last4(r.RoutingNumber),
	}, nil
}

func (a *Adapter) RegisterBankAccount(ctx context.Context, ref ramp.CustomerRef, reg ramp.BeneficiaryAccountRegistration, idempotencyKey string) (*ramp.BankAccountResult, error) {
	if ref.ExternalID == "" {
		return nil, fmt.Errorf("blindpay: bank account without a customer: %w", ramp.ErrIntegrationRequired)
	}
	body, err := encodeBankAccount(reg)
	if err != nil {
		return nil, err
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	var created bankAccountResponse
	if err := client.Do(ctx, Request{
		Method: http.MethodPost, Path: "customers/" + ref.ExternalID + "/bank-accounts",
		RawBody: body, IdempotencyKey: idempotencyKey,
	}, &created); err != nil {
		if errors.Is(err, errUndecodable) {
			// A 200 created the account: an answer that cannot be read is an
			// echo that does not match, not a refusal.
			return nil, fmt.Errorf("blindpay: bank account echo: undecodable answer: %w", ramp.ErrBankAccountEchoMismatch)
		}
		return nil, err
	}
	res, err := bankAccountResult(created)
	if err != nil {
		return nil, err
	}
	kind, _ := bankAccountType(reg.Rail)
	switch {
	case created.Name != reg.Name:
		return nil, fmt.Errorf("blindpay: bank account echo: name differs: %w", ramp.ErrBankAccountEchoMismatch)
	case created.Type != kind:
		return nil, fmt.Errorf("blindpay: bank account echo: type %q, sent %q: %w", created.Type, kind, ramp.ErrBankAccountEchoMismatch)
	case res.AccountLast4 != last4(&reg.AccountNumber) || res.RoutingLast4 != last4(&reg.RoutingNumber):
		return nil, fmt.Errorf("blindpay: bank account echo: numbers differ: %w", ramp.ErrBankAccountEchoMismatch)
	}
	return res, nil
}

func (a *Adapter) GetBankAccount(ctx context.Context, ref ramp.CustomerRef, externalID string) (*ramp.BankAccountResult, error) {
	if ref.ExternalID == "" {
		return nil, fmt.Errorf("blindpay: bank account without a customer: %w", ramp.ErrIntegrationRequired)
	}
	if !strings.HasPrefix(externalID, "ba_") {
		return nil, fmt.Errorf("blindpay: bank account id %q: %w", externalID, ramp.ErrRequestNotSent)
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	var got bankAccountResponse
	if err := client.Do(ctx, Request{Method: http.MethodGet, Path: "customers/" + ref.ExternalID + "/bank-accounts/" + externalID}, &got); err != nil {
		return nil, err
	}
	return bankAccountResult(got)
}

func (a *Adapter) FindBankAccountByName(ctx context.Context, ref ramp.CustomerRef, name string) (*ramp.BankAccountResult, error) {
	if ref.ExternalID == "" {
		return nil, fmt.Errorf("blindpay: bank account without a customer: %w", ramp.ErrIntegrationRequired)
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	var list []bankAccountResponse
	if err := client.Do(ctx, Request{
		Method: http.MethodGet, Path: "customers/" + ref.ExternalID + "/bank-accounts", Query: url.Values{"name": {name}},
	}, &list); err != nil {
		return nil, err
	}
	var match []bankAccountResponse
	for _, item := range list {
		// A deprecated account is gone: it is never the one to link.
		if item.Name == name && bankAccountStatus(item.Status) != ramp.BankAccountDeprecated {
			match = append(match, item)
		}
	}
	switch len(match) {
	case 0:
		return nil, nil
	case 1:
		return bankAccountResult(match[0])
	}
	return nil, fmt.Errorf("blindpay: %d bank accounts named %s: %w", len(match), name, ramp.ErrBankAccountNameAmbiguous)
}
