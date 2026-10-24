package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
)

func TestEffective_DepositSignatures_MissingRowLeavesEveryFieldToTheEnvironment(t *testing.T) {
	t.Parallel()

	got, err := newTestService(newMemoryStore()).EffectiveDepositSignatures(context.Background())

	require.NoError(t, err)
	assert.Equal(t, DepositSignatureValues{}, got, "no stored chain list: the environment's stands")
}

func TestEffective_DepositSignatures_StoredRowOverridesFieldByField(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupDepositSignatures, map[string]string{
		keySignatureChains: "sol, tsol", keySignatureInterval: "900", keySignatureConcurrency: "4", keySignatureMaxPages: "20",
	})

	got, err := newTestService(store).EffectiveDepositSignatures(context.Background())

	require.NoError(t, err)
	assert.Equal(t, DepositSignatureValues{Chains: []string{"sol", "tsol"}, ChainsStored: true, IntervalSeconds: 900, Concurrency: 4, MaxPages: 20}, got)
}

func TestEffective_DepositSignatures_AStoredEmptyChainListTurnsTheSyncOff(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupDepositSignatures, map[string]string{keySignatureChains: ""})

	got, err := newTestService(store).EffectiveDepositSignatures(context.Background())

	require.NoError(t, err)
	assert.True(t, got.ChainsStored, "an empty stored list is a decision, not a missing value")
	assert.Empty(t, got.Chains)
}

func TestEffective_DepositSignatures_AnInvalidKeyFallsBackForThatKeyOnly(t *testing.T) {
	t.Parallel()

	for name, stored := range map[string]map[string]string{
		"interval below 15 s":    {keySignatureInterval: "14", keySignatureConcurrency: "3"},
		"concurrency above 16":   {keySignatureConcurrency: "17", keySignatureInterval: "60", keySignatureMaxPages: "5"},
		"pages that are no int":  {keySignatureMaxPages: "lots", keySignatureInterval: "60"},
		"pages above 1000":       {keySignatureMaxPages: "1001", keySignatureConcurrency: "3"},
		"negative concurrency":   {keySignatureConcurrency: "-2", keySignatureMaxPages: "5"},
		"zeros keep the default": {keySignatureInterval: "0", keySignatureConcurrency: "0", keySignatureMaxPages: "0"},
	} {
		t.Run(name, func(t *testing.T) {
			store := newMemoryStore()
			store.PutPlatform(groupDepositSignatures, stored)

			got, err := newTestService(store).EffectiveDepositSignatures(context.Background())

			require.NoError(t, err)
			want := DepositSignatureValues{}
			if stored[keySignatureInterval] == "60" {
				want.IntervalSeconds = 60
			}
			if stored[keySignatureConcurrency] == "3" {
				want.Concurrency = 3
			}
			if stored[keySignatureMaxPages] == "5" {
				want.MaxPages = 5
			}
			assert.Equal(t, want, got)
		})
	}
}

func TestEffective_DepositSignatures_AStoreErrorIsReturned(t *testing.T) {
	t.Parallel()

	_, err := newTestService(platformErrStore{err: errors.New("db down")}).EffectiveDepositSignatures(context.Background())
	require.EqualError(t, err, "db down")
	_, err = newTestService(newMemoryStore()).EffectiveDepositSignatures(nil) //nolint:staticcheck // a nil context is refused
	require.Error(t, err)
}

func TestValidate_Deposit_Signatures(t *testing.T) {
	t.Parallel()

	group, ok := FindGroup(groupDepositSignatures)
	require.True(t, ok)
	require.NotNil(t, group.Validate)
	assert.Equal(t, ScopePlatform, group.Scope)
	assert.Equal(t, sectionScanning, group.SectionName(), "beside deposit_scan on the scanning page")
	require.NoError(t, group.Validate(map[string]string{keySignatureInterval: "15", keySignatureConcurrency: "16", keySignatureMaxPages: "1000"}))
	require.NoError(t, group.Validate(map[string]string{keySignatureInterval: "0", keySignatureConcurrency: "0", keySignatureMaxPages: "0"}),
		"zeros are the environment defaults")

	err := group.Validate(map[string]string{keySignatureInterval: "14", keySignatureConcurrency: "17", keySignatureMaxPages: "-1"})

	var validation *ValidationError
	require.ErrorAs(t, err, &validation)
	assert.Len(t, validation.Fields, 3)
	assert.Contains(t, validation.Fields, keySignatureInterval)
	assert.Contains(t, validation.Fields, keySignatureConcurrency)
	assert.Contains(t, validation.Fields, keySignatureMaxPages)
}

func TestSave_PlatformDepositSignatures_StoresTheChainsAndRefusesAChainItCannotSync(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	actor := uuid.New()
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: &recordingActivity{}}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()

	_, err := service.SavePlatform(ctx, actor, groupDepositSignatures, map[string]any{keySignatureChains: []any{models.ChainSOL}, keySignatureInterval: 600})
	require.NoError(t, err)
	got, err := service.EffectiveDepositSignatures(ctx)
	require.NoError(t, err)
	assert.Equal(t, DepositSignatureValues{Chains: []string{models.ChainSOL}, ChainsStored: true, IntervalSeconds: 600}, got,
		"the next read sees the write")

	_, err = service.SavePlatform(ctx, actor, groupDepositSignatures, map[string]any{keySignatureChains: []any{models.ChainETH}})
	var validation *ValidationError
	require.ErrorAs(t, err, &validation, "only a chain whose adapter reads signatures can be listed")
	_, err = service.SavePlatform(ctx, actor, groupDepositSignatures, map[string]any{keySignatureInterval: 5})
	require.ErrorAs(t, err, &validation)

	_, err = service.SavePlatform(ctx, actor, groupDepositSignatures, map[string]any{keySignatureChains: []any{}})
	require.NoError(t, err)
	got, err = service.EffectiveDepositSignatures(ctx)
	require.NoError(t, err)
	assert.True(t, got.ChainsStored)
	assert.Empty(t, got.Chains, "an empty list turns the sync off")
}

func TestSave_Deposit_SignaturesIsNotAnAccountGroup(t *testing.T) {
	t.Parallel()

	_, err := newTestService(newMemoryStore()).Save(
		context.Background(), uuid.New(), uuid.New(), "owner", groupDepositSignatures,
		map[string]any{keySignatureInterval: 600},
	)
	require.ErrorIs(t, err, ErrGroupNotFound)
}
