package blindpay_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
)

func clientFactory(client *blindpay.Client, err error) blindpay.ClientFactory {
	return func(context.Context) (*blindpay.Client, error) { return client, err }
}

func TestCheck_ReachesTheInstanceWithTheKey(t *testing.T) {
	client, rec := newTestClient(t, 200, `[]`)
	result, err := blindpay.New(clientFactory(client, nil), nil, nil).Check(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "in_000000000001", result.InstanceID)
	assert.Equal(t, http.MethodGet, rec.method)
	assert.Equal(t, "/v1/instances/in_000000000001/customers", rec.path)
	assert.Equal(t, "limit=10", rec.rawQuery)
}

func TestCheck_NamesABadKeyOrInstance(t *testing.T) {
	client, _ := newTestClient(t, 401, envelope("AUTH_UNAUTHORIZED", ""))
	_, err := blindpay.New(clientFactory(client, nil), nil, nil).Check(context.Background())
	assert.ErrorIs(t, err, ramp.ErrProviderCredentialsInvalid)

	client, _ = newTestClient(t, 404, envelope("INSTANCES_NOT_FOUND", ""))
	_, err = blindpay.New(clientFactory(client, nil), nil, nil).Check(context.Background())
	assert.ErrorIs(t, err, ramp.ErrProviderCredentialsInvalid)
}

func TestCheck_ReportsAConfigurationGapBeforeAnyRequest(t *testing.T) {
	_, err := blindpay.New(clientFactory(nil, errors.New("instance_id not configured")), nil, nil).Check(context.Background())
	assert.ErrorIs(t, err, ramp.ErrRequestNotSent)

	_, err = blindpay.New(nil, nil, nil).Check(context.Background())
	assert.ErrorIs(t, err, ramp.ErrRequestNotSent)
}

func TestQueryValuesAllowed_RefusesAValueOutsideTheEnum(t *testing.T) {
	const path = "/v1/instances/{instance_id}/customers"
	assert.Error(t, queryValuesAllowed(t, "GET", path, url.Values{"limit": {"1"}}))
	assert.NoError(t, queryValuesAllowed(t, "GET", path, url.Values{"limit": {"10"}}))
	assert.Error(t, queryValuesAllowed(t, "GET", path, url.Values{"nope": {"x"}}))
}
