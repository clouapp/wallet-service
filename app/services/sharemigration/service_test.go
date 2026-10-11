package sharemigration

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/wallet"
)

type fakeWallets struct {
	rows    []models.Wallet
	set     map[uuid.UUID]string
	failSet error
}

func (f *fakeWallets) FindAll(context.Context) ([]models.Wallet, error) { return f.rows, nil }

func (f *fakeWallets) SetMPCSecretARN(_ context.Context, id uuid.UUID, arn string) error {
	if f.failSet != nil {
		return f.failSet
	}
	if f.set == nil {
		f.set = map[uuid.UUID]string{}
	}
	f.set[id] = arn
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows[i].MPCSecretARN = arn
		}
	}
	return nil
}

// fakeStore is one Secrets Manager: secrets by ARN, ARNs by name.
type fakeStore struct {
	account  string
	byARN    map[string][]byte
	byName   map[string]string
	created  int
	failRead map[string]error
	readBack []byte // when set, Binary of a created secret returns this instead
}

func newStore(account string) *fakeStore {
	return &fakeStore{account: account, byARN: map[string][]byte{}, byName: map[string]string{}, failRead: map[string]error{}}
}

func (s *fakeStore) put(name string, secret []byte) string {
	arn := fmt.Sprintf("arn:aws:secretsmanager:us-east-1:%s:secret:%s-AbCdEf", s.account, name)
	s.byARN[arn] = secret
	s.byName[name] = arn
	return arn
}

func (s *fakeStore) Binary(_ context.Context, id string) ([]byte, error) {
	if err := s.failRead[id]; err != nil {
		return nil, err
	}
	secret, ok := s.byARN[id]
	if !ok {
		return nil, errors.New("not found")
	}
	if s.readBack != nil {
		return s.readBack, nil
	}
	return secret, nil
}

func (s *fakeStore) Lookup(_ context.Context, name string) (string, bool, error) {
	arn, ok := s.byName[name]
	return arn, ok, nil
}

func (s *fakeStore) Create(_ context.Context, name string, secret []byte) (string, error) {
	s.created++
	return s.put(name, secret), nil
}

type fixture struct {
	wallets *fakeWallets
	source  *fakeStore
	target  *fakeStore
	service *Service
	ids     []uuid.UUID
}

// newFixture puts n wallets whose share B lives only in the source (LocalStack).
func newFixture(n int) *fixture {
	f := &fixture{wallets: &fakeWallets{}, source: newStore("000000000000"), target: newStore("430776404482")}
	for i := 0; i < n; i++ {
		id := uuid.New()
		arn := f.source.put(wallet.ShareBSecretName(id), []byte(fmt.Sprintf("share-%d", i)))
		f.wallets.rows = append(f.wallets.rows, models.Wallet{ID: id, Chain: "eth", MPCSecretARN: arn})
		f.ids = append(f.ids, id)
	}
	f.service = NewService(Deps{Wallets: f.wallets, Target: f.target})
	return f
}

func TestShare_Migration_Run_DryRunWritesNothing(t *testing.T) {
	f := newFixture(2)

	rows, err := f.service.Run(context.Background(), f.source, false)

	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		assert.Equal(t, StatusWouldMigrate, row.Status)
	}
	assert.Zero(t, f.target.created)
	assert.Empty(t, f.wallets.set)
}

func TestShare_Migration_Run_CopiesVerifiesAndRewritesTheARN(t *testing.T) {
	f := newFixture(1)
	sourceARN := f.wallets.rows[0].MPCSecretARN

	rows, err := f.service.Run(context.Background(), f.source, true)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, StatusMigrated, rows[0].Status)
	assert.Equal(t, sourceARN, rows[0].From)
	targetARN := f.target.byName[wallet.ShareBSecretName(f.ids[0])]
	assert.Equal(t, targetARN, rows[0].To)
	assert.Equal(t, targetARN, f.wallets.set[f.ids[0]])
	assert.Equal(t, []byte("share-0"), f.target.byARN[targetARN])
	assert.Equal(t, []byte("share-0"), f.source.byARN[sourceARN], "the source must be left as it was")
}

func TestShare_Migration_Run_IsIdempotent(t *testing.T) {
	f := newFixture(1)
	_, err := f.service.Run(context.Background(), f.source, true)
	require.NoError(t, err)

	rows, err := f.service.Run(context.Background(), f.source, true)

	require.NoError(t, err)
	assert.Equal(t, StatusInPlace, rows[0].Status)
	assert.Equal(t, 1, f.target.created)
}

func TestShare_Migration_Run_PointsAtAnEqualExistingSecret(t *testing.T) {
	f := newFixture(1)
	existing := f.target.put(wallet.ShareBSecretName(f.ids[0]), []byte("share-0"))

	dry, err := f.service.Run(context.Background(), f.source, false)
	require.NoError(t, err)
	assert.Equal(t, StatusWouldPoint, dry[0].Status)
	assert.Empty(t, f.wallets.set)

	rows, err := f.service.Run(context.Background(), f.source, true)

	require.NoError(t, err)
	assert.Equal(t, StatusPointed, rows[0].Status)
	assert.Equal(t, existing, f.wallets.set[f.ids[0]])
	assert.Zero(t, f.target.created)
}

func TestShare_Migration_Run_RefusesAConflictingSecret(t *testing.T) {
	f := newFixture(1)
	f.target.put(wallet.ShareBSecretName(f.ids[0]), []byte("someone else's share"))

	rows, err := f.service.Run(context.Background(), f.source, true)

	require.ErrorIs(t, err, ErrConflict)
	assert.Equal(t, StatusFailed, rows[0].Status)
	assert.Empty(t, f.wallets.set)
	assert.Zero(t, f.target.created)
}

func TestShare_Migration_Run_ReportsAReadBackMismatch(t *testing.T) {
	f := newFixture(1)
	f.target.readBack = []byte("corrupted")

	rows, err := f.service.Run(context.Background(), f.source, true)

	require.ErrorIs(t, err, ErrReadBack)
	assert.Equal(t, StatusFailed, rows[0].Status)
	assert.Empty(t, f.wallets.set, "the row must keep the source ARN")
}

func TestShare_Migration_Run_SkipsWalletsWithoutAShare(t *testing.T) {
	f := newFixture(1)
	f.wallets.rows = append(f.wallets.rows, models.Wallet{ID: uuid.New(), Chain: "btc"})

	rows, err := f.service.Run(context.Background(), f.source, false)

	require.NoError(t, err)
	assert.Len(t, rows, 1)
}

func TestShare_Migration_Run_ContinuesPastAFailedWallet(t *testing.T) {
	f := newFixture(2)
	f.source.failRead[f.wallets.rows[0].MPCSecretARN] = errors.New("source down")

	rows, err := f.service.Run(context.Background(), f.source, true)

	require.Error(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, StatusFailed, rows[0].Status)
	assert.Equal(t, StatusMigrated, rows[1].Status)
	assert.NotContains(t, f.wallets.set, f.ids[0])
}

func TestShare_Migration_Run_FindsTheCopyAfterAFailedARNUpdate(t *testing.T) {
	f := newFixture(1)
	f.wallets.failSet = errors.New("database down")
	_, err := f.service.Run(context.Background(), f.source, true)
	require.Error(t, err)
	f.wallets.failSet = nil

	rows, err := f.service.Run(context.Background(), f.source, true)

	require.NoError(t, err)
	assert.Equal(t, StatusPointed, rows[0].Status)
	assert.Equal(t, 1, f.target.created)
}
