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

func (f *fakeWallets) FindAll(context.Context) ([]models.Wallet, error) {
	return append([]models.Wallet(nil), f.rows...), nil
}

// SetMPCSecretARN moves a row only when it still holds from, like the repository.
func (f *fakeWallets) SetMPCSecretARN(_ context.Context, id uuid.UUID, from, to string) error {
	if f.failSet != nil {
		return f.failSet
	}
	for i := range f.rows {
		if f.rows[i].ID != id {
			continue
		}
		if f.rows[i].MPCSecretARN != from {
			return errors.New("the row moved")
		}
		f.rows[i].MPCSecretARN = to
		if f.set == nil {
			f.set = map[uuid.UUID]string{}
		}
		f.set[id] = to
		return nil
	}
	return errors.New("no row")
}

// fakeStore is one Secrets Manager: secrets by ARN, ARNs by name. It hands out
// copies, as the SDK does, so the service zeroing its buffers leaves it intact.
type fakeStore struct {
	account  string
	byARN    map[string][]byte
	byName   map[string]string
	created  int
	failRead map[string]error
	readBack []byte // when set, every read returns this instead
}

func newStore(account string) *fakeStore {
	return &fakeStore{account: account, byARN: map[string][]byte{}, byName: map[string]string{}, failRead: map[string]error{}}
}

func (s *fakeStore) put(name string, secret []byte) string {
	arn := fmt.Sprintf("arn:aws:secretsmanager:us-east-1:%s:secret:%s-AbCdEf", s.account, name)
	s.byARN[arn] = append([]byte(nil), secret...)
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
		return append([]byte(nil), s.readBack...), nil
	}
	return append([]byte(nil), secret...), nil
}

func (s *fakeStore) Lookup(_ context.Context, name string) (string, bool, error) {
	arn, ok := s.byName[name]
	return arn, ok, nil
}

func (s *fakeStore) Create(_ context.Context, name string, secret []byte) (string, error) {
	s.created++
	return s.put(name, secret), nil
}

const localstack = "http://127.0.0.1:4566"

type fixture struct {
	wallets *fakeWallets
	source  *fakeStore
	target  *fakeStore
	deps    Deps
	ids     []uuid.UUID
}

// newFixture puts n wallets whose share B lives only in the source (LocalStack);
// the target is AWS, sealed with the master key.
func newFixture(n int) *fixture {
	f := &fixture{wallets: &fakeWallets{}, source: newStore("000000000000"), target: newStore("430776404482")}
	for i := 0; i < n; i++ {
		id := uuid.New()
		arn := f.source.put(wallet.ShareBSecretName(id), []byte(fmt.Sprintf("share-%d", i)))
		f.wallets.rows = append(f.wallets.rows, models.Wallet{ID: id, Chain: "eth", MPCSecretARN: arn})
		f.ids = append(f.ids, id)
	}
	f.deps = Deps{
		Wallets:        f.wallets,
		Sources:        func(string, string) (Source, error) { return f.source, nil },
		Target:         f.target,
		TargetKMSKeyID: "alias/waas-dev-master",
	}
	return f
}

func (f *fixture) run(t *testing.T, apply bool) (Report, error) {
	t.Helper()
	return NewService(f.deps).Run(context.Background(), Request{SourceEndpoint: localstack, Apply: apply})
}

func TestService_Run_DryRunWritesNothing(t *testing.T) {
	f := newFixture(2)

	report, err := f.run(t, false)

	require.NoError(t, err)
	require.Len(t, report.Rows, 2)
	for _, row := range report.Rows {
		assert.Equal(t, StatusWouldMigrate, row.Status)
	}
	assert.Zero(t, f.target.created)
	assert.Empty(t, f.wallets.set)
	assert.Equal(t, "alias/waas-dev-master", report.TargetKMSKeyID)
}

func TestService_Run_CopiesVerifiesAndRewritesTheARN(t *testing.T) {
	f := newFixture(1)
	sourceARN := f.wallets.rows[0].MPCSecretARN

	report, err := f.run(t, true)

	require.NoError(t, err)
	require.Len(t, report.Rows, 1)
	assert.Equal(t, StatusMigrated, report.Rows[0].Status)
	assert.Equal(t, sourceARN, report.Rows[0].From)
	targetARN := f.target.byName[wallet.ShareBSecretName(f.ids[0])]
	assert.Equal(t, targetARN, report.Rows[0].To)
	assert.Equal(t, targetARN, f.wallets.set[f.ids[0]])
	assert.Equal(t, []byte("share-0"), f.target.byARN[targetARN])
	assert.Equal(t, []byte("share-0"), f.source.byARN[sourceARN], "the source must be left as it was")
}

func TestService_Run_IsIdempotent(t *testing.T) {
	f := newFixture(1)
	_, err := f.run(t, true)
	require.NoError(t, err)

	report, err := f.run(t, true)

	require.NoError(t, err)
	assert.Equal(t, StatusInPlace, report.Rows[0].Status)
	assert.Equal(t, 1, f.target.created)
}

func TestService_Run_PointsAtAnEqualExistingSecret(t *testing.T) {
	f := newFixture(1)
	existing := f.target.put(wallet.ShareBSecretName(f.ids[0]), []byte("share-0"))

	dry, err := f.run(t, false)
	require.NoError(t, err)
	assert.Equal(t, StatusWouldPoint, dry.Rows[0].Status)
	assert.Empty(t, f.wallets.set)

	report, err := f.run(t, true)

	require.NoError(t, err)
	assert.Equal(t, StatusPointed, report.Rows[0].Status)
	assert.Equal(t, existing, f.wallets.set[f.ids[0]])
	assert.Zero(t, f.target.created)
}

func TestService_Run_RefusesAConflictingSecret(t *testing.T) {
	f := newFixture(1)
	f.target.put(wallet.ShareBSecretName(f.ids[0]), []byte("someone else's share"))

	report, err := f.run(t, true)

	require.ErrorIs(t, err, ErrConflict)
	assert.Equal(t, StatusFailed, report.Rows[0].Status)
	assert.Empty(t, f.wallets.set)
	assert.Zero(t, f.target.created)
}

func TestService_Run_ReportsAReadBackMismatch(t *testing.T) {
	f := newFixture(1)
	f.target.readBack = []byte("corrupted")

	report, err := f.run(t, true)

	require.ErrorIs(t, err, ErrReadBack)
	assert.Equal(t, StatusFailed, report.Rows[0].Status)
	assert.Empty(t, f.wallets.set, "the row must keep the source ARN")
}

func TestService_Run_RefusesAnEmptySourceShareBeforeWriting(t *testing.T) {
	f := newFixture(1)
	f.source.byARN[f.wallets.rows[0].MPCSecretARN] = nil

	report, err := f.run(t, true)

	require.ErrorIs(t, err, ErrEmptyShare)
	assert.Equal(t, StatusFailed, report.Rows[0].Status)
	assert.Zero(t, f.target.created)
	assert.Empty(t, f.wallets.set)
}

func TestService_Run_InPlaceMeansTheCopyOpens(t *testing.T) {
	f := newFixture(1)
	_, err := f.run(t, true)
	require.NoError(t, err)
	f.target.readBack = []byte{}

	report, err := f.run(t, false)

	require.ErrorIs(t, err, ErrEmptyShare)
	assert.Equal(t, StatusFailed, report.Rows[0].Status)
}

func TestService_Run_RefusesASourceThatIsTheTarget(t *testing.T) {
	f := newFixture(1)
	f.deps.TargetEndpoint = localstack

	_, err := f.run(t, false)

	require.ErrorIs(t, err, ErrSameStore)
	assert.Empty(t, f.wallets.set)
}

func TestService_Run_RefusesToWriteIntoAWSWithoutAMasterKey(t *testing.T) {
	f := newFixture(1)
	f.deps.TargetKMSKeyID = ""

	_, dryErr := f.run(t, false)
	_, err := f.run(t, true)

	require.NoError(t, dryErr, "a dry run only reads")
	require.ErrorIs(t, err, ErrUnsealedTarget)
	assert.Zero(t, f.target.created)
}

func TestService_Run_SkipsWalletsWithoutAShare(t *testing.T) {
	f := newFixture(1)
	f.wallets.rows = append(f.wallets.rows, models.Wallet{ID: uuid.New(), Chain: "btc"})

	report, err := f.run(t, false)

	require.NoError(t, err)
	assert.Len(t, report.Rows, 1)
}

func TestService_Run_ContinuesPastAFailedWallet(t *testing.T) {
	f := newFixture(2)
	f.source.failRead[f.wallets.rows[0].MPCSecretARN] = errors.New("source down")

	report, err := f.run(t, true)

	require.Error(t, err)
	require.Len(t, report.Rows, 2)
	assert.Equal(t, StatusFailed, report.Rows[0].Status)
	assert.Equal(t, StatusMigrated, report.Rows[1].Status)
	assert.NotContains(t, f.wallets.set, f.ids[0])
}

func TestService_Run_FindsTheCopyAfterAFailedARNUpdate(t *testing.T) {
	f := newFixture(1)
	f.wallets.failSet = errors.New("database down")
	_, err := f.run(t, true)
	require.Error(t, err)
	f.wallets.failSet = nil

	report, err := f.run(t, true)

	require.NoError(t, err)
	assert.Equal(t, StatusPointed, report.Rows[0].Status)
	assert.Equal(t, 1, f.target.created)
}

func TestService_Run_ReportsARowThatMovedMeanwhile(t *testing.T) {
	f := newFixture(1)
	sourceARN := f.wallets.rows[0].MPCSecretARN
	f.deps.Wallets = &movingWallets{fakeWallets: f.wallets}

	report, err := f.run(t, true)

	require.Error(t, err)
	assert.Equal(t, StatusFailed, report.Rows[0].Status)
	assert.Equal(t, sourceARN+"-moved", f.wallets.rows[0].MPCSecretARN, "a row another writer moved is not overwritten")
}

// movingWallets lists the rows as they were, then lets another writer move one
// before the migration updates it.
type movingWallets struct{ *fakeWallets }

func (m *movingWallets) FindAll(ctx context.Context) ([]models.Wallet, error) {
	rows, err := m.fakeWallets.FindAll(ctx)
	m.fakeWallets.rows[0].MPCSecretARN += "-moved"
	return rows, err
}
