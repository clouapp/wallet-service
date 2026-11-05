package stepup_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/stepup"
)

type fakeStore struct {
	values map[string]int64
	err    error
}

func (f *fakeStore) Put(key string, value any, _ time.Duration) error {
	if f.err != nil {
		return f.err
	}
	f.values[key] = value.(int64)
	return nil
}

func (f *fakeStore) GetInt64(key string, _ ...int64) int64 { return f.values[key] }

func (f *fakeStore) Forget(key string) bool {
	delete(f.values, key)
	return true
}

func newService(t *testing.T, now *time.Time) (*stepup.Service, *fakeStore) {
	t.Helper()
	store := &fakeStore{values: map[string]int64{}}
	svc, err := stepup.NewService(stepup.Deps{Store: store, Now: func() time.Time { return *now }})
	require.NoError(t, err)
	return svc, store
}

func TestGrant_OpensAFifteenMinuteWindowThatStatusReads(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 0, 0, 500_000_000, time.UTC)
	svc, _ := newService(t, &now)
	sid := uuid.New()

	expiresAt, err := svc.Grant(sid)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 11, 4, 10, 15, 0, 0, time.UTC), expiresAt)

	status, err := svc.Status(sid)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Equal(t, expiresAt, *status)
}

func TestStatus_OtherSessionAndNoWindowAreNil(t *testing.T) {
	now := time.Now()
	svc, _ := newService(t, &now)
	_, err := svc.Grant(uuid.New())
	require.NoError(t, err)

	status, err := svc.Status(uuid.New())
	require.NoError(t, err)
	assert.Nil(t, status)
	status, err = svc.Status(uuid.Nil)
	require.NoError(t, err)
	assert.Nil(t, status)
}

func TestStatus_NeverRenewsTheWindowAndEndsAtItsDeadline(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	svc, _ := newService(t, &now)
	sid := uuid.New()
	expiresAt, err := svc.Grant(sid)
	require.NoError(t, err)

	now = now.Add(10 * time.Minute)
	for i := 0; i < 3; i++ {
		status, err := svc.Status(sid)
		require.NoError(t, err)
		require.NotNil(t, status)
		assert.Equal(t, expiresAt, *status)
	}

	now = expiresAt
	status, err := svc.Status(sid)
	require.NoError(t, err)
	assert.Nil(t, status)
}

func TestGrant_OpensANewWindowOverAnOldOne(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	svc, _ := newService(t, &now)
	sid := uuid.New()
	_, err := svc.Grant(sid)
	require.NoError(t, err)

	now = now.Add(5 * time.Minute)
	second, err := svc.Grant(sid)
	require.NoError(t, err)
	assert.Equal(t, now.Add(stepup.Window), second)
}

func TestGrant_StoreFailureIsAnError(t *testing.T) {
	now := time.Now()
	svc, store := newService(t, &now)
	store.err = errors.New("down")
	_, err := svc.Grant(uuid.New())
	require.Error(t, err)
	_, err = svc.Grant(uuid.Nil)
	require.Error(t, err)
}

func TestForget_DropsTheWindows(t *testing.T) {
	now := time.Now()
	svc, _ := newService(t, &now)
	a, b := uuid.New(), uuid.New()
	_, _ = svc.Grant(a)
	_, _ = svc.Grant(b)

	svc.Forget(a, b)

	for _, sid := range []uuid.UUID{a, b} {
		status, err := svc.Status(sid)
		require.NoError(t, err)
		assert.Nil(t, status)
	}
}

func TestNewService_RequiresAStore(t *testing.T) {
	_, err := stepup.NewService(stepup.Deps{})
	require.Error(t, err)
}
