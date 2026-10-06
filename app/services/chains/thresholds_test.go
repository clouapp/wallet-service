package chains_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/stretchr/testify/assert"
)

func TestChain_Threshold_CatalogDeclaresTheSweepPair(t *testing.T) {
	t.Parallel()

	catalog := chainsvc.ChainThresholdCatalog()
	assert.Equal(t, policies.PermSweepView, catalog.ViewPermission)
	assert.Equal(t, policies.PermSweepUpdate, catalog.UpdatePermission)
	assert.NotEqual(t, policies.PermSettingsUpdate, catalog.UpdatePermission)
	assert.NotEqual(t, policies.PermSettingsView, catalog.ViewPermission)
	assert.NotEqual(t, policies.PermChainsView, catalog.ViewPermission)
	assert.NotEqual(t, policies.PermChainsUpdate, catalog.UpdatePermission)
	assert.Equal(t, policies.PermChainsView, catalog.ChainViewPermission)
	assert.Equal(t, policies.PermChainsUpdate, catalog.ChainUpdatePermission)
	assert.NotEqual(t, policies.PermSettingsUpdate, catalog.ChainUpdatePermission)
	assert.NotEqual(t, policies.PermSweepUpdate, catalog.ChainUpdatePermission)
	assert.False(t, catalog.ReturnsRPCURL)
	assert.Equal(t, []string{
		"gas_readiness_threshold_raw",
		"dust_threshold_native_raw",
		"dust_threshold_usd",
	}, catalog.Fields)
	if policies.PermSettingsUpdate == policies.PermSweepUpdate {
		t.Fatal("holding settings.update does not by itself become sweep.update")
	}
	if policies.PermSettingsUpdate == policies.PermChainsUpdate {
		t.Fatal("holding settings.update does not by itself become chains.update")
	}
}

func TestUpdate_Thresholds_UnknownChainIsNotFoundBeforeTheAdminCheck(t *testing.T) {
	t.Parallel()

	store := &thresholdStore{}
	admins := &thresholdAdmins{err: errors.New("admin lookup must not run")}
	service := chainsvc.NewThresholds(chainsvc.ThresholdDeps{
		Store: store, Admins: admins, Activity: &thresholdActivity{},
	})

	_, err := service.Update(context.Background(), uuid.New(), "missing", thresholdObject(t, `{"dust_threshold_usd":"-1"}`))
	assert.ErrorIs(t, err, chainsvc.ErrNotFound)
	assert.Nil(t, store.write)
}

func TestUpdate_Thresholds_NonAdminLeavesTheRowUnchanged(t *testing.T) {
	t.Parallel()

	store := &thresholdStore{chain: ethChain("5000000000000000", "500000000000000", "1")}
	service := chainsvc.NewThresholds(chainsvc.ThresholdDeps{
		Store: store, Admins: &thresholdAdmins{}, Activity: &thresholdActivity{},
	})

	_, err := service.Update(context.Background(), uuid.New(), "eth", map[string]json.RawMessage{
		"gas_readiness_threshold_raw": json.RawMessage(`"-1"`),
	})
	assert.ErrorIs(t, err, chainsvc.ErrPlatformForbidden)
	assert.Nil(t, store.write)
}

func TestUpdate_Thresholds_NegativeAmountAndConfirmationAreNotStored(t *testing.T) {
	t.Parallel()

	store := &thresholdStore{chain: ethChain("5000000000000000", "500000000000000", "1")}
	activity := &thresholdActivity{}
	service := chainsvc.NewThresholds(chainsvc.ThresholdDeps{
		Store: store, Admins: &thresholdAdmins{allow: true}, Activity: activity,
	})
	actor := uuid.New()

	_, err := service.Update(context.Background(), actor, "eth", map[string]json.RawMessage{
		"dust_threshold_usd":          json.RawMessage(`"-0.01"`),
		"required_confirmations":      json.RawMessage(`-3`),
		"gas_readiness_threshold_raw": json.RawMessage(`"42"`),
	})
	var invalid *chainsvc.ValidationError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, []string{"must not be negative"}, invalid.Fields["dust_threshold_usd"])
	assert.Equal(t, []string{"must not be negative"}, invalid.Fields["required_confirmations"])
	assert.Nil(t, store.write)
	assert.Empty(t, activity.rows)
	assert.Equal(t, "5000000000000000", *store.chain.GasReadinessThresholdRaw)
}

func TestUpdate_Thresholds_OneFieldLeavesTheOthersAndAuditsTheName(t *testing.T) {
	t.Parallel()

	store := &thresholdStore{chain: ethChain("5000000000000000", "500000000000000", "1")}
	activity := &thresholdActivity{}
	actor := uuid.New()
	service := chainsvc.NewThresholds(chainsvc.ThresholdDeps{
		Store: store, Admins: &thresholdAdmins{allow: true}, Activity: activity,
	})

	view, err := service.Update(context.Background(), actor, "eth", map[string]json.RawMessage{
		"gas_readiness_threshold_raw": json.RawMessage(`"77"`),
	})
	require.NoError(t, err)
	assert.Equal(t, "77", view.GasReadinessThresholdRaw)
	assert.Equal(t, "500000000000000", view.DustThresholdNativeRaw)
	assert.Equal(t, "1", view.DustThresholdUSD)
	require.NotNil(t, store.write)
	assert.NotNil(t, store.write.GasReadinessThresholdRaw)
	assert.Nil(t, store.write.DustThresholdNativeRaw)
	assert.Nil(t, store.write.DustThresholdUSD)
	assert.Equal(t, "77", store.chain.GasReadinessThreshold().String())

	require.Len(t, activity.rows, 1)
	assert.Nil(t, activity.rows[0].AccountID)
	assert.Equal(t, actor, activity.rows[0].ActorUserID)
	assert.Equal(t, activitylog.ActionChainsUpdated, activity.rows[0].Action)
	assert.Equal(t, "eth", activity.rows[0].TargetID)
	encoded, err := activity.rows[0].Metadata.Encode()
	require.NoError(t, err)
	assert.Contains(t, encoded, `"key":"eth"`)
	assert.Contains(t, encoded, `"gas_readiness_threshold_raw"`)
	assert.NotContains(t, encoded, "77")
	assert.NotContains(t, encoded, "platform.secret_viewed")
}

func TestUpdate_Thresholds_EmptyGasIsTheBitcoinSentinelOnly(t *testing.T) {
	t.Parallel()

	btc := btcChain("", "10000", "0")
	store := &thresholdStore{chain: btc}
	service := chainsvc.NewThresholds(chainsvc.ThresholdDeps{
		Store: store, Admins: &thresholdAdmins{allow: true}, Activity: &thresholdActivity{},
	})

	view, err := service.Update(context.Background(), uuid.New(), "btc", map[string]json.RawMessage{
		"gas_readiness_threshold_raw": json.RawMessage(`""`),
	})
	require.NoError(t, err)
	assert.Empty(t, view.GasReadinessThresholdRaw)
	assert.Nil(t, store.chain.GasReadinessThreshold())
	assert.Equal(t, "10000", *store.chain.DustThresholdNativeRaw)

	eth := &thresholdStore{chain: ethChain("5", "1", "1")}
	service = chainsvc.NewThresholds(chainsvc.ThresholdDeps{
		Store: eth, Admins: &thresholdAdmins{allow: true}, Activity: &thresholdActivity{},
	})
	_, err = service.Update(context.Background(), uuid.New(), "eth", map[string]json.RawMessage{
		"gas_readiness_threshold_raw": json.RawMessage(`""`),
	})
	var invalid *chainsvc.ValidationError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, []string{"must not be empty"}, invalid.Fields["gas_readiness_threshold_raw"])
	assert.Nil(t, eth.write)
	assert.Equal(t, "5", eth.chain.GasReadinessThreshold().String())
}

type thresholdStore struct {
	chain *models.Chain
	write *models.ChainThresholdWrite
}

func (s *thresholdStore) FindByID(_ context.Context, id string) (*models.Chain, error) {
	if s.chain == nil || s.chain.ID != id {
		return nil, models.ErrRepositoryNotFound
	}
	return s.chain, nil
}

func (s *thresholdStore) UpdateThresholds(_ context.Context, id string, write models.ChainThresholdWrite) error {
	if s.chain == nil || s.chain.ID != id {
		return models.ErrRepositoryNotFound
	}
	copied := write
	s.write = &copied
	if write.GasReadinessThresholdRaw != nil {
		value := *write.GasReadinessThresholdRaw
		s.chain.GasReadinessThresholdRaw = &value
	}
	if write.DustThresholdNativeRaw != nil {
		value := *write.DustThresholdNativeRaw
		s.chain.DustThresholdNativeRaw = &value
	}
	if write.DustThresholdUSD != nil {
		parsed, err := decimal.NewFromString(*write.DustThresholdUSD)
		if err != nil {
			return err
		}
		s.chain.DustThresholdUSD = numeric.NewNullDecimal(parsed)
	}
	return nil
}

type thresholdAdmins struct {
	allow bool
	err   error
}

func (a *thresholdAdmins) Contains(context.Context, uuid.UUID) (bool, error) {
	if a.err != nil {
		return false, a.err
	}
	return a.allow, nil
}

type thresholdActivity struct {
	rows []models.AccountActivity
}

func (a *thresholdActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (a *thresholdActivity) Append(_ context.Context, row models.AccountActivity) error {
	a.rows = append(a.rows, row)
	return nil
}

func ethChain(gas, dust, usd string) *models.Chain {
	return chainRow(models.ChainETH, models.AdapterTypeEVM, gas, dust, usd)
}

func btcChain(gas, dust, usd string) *models.Chain {
	return chainRow(models.ChainBTC, models.AdapterTypeBitcoin, gas, dust, usd)
}

func chainRow(id, adapter, gas, dust, usd string) *models.Chain {
	parsed := decimal.RequireFromString(usd)
	return &models.Chain{
		ID:                       id,
		AdapterType:              adapter,
		GasReadinessThresholdRaw: &gas,
		DustThresholdNativeRaw:   &dust,
		DustThresholdUSD:         numeric.NewNullDecimal(parsed),
		RequiredConfirmations:    12,
	}
}

func thresholdObject(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &body))
	return body
}
