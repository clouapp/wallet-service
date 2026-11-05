package ozav

// customerRequest is the body of POST /customers and PATCH /customers/{id}.
// Type, CustodyModel and TermsSessionID are create-only (see
// customerRequestFrom); omitempty is what keeps them off the PATCH.
type customerRequest struct {
	Type           string             `json:"type,omitempty"`
	Email          string             `json:"email,omitempty"`
	CustodyModel   string             `json:"custody_model,omitempty"`
	TermsSessionID string             `json:"terms_session_id,omitempty"`
	Individual     *individualPayload `json:"individual,omitempty"`
}

// custodyManaged is the only custody model XIP uses: Ozav signs from a managed
// wallet, so a self-custody customer gets no balance for an off-ramp to debit
// (422 WALLET_NOT_MANAGED), and only Ozav support can migrate it. It does not
// put anyone between the user and their keys: Ozav attributes a deposit by
// destination address, and the user funds it from the app's own wallet.
const custodyManaged = "managed"

// individualPayload is the profile Ozav wants for an individual. Each
// risk-profile field is sent only when the client supplied it; a gap reaches
// the user through requirements.missing_fields.
type individualPayload struct {
	LegalName                string          `json:"legal_name,omitempty"`
	MotherName               string          `json:"mother_name,omitempty"`
	DateOfBirth              string          `json:"date_of_birth,omitempty"`
	Phone                    *phonePayload   `json:"phone,omitempty"`
	TaxID                    *taxIDPayload   `json:"tax_id,omitempty"`
	ResidentialAddress       *addressPayload `json:"residential_address,omitempty"`
	Nationality              string          `json:"nationality,omitempty"`
	Occupation               string          `json:"occupation,omitempty"`
	SourceOfFunds            string          `json:"source_of_funds,omitempty"`
	AccountPurpose           string          `json:"account_purpose,omitempty"`
	MonthlyIncomeUSD         *float64        `json:"monthly_income_usd,omitempty"`
	ExpectedMonthlyVolumeUSD *float64        `json:"expected_monthly_volume_usd,omitempty"`
	// IsPEP is a pointer so a declared false is sent and an undeclared one is
	// not: omitted is Ozav's default, false is the user's statement.
	IsPEP *bool `json:"is_pep,omitempty"`
}

type phonePayload struct {
	CountryCode string `json:"country_code,omitempty"`
	Number      string `json:"number,omitempty"`
}

type taxIDPayload struct {
	Country string `json:"country,omitempty"`
	Type    string `json:"type,omitempty"`
	Value   string `json:"value,omitempty"`
}

// addressPayload has no line2: Ozav's schema carries line1 only.
type addressPayload struct {
	Line1        string `json:"line1,omitempty"`
	Number       string `json:"number,omitempty"`
	Neighborhood string `json:"neighborhood,omitempty"`
	City         string `json:"city,omitempty"`
	State        string `json:"state,omitempty"`
	PostalCode   string `json:"postal_code,omitempty"`
	Country      string `json:"country,omitempty"` // alpha-2, see countryAlpha2
}

// customerResponse is what create, update and get all answer.
type customerResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// Email proves, on an admin relink, that a hand-typed customer id belongs
	// to the user it is written on.
	Email        string               `json:"email"`
	Verification verificationResponse `json:"verification"`
	Requirements customerRequirements `json:"requirements"`
	// Decision is Ozav's compliance verdict and why; absent on records older
	// than it (2026-09-08).
	Decision *customerDecision `json:"decision"`
	// Individual is read back for the Travel Rule originator (delivery.go).
	Individual *individualPayload `json:"individual"`
}

// customerDecision is Ozav's verdict on a customer. ReasonCode is a closed
// vocabulary (DecisionReasonCode); the English label beside it adds nothing
// the code does not say, so it is not decoded.
type customerDecision struct {
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code"`
}

// verificationResponse is Ozav's attestation of identity verification. An
// absent Mode is an unknown posture, never a pass (customerKycStatus).
type verificationResponse struct {
	Mode string `json:"mode"`
}

type customerRequirements struct {
	Due           []string `json:"due"`
	MissingFields []string `json:"missing_fields"`
	// Owner says whose move it is: `integrator`, `ozav` or `none`. It is not
	// what customerStatus reads — a customer under human review keeps `ozav`
	// even with fields missing — but it is decoded for the admin's raw view.
	Owner string `json:"owner"`
	// DocumentsDue is Ozav's compliance asking for files by name, each with
	// its own state.
	DocumentsDue []documentDue `json:"documents_due"`
}

// documentDue is one file Ozav's compliance asked for. DocType is the
// still_required vocabulary; it is absent when the requirement is not an API
// document, and Key names it then.
type documentDue struct {
	Key     string `json:"key"`
	DocType string `json:"doc_type"`
	Status  string `json:"status"`
	// ReasonCode is why the file is asked for: document_invalid when the one
	// sent was not the document requested, capacity_unproven for a limit
	// increase.
	ReasonCode string `json:"reason_code"`
}

// documentInvalid is the reason_code of a file that does not count as the
// document it was sent as.
const documentInvalid = "document_invalid"

// owed reports whether the user still has to send it: `due`, or `rejected`
// (a new file is owed). `submitted` stopped the clock and `accepted` is done,
// except a submitted file marked document_invalid while Ozav waits on the
// integrator: the customer of 29/10/2026 sat there, labelled "envie CNH,
// passaporte ou RG", with nothing else owed, so only a new file moves it.
func (d documentDue) owed(awaitingIntegrator bool) bool {
	switch d.Status {
	case "due", "rejected":
		return true
	case "submitted":
		return awaitingIntegrator && d.ReasonCode == documentInvalid
	}
	return false
}

// code is the requirement code the file is named by.
func (d documentDue) code() string {
	if d.DocType != "" {
		return d.DocType
	}
	return d.Key
}

// owedDocuments lists the codes of the files still owed, in Ozav's order.
func (r customerRequirements) owedDocuments() []string {
	var out []string
	awaitingIntegrator := r.Owner == "integrator"
	for _, doc := range r.DocumentsDue {
		if doc.owed(awaitingIntegrator) {
			out = append(out, doc.code())
		}
	}
	return out
}

// waitingOnData reports whether Ozav cannot move until the user sends
// something: a field never supplied, or a file its compliance asked for.
func (r customerRequirements) waitingOnData() bool {
	return len(r.MissingFields) > 0 || len(r.owedDocuments()) > 0
}

// customerDocumentsPage is GET /customers/{id}/documents. Only StillRequired
// (identity_document, proof_of_address, …) is decoded: the rest lists a
// person's identity documents, PII carried for nothing.
type customerDocumentsPage struct {
	StillRequired []string `json:"still_required"`
}
