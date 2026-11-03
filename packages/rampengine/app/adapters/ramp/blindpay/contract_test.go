package blindpay_test

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
)

// The pinned BlindPay OpenAPI document. DTOs are checked against it so a field
// BlindPay renames (it already moved receivers to customers) fails CI rather
// than a payout.
const openAPIFixture = "testdata/openapi-2026-10-15.json.gz"

var (
	openAPIOnce sync.Once
	openAPIDoc  map[string]any
	openAPIErr  error
)

func openAPI(t *testing.T) map[string]any {
	t.Helper()
	openAPIOnce.Do(func() {
		file, err := os.Open(openAPIFixture)
		if err != nil {
			openAPIErr = err
			return
		}
		defer file.Close()
		reader, err := gzip.NewReader(file)
		if err != nil {
			openAPIErr = err
			return
		}
		openAPIErr = json.NewDecoder(reader).Decode(&openAPIDoc)
	})
	require.NoError(t, openAPIErr)
	return openAPIDoc
}

// resolve follows a local $ref ("#/components/schemas/X").
func resolve(t *testing.T, doc map[string]any, node any) map[string]any {
	t.Helper()
	object, ok := node.(map[string]any)
	require.True(t, ok, "schema node is not an object")
	ref, ok := object["$ref"].(string)
	if !ok {
		return object
	}
	current := any(doc)
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		current = current.(map[string]any)[part]
	}
	return resolve(t, doc, current)
}

// schemaProperties gathers the properties of a schema, through allOf/oneOf/
// anyOf, which is how BlindPay composes its bodies.
func schemaProperties(t *testing.T, doc map[string]any, schema map[string]any) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	if props, ok := schema["properties"].(map[string]any); ok {
		for name := range props {
			out[name] = true
		}
	}
	for _, key := range []string{"allOf", "oneOf", "anyOf"} {
		variants, _ := schema[key].([]any)
		for _, variant := range variants {
			for name := range schemaProperties(t, doc, resolve(t, doc, variant)) {
				out[name] = true
			}
		}
	}
	return out
}

func jsonFields(t *testing.T, dto any) []string {
	t.Helper()
	typ := reflect.TypeOf(dto)
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	require.Equal(t, reflect.Struct, typ.Kind(), "dto must be a struct")
	var fields []string
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	return fields
}

func operation(t *testing.T, doc map[string]any, method, path string) map[string]any {
	t.Helper()
	paths := doc["paths"].(map[string]any)
	item, ok := paths[path].(map[string]any)
	require.True(t, ok, "the pinned OpenAPI has no path %s", path)
	op, ok := item[strings.ToLower(method)].(map[string]any)
	require.True(t, ok, "the pinned OpenAPI has no %s %s", method, path)
	return op
}

func requireFields(t *testing.T, doc map[string]any, schema map[string]any, dto any, where string) {
	t.Helper()
	declared := schemaProperties(t, doc, schema)
	for _, field := range jsonFields(t, dto) {
		require.True(t, declared[field], "%s: %T.%s is not in BlindPay's schema", where, dto, field)
	}
}

// requireFieldsInSchema checks every json field of dto against the response
// schema BlindPay declares for one endpoint and status.
func requireFieldsInSchema(t *testing.T, method, path string, status int, dto any) {
	t.Helper()
	doc := openAPI(t)
	op := operation(t, doc, method, path)
	response := resolve(t, doc, op["responses"].(map[string]any)[strconv.Itoa(status)])
	content := response["content"].(map[string]any)["application/json"].(map[string]any)
	requireFields(t, doc, resolve(t, doc, content["schema"]), dto, method+" "+path)
}

// requireFieldsInRequestSchema does the same for the request body.
func requireFieldsInRequestSchema(t *testing.T, method, path string, dto any) {
	t.Helper()
	doc := openAPI(t)
	op := operation(t, doc, method, path)
	body := resolve(t, doc, op["requestBody"])
	content := body["content"].(map[string]any)["application/json"].(map[string]any)
	requireFields(t, doc, resolve(t, doc, content["schema"]), dto, method+" "+path+" (request)")
}

// The helper itself is pinned to a known shape, so a broken helper cannot pass
// every later contract test vacuously.
func TestContractHelper_SeesAKnownSchema(t *testing.T) {
	type quoteRequestProbe struct {
		BankAccountID string `json:"bank_account_id"`
		Network       string `json:"network"`
		Token         string `json:"token"`
	}
	requireFieldsInRequestSchema(t, "POST", "/v1/instances/{instance_id}/quotes", quoteRequestProbe{})

	type payoutProbe struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	requireFieldsInSchema(t, "GET", "/v1/instances/{instance_id}/payouts/{id}", 200, payoutProbe{})
}

func TestContractHelper_RefusesAFieldBlindPayDoesNotDeclare(t *testing.T) {
	doc := openAPI(t)
	op := operation(t, doc, "POST", "/v1/instances/{instance_id}/quotes")
	body := resolve(t, doc, op["requestBody"])
	content := body["content"].(map[string]any)["application/json"].(map[string]any)
	declared := schemaProperties(t, doc, resolve(t, doc, content["schema"]))
	require.False(t, declared["receiver_id"], "a retired field must not read as declared")
	require.True(t, declared["bank_account_id"])
}

// requireQueryValuesAllowed fails when a query key is not a declared query
// parameter of the operation, or its value is outside the parameter's enum.
func requireQueryValuesAllowed(t *testing.T, method, path string, query url.Values) {
	t.Helper()
	require.NoError(t, queryValuesAllowed(t, method, path, query))
}

// queryValuesAllowed is the check behind requireQueryValuesAllowed, returning
// the first violation so a test can prove the guard is not vacuous.
func queryValuesAllowed(t *testing.T, method, path string, query url.Values) error {
	t.Helper()
	doc := openAPI(t)
	op := operation(t, doc, method, path)
	params, ok := op["parameters"].([]any)
	require.True(t, ok, "%s %s declares no parameters", method, path)
	for name, values := range query {
		var param map[string]any
		for _, raw := range params {
			candidate := resolve(t, doc, raw)
			if candidate["in"] == "query" && candidate["name"] == name {
				param = candidate
				break
			}
		}
		if param == nil {
			return fmt.Errorf("%s %s declares no query parameter %q", method, path, name)
		}
		schemaNode, ok := param["schema"]
		require.True(t, ok, "query parameter %q has no schema", name)
		schema := resolve(t, doc, schemaNode)
		enum, ok := schema["enum"].([]any)
		if !ok {
			continue
		}
		for _, value := range values {
			found := false
			for _, allowed := range enum {
				if fmt.Sprint(allowed) == value {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%s %s: query %q value %q is not in %v", method, path, name, value, enum)
			}
		}
	}
	return nil
}

// requireWebhookFields checks every json field of dto against the payload
// schema BlindPay declares for one webhook event.
func requireWebhookFields(t *testing.T, event string, dto any) {
	t.Helper()
	doc := openAPI(t)
	webhooks, ok := doc["webhooks"].(map[string]any)
	require.True(t, ok, "the pinned OpenAPI has no webhooks section")
	item, ok := webhooks[event].(map[string]any)
	require.True(t, ok, "the pinned OpenAPI has no webhook %s", event)
	post, ok := item["post"].(map[string]any)
	require.True(t, ok, "webhook %s has no post", event)
	body := resolve(t, doc, post["requestBody"])
	content, ok := body["content"].(map[string]any)["application/json"].(map[string]any)
	require.True(t, ok, "webhook %s has no JSON body", event)
	requireFields(t, doc, resolve(t, doc, content["schema"]), dto, "webhook "+event)
}

func TestContract_PartnerFees(t *testing.T) {
	const path = "/v1/instances/{instance_id}/partner-fees"
	requireFieldsInRequestSchema(t, "POST", path, blindpay.PartnerFeeRequest{})
	requireFieldsInSchema(t, "POST", path, 200, blindpay.PartnerFeeResponse{})

	// The listing answers an array: the DTO is checked against its item.
	doc := openAPI(t)
	op := operation(t, doc, "GET", path)
	response := resolve(t, doc, op["responses"].(map[string]any)["200"])
	content := response["content"].(map[string]any)["application/json"].(map[string]any)
	list := resolve(t, doc, content["schema"])
	require.Equal(t, "array", list["type"])
	requireFields(t, doc, resolve(t, doc, list["items"]), blindpay.PartnerFeeResponse{}, "GET "+path+" (item)")
}

func TestContract_BlockchainWallets(t *testing.T) {
	const path = "/v1/instances/{instance_id}/customers/{customer_id}/blockchain-wallets"
	requireFieldsInRequestSchema(t, "POST", path, blindpay.CreateBlockchainWalletIn{})
	requireFieldsInSchema(t, "POST", path, 200, blindpay.BlockchainWalletOut{})

	doc := openAPI(t)
	op := operation(t, doc, "GET", path)
	response := resolve(t, doc, op["responses"].(map[string]any)["200"])
	content := response["content"].(map[string]any)["application/json"].(map[string]any)
	list := resolve(t, doc, content["schema"])
	require.Equal(t, "array", list["type"])
	requireFields(t, doc, resolve(t, doc, list["items"]), blindpay.BlockchainWalletOut{}, "GET "+path+" (item)")
}

// The payin DTO only reads fields BlindPay's PayinOut declares.
func TestContract_PayinResponse(t *testing.T) {
	requireFieldsInSchema(t, "GET", "/v1/instances/{instance_id}/payins/{id}", 200, blindpay.PayinResponse{})
}

// Every payment step BlindPay declares is classified, and a new one fails here
// before it is silently read as unpaid: only processing and on_hold are before
// the money arrives.
func TestContract_PayinPaymentSteps(t *testing.T) {
	doc := openAPI(t)
	payin := resolve(t, doc, doc["components"].(map[string]any)["schemas"].(map[string]any)["PayinOut"])
	payment := resolve(t, doc, payin["properties"].(map[string]any)["tracking_payment"])
	stepSchema := resolve(t, doc, payment["properties"].(map[string]any)["step"])
	want := map[string]bool{
		"processing": false, "on_hold": false,
		"pending_review": true, "pending_refund_review": true, "completed": true,
	}
	steps := stepSchema["enum"].([]any)
	require.Len(t, steps, len(want))
	for _, s := range steps {
		step := s.(string)
		paid, known := want[step]
		require.True(t, known, "unclassified payment step %q", step)
		require.Equal(t, paid, blindpay.PaymentReceived(step), step)
	}
}

// The payin quote body and answer only name fields BlindPay declares.
func TestContract_PayinQuotes(t *testing.T) {
	const path = "/v1/instances/{instance_id}/payin-quotes"
	requireFieldsInRequestSchema(t, "POST", path, blindpay.CreatePayinQuoteIn{})
	requireFieldsInSchema(t, "POST", path, 200, blindpay.CreatePayinQuoteOut{})
}

// The payin create body and answer only name fields BlindPay declares.
func TestContract_CreatePayin(t *testing.T) {
	const path = "/v1/instances/{instance_id}/payins/evm"
	requireFieldsInRequestSchema(t, "POST", path, blindpay.CreatePayinIn{})
	requireFieldsInSchema(t, "POST", path, 200, blindpay.CreatePayinOut{})
}

func TestContract_BankAccountRequestAndResponse(t *testing.T) {
	requireFieldsInRequestSchema(t, "post", "/v1/instances/{instance_id}/customers/{customer_id}/bank-accounts", blindpay.BankAccountRequest{})
	requireFieldsInSchema(t, "post", "/v1/instances/{instance_id}/customers/{customer_id}/bank-accounts", 200, blindpay.BankAccountResponse{})
	requireFieldsInSchema(t, "get", "/v1/instances/{instance_id}/customers/{customer_id}/bank-accounts/{id}", 200, blindpay.BankAccountResponse{})
	requireQueryValuesAllowed(t, "get", "/v1/instances/{instance_id}/customers/{customer_id}/bank-accounts", url.Values{"name": {"x"}})

	doc := openAPI(t)
	op := operation(t, doc, "get", "/v1/instances/{instance_id}/customers/{customer_id}/bank-accounts")
	response := resolve(t, doc, op["responses"].(map[string]any)["200"])
	content := response["content"].(map[string]any)["application/json"].(map[string]any)
	list := resolve(t, doc, content["schema"])
	require.Equal(t, "array", list["type"])
	requireFields(t, doc, resolve(t, doc, list["items"]), blindpay.BankAccountResponse{}, "get bank-accounts (list item)")
}

func TestContract_Quotes(t *testing.T) {
	const path = "/v1/instances/{instance_id}/quotes"
	requireFieldsInRequestSchema(t, "POST", path, blindpay.QuoteRequest{})
	requireFieldsInSchema(t, "POST", path, 200, blindpay.QuoteResponse{})
}

func TestContract_CustomerLimits(t *testing.T) {
	requireFieldsInSchema(t, "GET", "/v1/instances/{instance_id}/limits/customers/{id}", 200, blindpay.LimitsResponse{})

	doc := openAPI(t)
	op := operation(t, doc, "GET", "/v1/instances/{instance_id}/limits/customers/{id}")
	response := resolve(t, doc, op["responses"].(map[string]any)["200"])
	schema := resolve(t, doc, response["content"].(map[string]any)["application/json"].(map[string]any)["schema"])
	limits := resolve(t, doc, schema["properties"].(map[string]any)["limits"])
	payout := resolve(t, doc, limits["properties"].(map[string]any)["payout"])
	declared := schemaProperties(t, doc, payout)
	require.True(t, declared["daily"], "payout.daily is not in BlindPay's schema")
	require.True(t, declared["monthly"], "payout.monthly is not in BlindPay's schema")
}

func TestContract_PrepareDelegateSolana(t *testing.T) {
	const path = "/v1/instances/{instance_id}/prepare-delegate-solana"
	requireFieldsInRequestSchema(t, "POST", path, blindpay.PrepareDelegateRequest{})
	requireFieldsInSchema(t, "POST", path, 200, blindpay.PrepareDelegateResponse{})
}
