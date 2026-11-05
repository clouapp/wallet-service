//go:build smoke

// This file is a live smoke of the business KYB against api.sandbox.bridge.xyz,
// through the adapter's own SubmitApplication and SubmitDocuments. It is
// behind the `smoke` build tag and skips without a key, so it never runs in CI
// and never runs by accident.
//
//	BRIDGE_API_KEY=<sandbox key> go test -tags smoke ./app/adapters/ramp/bridge/ \
//	    -run TestBusinessKybSmoke -v -timeout 15m
//
// Each case creates a company, sends its documents and the photographed ID of
// each partner, and waits for the sandbox to approve it (about 60 s), asking
// simulate_kyc_approval if it does not. The data is synthetic: random valid
// CNPJ, CPF and EIN numbers, example.com e-mails.
package bridge_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/bridge"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func TestBusinessKybSmoke(t *testing.T) {
	apiKey := os.Getenv("BRIDGE_API_KEY")
	if apiKey == "" {
		t.Skip("BRIDGE_API_KEY not set")
	}
	client := bridge.NewClient(bridge.Config{BaseURL: bridge.SandboxBaseURL, APIKey: apiKey, Timeout: 60 * time.Second})
	adapter := bridge.New(func(context.Context) (*bridge.Client, error) { return client, nil }, nil)

	t.Run("brazilian ltda", func(t *testing.T) { smokeBusiness(t, adapter, smokeBrazilianCompany()) })
	t.Run("us llc", func(t *testing.T) { smokeBusiness(t, adapter, smokeUSCompany()) })
}

type smokeCompany struct {
	app       ramp.Application
	documents func(ids map[string]string) []ramp.PersonDocuments
	// endorsements are the ones the company must end up approved for.
	endorsements []string
}

func smokeBusiness(t *testing.T, adapter *bridge.Adapter, company smokeCompany) {
	ctx := context.Background()

	created, err := adapter.SubmitApplication(ctx, ramp.CustomerRef{}, company.app)
	require.NoError(t, err, "create")
	t.Logf("created %s status=%s people=%v", created.ExternalCustomerID, created.Status, created.AssociatedPersonIDs)
	require.Len(t, created.AssociatedPersonIDs, len(company.app.AssociatedPersons), "every person has an id")
	ref := ramp.CustomerRef{ExternalID: created.ExternalCustomerID}

	documents, err := adapter.SubmitDocuments(ctx, ref, ramp.Documents{
		CompanyDocuments: []ramp.CompanyDocument{
			{Purpose: ramp.CompanyDocumentFormation, File: smokeImage(1)},
			{Purpose: ramp.CompanyDocumentProofOfAddress, File: smokeImage(2)},
			{Purpose: ramp.CompanyDocumentSourceOfFunds, File: smokeImage(3)},
		},
		PersonDocuments: company.documents(created.AssociatedPersonIDs),
	})
	require.NoError(t, err, "documents")
	t.Logf("after documents: status=%s requirements=%v", documents.Status, documents.Requirements)

	state := waitForApproval(t, adapter, ref)
	require.Equal(t, models.KycStatusApproved, state.Status)

	// The customer turns active before its endorsements are decided (sandbox,
	// 18/10/2026: pix_onramp still incomplete at that moment, approved a
	// minute later), so the rails are read until they settle.
	state = waitForEndorsements(t, adapter, ref, company.endorsements)
	for _, endorsement := range state.Endorsements {
		t.Logf("  endorsement %s: %s", endorsement.Name, endorsement.Status)
	}
	for _, name := range company.endorsements {
		require.Equal(t, models.EndorsementStatusApproved, endorsementStatus(state, name), name)
	}
}

// waitForEndorsements polls for up to three minutes until every one of names
// is approved.
func waitForEndorsements(t *testing.T, adapter *bridge.Adapter, ref ramp.CustomerRef, names []string) *ramp.KycState {
	t.Helper()
	start := time.Now()
	for {
		state, err := adapter.GetKyc(context.Background(), ref)
		require.NoError(t, err, "poll endorsements")
		pending := 0
		for _, name := range names {
			if endorsementStatus(state, name) != models.EndorsementStatusApproved {
				pending++
			}
		}
		elapsed := time.Since(start).Round(time.Second)
		t.Logf("+%s after active: %d of %v not approved yet", elapsed, pending, names)
		if pending == 0 || elapsed > 3*time.Minute {
			return state
		}
		time.Sleep(10 * time.Second)
	}
}

func endorsementStatus(state *ramp.KycState, name string) string {
	for _, endorsement := range state.Endorsements {
		if endorsement.Name == name {
			return endorsement.Status
		}
	}
	return ""
}

// waitForApproval polls the customer for up to three minutes, asking the
// sandbox to approve it once 90 s have passed without a verdict.
func waitForApproval(t *testing.T, adapter *bridge.Adapter, ref ramp.CustomerRef) *ramp.KycState {
	t.Helper()
	ctx := context.Background()
	start := time.Now()
	simulated := false
	for {
		state, err := adapter.GetKyc(ctx, ref)
		require.NoError(t, err, "poll")
		elapsed := time.Since(start).Round(time.Second)
		t.Logf("+%s status=%s", elapsed, state.Status)
		if state.Status == models.KycStatusApproved || state.Status == models.KycStatusRejected {
			return state
		}
		if !simulated && elapsed > 90*time.Second {
			require.NoError(t, adapter.SimulateKycApproval(ctx, ref), "simulate approval")
			t.Logf("+%s asked simulate_kyc_approval", elapsed)
			simulated = true
		}
		if elapsed > 3*time.Minute {
			return state
		}
		time.Sleep(10 * time.Second)
	}
}

func smokeBrazilianCompany() smokeCompany {
	n := rand.IntN(1_000_000)
	address := ramp.Address{StreetLine1: "Avenida Paulista 1000", City: "Sao Paulo", Subdivision: "SP", PostalCode: "01310100", Country: "BRA"}
	person := func(ref, first string, roles []string, share float64, representative bool) ramp.AssociatedPerson {
		return ramp.AssociatedPerson{
			LocalRef: ref, Representative: representative, Roles: roles, OwnershipPercent: share, Title: "Socio-administrador",
			Profile: ramp.Profile{
				FirstName: first, LastName: "Sintetico", Email: fmt.Sprintf("kybleaf+%s%d@example.com", ref, n),
				Phone: smokePhone("+55119"), BirthDate: "1986-04-12",
				TaxID: smokeCPF(), TaxIDType: models.TaxIDTypeCPF, TaxIDCountry: "BRA",
				Address: ramp.Address{StreetLine1: "Rua Augusta 500", City: "Sao Paulo", Subdivision: "SP", PostalCode: "01305000", Country: "BRA"},
			},
		}
	}
	persons := []ramp.AssociatedPerson{
		person("ana", "Ana", []string{ramp.AssociatedPersonOwner, ramp.AssociatedPersonControl, ramp.AssociatedPersonSigner}, 60, true),
		person("bruno", "Bruno", []string{ramp.AssociatedPersonOwner}, 40, false),
	}
	app := smokeApplication(n, ramp.TaxIdentity{Type: models.TaxIDTypeCNPJ, Country: "BRA"}, smokeCNPJ(),
		models.CompanyTypeLTDA, address, smokePhone("+55113"), persons)
	return smokeCompany{app: app, endorsements: []string{"base", models.EndorsementPixOnramp, models.EndorsementPixOfframp}, documents: func(ids map[string]string) []ramp.PersonDocuments {
		var out []ramp.PersonDocuments
		for i, p := range persons {
			out = append(out, ramp.PersonDocuments{LocalRef: p.LocalRef, ExternalID: ids[p.LocalRef], Documents: []ramp.IdentifyingDocument{
				{Type: models.TaxIDTypeCPF, IssuingCountry: "BRA", Number: p.Profile.TaxID},
				{Type: "national_id", IssuingCountry: "BRA", ImageFront: smokeImage(10 + 2*i), ImageBack: smokeImage(11 + 2*i)},
			}})
		}
		return out
	}}
}

func smokeUSCompany() smokeCompany {
	n := rand.IntN(1_000_000)
	address := ramp.Address{StreetLine1: "1450 Brickell Ave", City: "Miami", Subdivision: "FL", PostalCode: "33131", Country: "USA"}
	owner := ramp.AssociatedPerson{
		LocalRef: "jane", Representative: true, Title: "Managing Member", OwnershipPercent: 100,
		Roles: []string{ramp.AssociatedPersonOwner, ramp.AssociatedPersonControl, ramp.AssociatedPersonSigner, ramp.AssociatedPersonDirector},
		Profile: ramp.Profile{
			FirstName: "Jane", LastName: "Synthetic", Email: fmt.Sprintf("kybleaf+jane%d@example.com", n),
			Phone: smokePhone("+1305555"), BirthDate: "1984-02-10",
			TaxID: fmt.Sprintf("287-65-%04d", 1000+rand.IntN(8999)), TaxIDType: models.TaxIDTypeSSN, TaxIDCountry: "USA",
			Address: ramp.Address{StreetLine1: "200 Biscayne Blvd Way", City: "Miami", Subdivision: "FL", PostalCode: "33131", Country: "USA"},
		},
	}
	persons := []ramp.AssociatedPerson{owner}
	app := smokeApplication(n, ramp.TaxIdentity{Type: models.TaxIDTypeEIN, Country: "USA"}, fmt.Sprintf("%09d", 100000000+rand.IntN(899999999)),
		models.CompanyTypeLLC, address, smokePhone("+1305555"), persons)
	return smokeCompany{app: app, endorsements: []string{"base", models.EndorsementPixOnramp}, documents: func(ids map[string]string) []ramp.PersonDocuments {
		return []ramp.PersonDocuments{{LocalRef: owner.LocalRef, ExternalID: ids[owner.LocalRef], Documents: []ramp.IdentifyingDocument{
			{Type: models.TaxIDTypeSSN, IssuingCountry: "USA", Number: owner.Profile.TaxID},
			{Type: "passport", IssuingCountry: "USA", ImageFront: smokeImage(20)},
		}}}
	}}
}

func smokeApplication(n int, identity ramp.TaxIdentity, taxID, companyType string, address ramp.Address, phone string, persons []ramp.AssociatedPerson) ramp.Application {
	return ramp.Application{
		Type: models.CustomerTypeBusiness,
		// The sandbox takes any fresh uuid4 as the signed agreement (memory
		// bridge-tos-aceite-no-smoke).
		SignedAgreementID: uuid.NewString(),
		Business: &ramp.BusinessProfile{
			TaxIdentity: identity, TaxID: taxID,
			LegalName: fmt.Sprintf("Kybleaf %d Tecnologia", n), TradeName: fmt.Sprintf("Kybleaf %d", n),
			CompanyType: companyType, IncorporationDate: "2019-03-01",
			Email: fmt.Sprintf("kybleaf+biz%d@example.com", n), Phone: phone,
			Website: fmt.Sprintf("https://kybleaf%d.example.com", n), Industry: "software_technology",
			Description:       "Synthetic sandbox company for custom software development",
			RegisteredAddress: address, AccountPurpose: "receive_from_customers", SourceOfFunds: "sales",
			AnnualRevenueBand: "100000_999999", MonthlyVolumeBand: "10000_49999",
			OwnershipAttestedAt: time.Now().UTC().Format(time.RFC3339),
		},
		AssociatedPersons: persons,
		IdempotencyKey:    "kyb-leaf-smoke:" + uuid.NewString(),
	}
}

func smokePhone(prefix string) string {
	digits := 13 - len(prefix) + 1
	if prefix[1] == '1' {
		digits = 12 - len(prefix)
	}
	out := prefix
	for range digits {
		out += fmt.Sprint(rand.IntN(10))
	}
	return out
}

// smokeCPF and smokeCNPJ are random numbers with valid check digits.
func smokeCPF() string {
	digits := make([]int, 9, 11)
	for i := range digits {
		digits[i] = rand.IntN(10)
	}
	for _, length := range []int{9, 10} {
		sum := 0
		for i := 0; i < length; i++ {
			sum += digits[i] * (length + 1 - i)
		}
		digits = append(digits, (sum*10%11)%10)
	}
	return joinDigits(digits)
}

func smokeCNPJ() string {
	digits := make([]int, 12, 14)
	for i := range 8 {
		digits[i] = rand.IntN(10)
	}
	digits[11] = 1 // branch 0001
	for _, weights := range [][]int{{5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}, {6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}} {
		sum := 0
		for i, w := range weights {
			sum += digits[i] * w
		}
		check := 0
		if rem := sum % 11; rem >= 2 {
			check = 11 - rem
		}
		digits = append(digits, check)
	}
	return joinDigits(digits)
}

func joinDigits(digits []int) string {
	var out bytes.Buffer
	for _, d := range digits {
		fmt.Fprint(&out, d)
	}
	return out.String()
}

// smokeImage is a 256 x 256 PNG (Bridge wants 200 px a side at least),
// different per seed, as a data URI.
func smokeImage(seed int) string {
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := range 256 {
		for x := range 256 {
			img.Set(x, y, color.RGBA{R: uint8(x + seed*37), G: uint8(y + seed*11), B: uint8((x ^ y) + seed), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// TestBusinessKybResubmissionSmoke runs the paths the first smoke does not:
// a second company under the same company e-mail, and a resubmission that
// keeps one partner, adds one and removes one — the normal path after
// action_required. It reports which partners Bridge then still wants a
// document from, which says whether updating a partner keeps the document
// already sent.
func TestBusinessKybResubmissionSmoke(t *testing.T) {
	apiKey := os.Getenv("BRIDGE_API_KEY")
	if apiKey == "" {
		t.Skip("BRIDGE_API_KEY not set")
	}
	client := bridge.NewClient(bridge.Config{BaseURL: bridge.SandboxBaseURL, APIKey: apiKey, Timeout: 60 * time.Second})
	adapter := bridge.New(func(context.Context) (*bridge.Client, error) { return client, nil }, nil)
	ctx := context.Background()

	company := smokeBrazilianCompany()
	created, err := adapter.SubmitApplication(ctx, ramp.CustomerRef{}, company.app)
	require.NoError(t, err, "create")
	ref := ramp.CustomerRef{ExternalID: created.ExternalCustomerID}
	_, err = adapter.SubmitDocuments(ctx, ref, ramp.Documents{
		CompanyDocuments: []ramp.CompanyDocument{
			{Purpose: ramp.CompanyDocumentFormation, File: smokeImage(1)},
			{Purpose: ramp.CompanyDocumentProofOfAddress, File: smokeImage(2)},
			{Purpose: ramp.CompanyDocumentSourceOfFunds, File: smokeImage(3)},
		},
		PersonDocuments: company.documents(created.AssociatedPersonIDs),
	})
	require.NoError(t, err, "documents")
	t.Logf("created %s people=%v", created.ExternalCustomerID, created.AssociatedPersonIDs)

	// The same company e-mail on another company.
	twin := smokeBrazilianCompany()
	twin.app.Business.Email = company.app.Business.Email
	duplicate, err := adapter.SubmitApplication(ctx, ramp.CustomerRef{}, twin.app)
	if err != nil {
		t.Logf("duplicate company e-mail: error %v (email_in_use: %v)", err, errors.Is(err, ramp.ErrEmailInUse))
	} else {
		t.Logf("duplicate company e-mail: accepted as %s", duplicate.ExternalCustomerID)
	}

	// Keep Ana, drop Bruno, add Carla.
	app := company.app
	ana := app.AssociatedPersons[0]
	ana.ExternalID = created.AssociatedPersonIDs[ana.LocalRef]
	carla := app.AssociatedPersons[1]
	carla.LocalRef, carla.Profile.FirstName, carla.Profile.TaxID = "carla", "Carla", smokeCPF()
	carla.Profile.Email = "kybleaf+carla" + ana.Profile.Email[len("kybleaf+ana"):]
	app.AssociatedPersons = []ramp.AssociatedPerson{ana, carla}
	app.Business.Description = "Synthetic sandbox company, resubmitted"

	state, err := adapter.SubmitApplication(ctx, ref, app)
	require.NoError(t, err, "resubmit")
	t.Logf("resubmitted: status=%s people=%v", state.Status, state.AssociatedPersonIDs)
	require.Equal(t, ana.ExternalID, state.AssociatedPersonIDs["ana"], "the kept partner keeps her id")
	require.NotEmpty(t, state.AssociatedPersonIDs["carla"])

	// The requirements right after the changes still name the removed
	// partner and not the new one (sandbox, 18/10/2026): they are read again
	// until they settle.
	bruno, carlaID := created.AssociatedPersonIDs["bruno"], state.AssociatedPersonIDs["carla"]
	var listed, owed []string
	for start := time.Now(); time.Since(start) < 90*time.Second; time.Sleep(10 * time.Second) {
		settled, err := adapter.GetKyc(ctx, ref)
		require.NoError(t, err, "poll")
		listed, owed = smokePeople(t, settled.Raw)
		t.Logf("+%s Bridge lists %v; wants a document from %v", time.Since(start).Round(time.Second), listed, owed)
		if !slices.Contains(owed, bruno) && slices.Contains(owed, carlaID) {
			break
		}
	}
	require.NotContains(t, listed, bruno, "the dropped partner is removed")
	require.Contains(t, owed, carlaID, "the new partner owes her document")
	require.NotContains(t, owed, ana.ExternalID, "updating a partner keeps the document she sent")
}

// smokePeople reads, from a customer snapshot, the partners Bridge lists and
// the ones its endorsements still want a document from.
func smokePeople(t *testing.T, raw json.RawMessage) (listed, owed []string) {
	t.Helper()
	var customer struct {
		AssociatedPersons []struct {
			ID string `json:"id"`
		} `json:"associated_persons"`
		Endorsements []struct {
			Requirements struct {
				Missing json.RawMessage `json:"missing"`
			} `json:"requirements"`
		} `json:"endorsements"`
	}
	require.NoError(t, json.Unmarshal(raw, &customer))
	for _, person := range customer.AssociatedPersons {
		listed = append(listed, person.ID)
	}
	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if id, ok := v["object_id"].(string); ok && !slices.Contains(owed, id) {
				owed = append(owed, id)
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	for _, endorsement := range customer.Endorsements {
		var missing any
		if json.Unmarshal(endorsement.Requirements.Missing, &missing) == nil {
			walk(missing)
		}
	}
	return listed, owed
}
