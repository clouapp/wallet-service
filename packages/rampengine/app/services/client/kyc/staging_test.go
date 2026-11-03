package kyc

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/goravel/framework/testing/mock"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const selfieImage = "data:image/jpeg;base64,SELFIEBYTES"

func staged() StagedSubmission {
	app := ramp.Application{
		Type:              "individual",
		SignedAgreementID: "sa_1",
		Profile:           ramp.Profile{FirstName: "Luiza", Email: "luiza@example.com"},
		IdentifyingInformation: []ramp.IdentifyingDocument{
			{Type: "cpf", Number: "39053344705"},
		},
		LivenessCheckSelfies: []ramp.Selfie{{Image: selfieImage}},
	}
	return StagedSubmission{
		CustomerID: 7, UserID: 7, Provider: models.ProviderBridge, Kind: KindApplication,
		PreviousStatus: models.KycStatusNotStarted, Application: &app,
	}
}

// The property that matters: what lands in Redis is ciphertext under a TTL.
// Redis has a dump file, a MONITOR command and an operator, and none of them
// should ever see a customer's identity documents.
func TestPutStoresOnlyCiphertextUnderTTL(t *testing.T) {
	factory := mock.Factory()
	crypt := factory.Crypt()
	cache := factory.Cache()

	var plaintext string
	crypt.On("EncryptString", testifymock.Anything).Return("ciphertext", nil).
		Run(func(args testifymock.Arguments) { plaintext = args.String(0) })

	var storedKey string
	var storedValue any
	var storedTTL time.Duration
	// Put also writes the in-flight mark and its reverse index, so the payload
	// write is picked out by its prefix rather than by being the last one.
	cache.On("Put", testifymock.Anything, testifymock.Anything, testifymock.Anything).Return(nil).
		Run(func(args testifymock.Arguments) {
			if !strings.HasPrefix(args.String(0), stagingKeyPrefix) {
				return
			}
			storedKey, storedValue, storedTTL = args.String(0), args.Get(1), args.Get(2).(time.Duration)
		})

	id, err := NewCacheStaging().Put(staged())
	require.NoError(t, err)

	require.Equal(t, "ciphertext", storedValue, "the payload must never reach the cache in the clear")
	require.NotContains(t, storedKey, selfieImage)
	require.Equal(t, StagingTTL, storedTTL)
	require.True(t, strings.HasPrefix(storedKey, stagingKeyPrefix))
	require.Equal(t, stagingKeyPrefix+id, storedKey, "the id returned is the handle the worker looks the payload up by")

	// The encryptor received the whole submission, images included.
	var decoded StagedSubmission
	require.NoError(t, json.Unmarshal([]byte(plaintext), &decoded))
	require.Equal(t, selfieImage, decoded.Application.LivenessCheckSelfies[0].Image)
}

// The worker times its retries against the staging deadline, and the payload
// is all it reads: the moment it was staged has to travel inside it.
func TestPutStampsWhenTheSubmissionWasStaged(t *testing.T) {
	factory := mock.Factory()
	var plaintext string
	factory.Crypt().On("EncryptString", testifymock.Anything).Return("ciphertext", nil).
		Run(func(args testifymock.Arguments) { plaintext = args.String(0) })
	factory.Cache().On("Put", testifymock.Anything, testifymock.Anything, testifymock.Anything).Return(nil)

	before := time.Now()
	_, err := NewCacheStaging().Put(staged())
	require.NoError(t, err)

	var decoded StagedSubmission
	require.NoError(t, json.Unmarshal([]byte(plaintext), &decoded))
	require.WithinRange(t, decoded.StagedAt, before, time.Now())
}

// The id is the submission's idempotency seed. Two submissions must never
// collide on one, or a genuine resubmission after a rejection would be
// swallowed upstream as a duplicate of the first.
func TestPutGeneratesDistinctIDs(t *testing.T) {
	factory := mock.Factory()
	factory.Crypt().On("EncryptString", testifymock.Anything).Return("ciphertext", nil)
	factory.Cache().On("Put", testifymock.Anything, testifymock.Anything, testifymock.Anything).Return(nil)

	staging := NewCacheStaging()
	first, err := staging.Put(staged())
	require.NoError(t, err)
	second, err := staging.Put(staged())
	require.NoError(t, err)
	require.NotEqual(t, first, second)
}

func TestGetOpensStagedSubmission(t *testing.T) {
	factory := mock.Factory()
	encoded, err := json.Marshal(staged())
	require.NoError(t, err)
	factory.Cache().On("GetString", stagingKeyPrefix+"stg_1", testifymock.Anything).Return("ciphertext")
	factory.Crypt().On("DecryptString", "ciphertext").Return(string(encoded), nil)

	got, err := NewCacheStaging().Get("stg_1")
	require.NoError(t, err)
	require.Equal(t, uint(7), got.UserID)
	require.Equal(t, models.KycStatusNotStarted, got.PreviousStatus)
	require.Equal(t, "39053344705", got.Application.IdentifyingInformation[0].Number)
}

// A payload past its TTL is gone for good. The worker has to be able to tell
// that apart from a transport error, because retrying cannot bring it back.
func TestGetReportsExpiredWhenAbsent(t *testing.T) {
	factory := mock.Factory()
	factory.Cache().On("GetString", stagingKeyPrefix+"stg_gone", testifymock.Anything).Return("")

	_, err := NewCacheStaging().Get("stg_gone")
	require.ErrorIs(t, err, ErrStagingExpired)
}

func TestForgetRemovesThePayload(t *testing.T) {
	factory := mock.Factory()
	cache := factory.Cache()
	cache.On("GetString", inFlightOwnerKeyPrefix+"stg_1", testifymock.Anything).Return("")
	cache.On("Forget", testifymock.Anything).Return(true)

	NewCacheStaging().Forget("stg_1")
	cache.AssertCalled(t, "Forget", stagingKeyPrefix+"stg_1")
}

// The mark is what tells a second submission which kind is already queued. The
// row cannot: it says `submitting` for both, which is how every documents
// submission came to bypass the duplicate check instead of only the one
// chaining behind an application.
func TestPutMarksWhichKindIsInFlight(t *testing.T) {
	factory := mock.Factory()
	factory.Crypt().On("EncryptString", testifymock.Anything).Return("ciphertext", nil)
	cache := factory.Cache()
	written := map[string]any{}
	cache.On("Put", testifymock.Anything, testifymock.Anything, testifymock.Anything).Return(nil).
		Run(func(args testifymock.Arguments) { written[args.String(0)] = args.Get(1) })

	id, err := NewCacheStaging().Put(staged())
	require.NoError(t, err)

	require.Equal(t, KindApplication+":"+id, written[inFlightKey(7, models.ProviderBridge)],
		"the mark carries the kind, which is the question the guard asks, and whose it is")
	require.Equal(t, inFlightKey(7, models.ProviderBridge), written[inFlightOwnerKeyPrefix+id],
		"Forget knows only the submission id, so the reverse index is what lets the mark die with the payload")
}

// The mark is the customer's: a member sending for a company marks the
// company's submission, not anything of their own (spec §5, F1).
func TestPutMarksTheCustomersSubmission(t *testing.T) {
	factory := mock.Factory()
	factory.Crypt().On("EncryptString", testifymock.Anything).Return("ciphertext", nil)
	cache := factory.Cache()
	written := map[string]any{}
	cache.On("Put", testifymock.Anything, testifymock.Anything, testifymock.Anything).Return(nil).
		Run(func(args testifymock.Arguments) { written[args.String(0)] = args.Get(1) })
	submission := staged()
	submission.CustomerID = 1_000_000_000_000

	id, err := NewCacheStaging().Put(submission)
	require.NoError(t, err)

	require.Equal(t, KindApplication+":"+id, written[inFlightKey(1_000_000_000_000, models.ProviderBridge)])
	require.NotContains(t, written, inFlightKey(7, models.ProviderBridge), "the sender holds nothing in flight")
}

// Without the reverse index the mark would outlive the submission by the whole
// staging TTL, and every documents submission in that window would be refused.
func TestForgetReleasesTheMark(t *testing.T) {
	factory := mock.Factory()
	cache := factory.Cache()
	owner := inFlightKey(7, models.ProviderBridge)
	cache.On("GetString", inFlightOwnerKeyPrefix+"stg_1", testifymock.Anything).Return(owner)
	cache.On("GetString", owner, testifymock.Anything).Return(KindApplication + ":stg_1")
	cache.On("Forget", testifymock.Anything).Return(true)

	NewCacheStaging().Forget("stg_1")

	cache.AssertCalled(t, "Forget", owner)
	cache.AssertCalled(t, "Forget", inFlightOwnerKeyPrefix+"stg_1")
}

// Documents chained behind an application replace its mark. The application
// finishing first must not take the documents' mark with it: that is the
// window where the provider already answered the application — Bridge reads
// `incomplete` asking for the document and the selfie — while those very
// files are still queued, and the mark is the only thing that says so
// (production, 28/09 18:04:28 UTC).
func TestForgetKeepsTheMarkOfALaterSubmission(t *testing.T) {
	factory := mock.Factory()
	cache := factory.Cache()
	owner := inFlightKey(7, models.ProviderBridge)
	cache.On("GetString", inFlightOwnerKeyPrefix+"stg_app", testifymock.Anything).Return(owner)
	cache.On("GetString", owner, testifymock.Anything).Return(KindDocuments + ":stg_docs")
	cache.On("Forget", testifymock.Anything).Return(true)

	NewCacheStaging().Forget("stg_app")

	cache.AssertCalled(t, "Forget", stagingKeyPrefix+"stg_app")
	cache.AssertCalled(t, "Forget", inFlightOwnerKeyPrefix+"stg_app")
	cache.AssertNotCalled(t, "Forget", owner)
}

// A mark written before it named its submission is released the old way.
func TestForgetReleasesAMarkThatNamesNoSubmission(t *testing.T) {
	factory := mock.Factory()
	cache := factory.Cache()
	owner := inFlightKey(7, models.ProviderBridge)
	cache.On("GetString", inFlightOwnerKeyPrefix+"stg_1", testifymock.Anything).Return(owner)
	cache.On("GetString", owner, testifymock.Anything).Return(KindApplication)
	cache.On("Forget", testifymock.Anything).Return(true)

	NewCacheStaging().Forget("stg_1")

	cache.AssertCalled(t, "Forget", owner)
}

func TestInFlightKindReadsTheMark(t *testing.T) {
	factory := mock.Factory()
	factory.Cache().On("GetString", inFlightKey(7, models.ProviderBridge), testifymock.Anything).
		Return(KindDocuments + ":stg_1")

	require.Equal(t, KindDocuments,
		NewCacheStaging().InFlightKind(7, models.ProviderBridge))
}

// Identity documents never reach Redis in the clear, not even on a failure.
func TestPutStagesNothingItCouldNotSeal(t *testing.T) {
	factory := mock.Factory()
	factory.Crypt().On("EncryptString", testifymock.Anything).Return("", errors.New("crypt: no key"))
	cache := factory.Cache()

	id, err := NewCacheStaging().Put(staged())
	require.Error(t, err)
	require.Empty(t, id)
	cache.AssertNotCalled(t, "Put", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The in-flight mark is best-effort: a lost one costs only the duplicate check,
// which fails open, so only the payload write can refuse the submission.
func TestPutRefusesOnlyWhenThePayloadIsNotStored(t *testing.T) {
	down := errors.New("redis: connection refused")
	owner := inFlightKey(7, models.ProviderBridge)
	cases := map[string]struct {
		payloadErr, markErr error
		wantMarked          bool
	}{
		"payload not stored": {payloadErr: down},
		"mark not stored":    {markErr: down},
		"both stored":        {wantMarked: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			factory := mock.Factory()
			factory.Log()
			factory.Crypt().On("EncryptString", testifymock.Anything).Return("ciphertext", nil)
			written := map[string]any{}
			record := func(args testifymock.Arguments) { written[args.String(0)] = args.Get(1) }
			cache := factory.Cache()
			cache.On("Put", testifymock.MatchedBy(func(key string) bool {
				return strings.HasPrefix(key, stagingKeyPrefix)
			}), "ciphertext", StagingTTL).Return(c.payloadErr).Once()
			cache.On("Put", owner, testifymock.MatchedBy(func(mark string) bool {
				return strings.HasPrefix(mark, KindApplication+":")
			}), StagingTTL).Return(c.markErr).Run(record).Maybe()
			cache.On("Put", testifymock.MatchedBy(func(key string) bool {
				return strings.HasPrefix(key, inFlightOwnerKeyPrefix)
			}), owner, StagingTTL).Return(nil).Run(record).Maybe()

			id, err := NewCacheStaging().Put(staged())
			require.ErrorIs(t, err, c.payloadErr)
			require.Equal(t, c.payloadErr == nil, id != "")
			require.Equal(t, c.wantMarked, written[inFlightOwnerKeyPrefix+id] == owner)
		})
	}
}

// A payload that does not open is not an expired one: the worker records a
// different failure for each.
func TestGetRefusesAPayloadItCannotOpen(t *testing.T) {
	factory := mock.Factory()
	factory.Cache().On("GetString", stagingKeyPrefix+"stg_1", testifymock.Anything).Return("tampered")
	factory.Crypt().On("DecryptString", "tampered").Return("", errors.New("crypt: invalid mac"))

	_, err := NewCacheStaging().Get("stg_1")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrStagingExpired)
}

// Update writes the worker's progress over the payload without buying it more
// time: the images still leave Redis StagingTTL after they were first staged.
func TestUpdateResealsForWhatIsLeftOfTheTTL(t *testing.T) {
	factory := mock.Factory()
	cache := factory.Cache()
	cache.On("GetString", stagingKeyPrefix+"stg_1", testifymock.Anything).Return("old")
	var plaintext string
	factory.Crypt().On("EncryptString", testifymock.Anything).Return("ciphertext", nil).
		Run(func(args testifymock.Arguments) { plaintext = args.String(0) })
	var storedTTL time.Duration
	cache.On("Put", stagingKeyPrefix+"stg_1", "ciphertext", testifymock.Anything).Return(nil).
		Run(func(args testifymock.Arguments) { storedTTL = args.Get(2).(time.Duration) }).Once()

	submission := staged()
	submission.StagedAt = time.Now().Add(-10 * time.Minute)
	submission.DispatchAttempts = 3
	require.NoError(t, NewCacheStaging().Update("stg_1", submission))

	require.Greater(t, storedTTL, time.Duration(0))
	require.LessOrEqual(t, storedTTL, 5*time.Minute, "the TTL is what was left, never a fresh one")
	var decoded StagedSubmission
	require.NoError(t, json.Unmarshal([]byte(plaintext), &decoded))
	require.Equal(t, 3, decoded.DispatchAttempts)
}

// A payload already gone, or out of time, is not written back to life.
func TestUpdateRefusesAnExpiredPayload(t *testing.T) {
	t.Run("gone", func(t *testing.T) {
		factory := mock.Factory()
		factory.Cache().On("GetString", stagingKeyPrefix+"stg_gone", testifymock.Anything).Return("")
		submission := staged()
		submission.StagedAt = time.Now()
		require.ErrorIs(t, NewCacheStaging().Update("stg_gone", submission), ErrStagingExpired)
	})
	t.Run("out of time", func(t *testing.T) {
		factory := mock.Factory()
		factory.Cache().On("GetString", stagingKeyPrefix+"stg_1", testifymock.Anything).Return("old")
		submission := staged()
		submission.StagedAt = time.Now().Add(-StagingTTL - time.Second)
		require.ErrorIs(t, NewCacheStaging().Update("stg_1", submission), ErrStagingExpired)
	})
}
