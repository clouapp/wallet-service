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
)

func TestChainThresholdCatalogDeclaresTheSweepPair(t *testing.T) {
	t.Parallel()

	catalog := chainsvc.ChainThresholdCatalog()
	require.Equal(t, policies.PermSweepView, catalog.ViewPermission)
	require.Equal(t, policies.PermSweepUpdate, catalog.UpdatePermission)
	require.NotEqual(t, policies.PermSettingsUpdate, catalog.UpdatePermission)
	require.NotEqual(t, policies.PermSettingsView, catalog.ViewPermission)
	require.NotEqual(t, policies.PermChainsView, catalog.ViewPermission)
	require.NotEqual(t, policies.PermChainsUpdate, catalog.UpdatePermission)
	require.Equal(t, policies.PermChainsView, catalog.ChainViewPermission)
	require.Equal(t, policies.PermChainsUpdate, catalog.ChainUpdatePermission)
	require.NotEqual(t, policies.PermSettingsUpdate, catalog.ChainUpdatePermission)
	require.NotEqual(t, policies.PermSweepUpdate, catalog.ChainUpdatePermission)
	require.False(t, catalog.ReturnsRPCURL)
	require.Equal(t, []string{
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

func TestUpdateThresholds_UnknownChainIsNotFoundBeforeTheAdminCheck(t *testing.T) {
	t.Parallel()

	store := &thresholdStore{}
	admins := &thresholdAdmins{err: errors.New("admin lookup must not run")}
	service := chainsvc.NewThresholds(store, admins, &thresholdActivity{})

	_, err := service.Update(context.Background(), uuid.New(), "missing", thresholdObject(t, `{"dust_threshold_usd":"-1"}`))
	require.ErrorIs(t, err, chainsvc.ErrNotFound)
	require.Nil(t, store.write)
}

func TestUpdateThresholds_NonAdminLeavesTheRowUnchanged(t *testing.T) {
	t.Parallel()

	store := &thresholdStore{chain: ethChain("5000000000000000", "500000000000000", "1")}
	service := chainsvc.NewThresholds(store, &thresholdAdmins{}, &thresholdActivity{})

	_, err := service.Update(context.Background(), uuid.New(), "eth", map[string]json.RawMessage{
		"gas_readiness_threshold_raw": json.RawMessage(`"-1"`),
	})
	require.ErrorIs(t, err, chainsvc.ErrPlatformForbidden)
	require.Nil(t, store.write)
}

func TestUpdateThresholds_NegativeAmountAndConfirmationAreNotStored(t *testing.T) {
	t.Parallel()

	store := &thresholdStore{chain: ethChain("5000000000000000", "500000000000000", "1")}
	activity := &thresholdActivity{}
	service := chainsvc.NewThresholds(store, &thresholdAdmins{allow: true}, activity)
	actor := uuid.New()

	_, err := service.Update(context.Background(), actor, "eth", map[string]json.RawMessage{
		"dust_threshold_usd":          json.RawMessage(`"-0.01"`),
		"required_confirmations":      json.RawMessage(`-3`),
		"gas_readiness_threshold_raw": json.RawMessage(`"42"`),
	})
	var invalid *chainsvc.ValidationError
	require.ErrorAs(t, err, &invalid)
	require.Equal(t, []string{"must not be negative"}, invalid.Fields["dust_threshold_usd"])
	require.Equal(t, []string{"must not be negative"}, invalid.Fields["required_confirmations"])
	require.Nil(t, store.write)
	require.Empty(t, activity.rows)
	require.Equal(t, "5000000000000000", *store.chain.GasReadinessThresholdRaw)
}

func TestUpdateThresholds_OneFieldLeavesTheOthersAndAuditsTheName(t *testing.T) {
	t.Parallel()

	store := &thresholdStore{chain: ethChain("5000000000000000", "500000000000000", "1")}
	activity := &thresholdActivity{}
	actor := uuid.New()
	service := chainsvc.NewThresholds(store, &thresholdAdmins{allow: true}, activity)

	view, err := service.Update(context.Background(), actor, "eth", map[string]json.RawMessage{
		"gas_readiness_threshold_raw": json.RawMessage(`"77"`),
	})
	require.NoError(t, err)
	require.Equal(t, "77", view.GasReadinessThresholdRaw)
	require.Equal(t, "500000000000000", view.DustThresholdNativeRaw)
	require.Equal(t, "1", view.DustThresholdUSD)
	require.NotNil(t, store.write)
	require.NotNil(t, store.write.GasReadinessThresholdRaw)
	require.Nil(t, store.write.DustThresholdNativeRaw)
	require.Nil(t, store.write.DustThresholdUSD)
	require.Equal(t, "77", store.chain.GasReadinessThreshold().String())

	require.Len(t, activity.rows, 1)
	require.Nil(t, activity.rows[0].AccountID)
	require.Equal(t, actor, activity.rows[0].ActorUserID)
	require.Equal(t, activitylog.ActionChainsUpdated, activity.rows[0].Action)
	require.Equal(t, "eth", activity.rows[0].TargetID)
	encoded, err := activity.rows[0].Metadata.Encode()
	require.NoError(t, err)
	require.Contains(t, encoded, `"key":"eth"`)
	require.Contains(t, encoded, `"gas_readiness_threshold_raw"`)
	require.NotContains(t, encoded, "77")
	require.NotContains(t, encoded, "platform.secret_viewed")
}

func TestUpdateThresholds_EmptyGasIsTheBitcoinSentinelOnly(t *testing.T) {
	t.Parallel()

	btc := btcChain("", "10000", "0")
	store := &thresholdStore{chain: btc}
	service := chainsvc.NewThresholds(store, &thresholdAdmins{allow: true}, &thresholdActivity{})

	view, err := service.Update(context.Background(), uuid.New(), "btc", map[string]json.RawMessage{
		"gas_readiness_threshold_raw": json.RawMessage(`""`),
	})
	require.NoError(t, err)
	require.Empty(t, view.GasReadinessThresholdRaw)
	require.Nil(t, store.chain.GasReadinessThreshold())
	require.Equal(t, "10000", *store.chain.DustThresholdNativeRaw)

	eth := &thresholdStore{chain: ethChain("5", "1", "1")}
	service = chainsvc.NewThresholds(eth, &thresholdAdmins{allow: true}, &thresholdActivity{})
	_, err = service.Update(context.Background(), uuid.New(), "eth", map[string]json.RawMessage{
		"gas_readiness_threshold_raw": json.RawMessage(`""`),
	})
	var invalid *chainsvc.ValidationError
	require.ErrorAs(t, err, &invalid)
	require.Equal(t, []string{"must not be empty"}, invalid.Fields["gas_readiness_threshold_raw"])
	require.Nil(t, eth.write)
	require.Equal(t, "5", eth.chain.GasReadinessThreshold().String())
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
