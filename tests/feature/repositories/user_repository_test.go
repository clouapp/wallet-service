package repositories_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type UserRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.UserRepository
}

func TestUser_Repository_Suite(t *testing.T) {
	suite.Run(t, new(UserRepositoryTestSuite))
}

func (s *UserRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewUserRepository(nil)
}

func (s *UserRepositoryTestSuite) TestUserRepository_Create_Success() {
	user := &models.User{
		ID:           uuid.New(),
		Email:        "test@example.com",
		PasswordHash: "hashed_pw",
		Status:       "active",
	}
	err := s.repo.Create(context.Background(), user)
	s.NoError(err)
	s.NotNil(user.Preferences)

	found, err := s.repo.FindByEmail(context.Background(), "test@example.com")
	s.NoError(err)
	s.NotNil(found)
	s.Equal(user.ID, found.ID)
	s.Equal("test@example.com", found.Email)
	s.NotNil(found.Preferences)
}

func (s *UserRepositoryTestSuite) TestFind_ByEmail_Found() {
	user := &models.User{
		ID:           uuid.New(),
		Email:        "found@example.com",
		PasswordHash: "hash",
		Status:       "active",
	}
	s.Require().NoError(s.repo.Create(context.Background(), user))

	found, err := s.repo.FindByEmail(context.Background(), "found@example.com")
	s.NoError(err)
	s.NotNil(found)
	s.Equal(user.ID, found.ID)
}

func (s *UserRepositoryTestSuite) TestFind_ByEmail_NotFound() {
	found, err := s.repo.FindByEmail(context.Background(), "nonexistent@example.com")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *UserRepositoryTestSuite) TestFind_ByID_Found() {
	user := &models.User{
		ID:           uuid.New(),
		Email:        "byid@example.com",
		PasswordHash: "hash",
		Status:       "active",
	}
	s.Require().NoError(s.repo.Create(context.Background(), user))

	found, err := s.repo.FindByID(context.Background(), user.ID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal("byid@example.com", found.Email)
}

func (s *UserRepositoryTestSuite) TestFind_ByID_NotFound() {
	found, err := s.repo.FindByID(context.Background(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *UserRepositoryTestSuite) TestUpdate_Full_Name() {
	user := &models.User{
		ID:           uuid.New(),
		Email:        "name@example.com",
		PasswordHash: "hash",
		FullName:     "Old Name",
		Status:       "active",
	}
	s.Require().NoError(s.repo.Create(context.Background(), user))

	err := s.repo.UpdateFullName(context.Background(), user.ID, "New Name")
	s.NoError(err)

	found, err := s.repo.FindByID(context.Background(), user.ID)
	s.NoError(err)
	s.Equal("New Name", found.FullName)
}

func (s *UserRepositoryTestSuite) TestUpdate_Password_Hash() {
	user := &models.User{
		ID:           uuid.New(),
		Email:        "pw@example.com",
		PasswordHash: "old_hash",
		Status:       "active",
	}
	s.Require().NoError(s.repo.Create(context.Background(), user))

	err := s.repo.UpdatePasswordHash(context.Background(), user.ID, "new_hash")
	s.NoError(err)

	found, err := s.repo.FindByID(context.Background(), user.ID)
	s.NoError(err)
	s.Equal("new_hash", found.PasswordHash)
}

func (s *UserRepositoryTestSuite) TestAdvance_TotpCounter_OnlyMovesForward() {
	userID := insertActiveUserRow(s.T())

	advanced, err := s.repo.AdvanceTotpCounter(context.Background(), userID, 100)
	s.Require().NoError(err)
	s.True(advanced, "a newer step is recorded")

	advanced, err = s.repo.AdvanceTotpCounter(context.Background(), userID, 100)
	s.Require().NoError(err)
	s.False(advanced, "the same step is a replay")

	advanced, err = s.repo.AdvanceTotpCounter(context.Background(), userID, 99)
	s.Require().NoError(err)
	s.False(advanced, "an older step is a replay")

	found, err := s.repo.FindByID(context.Background(), userID)
	s.Require().NoError(err)
	s.Equal(int64(100), found.TotpLastUsedCounter)
}

func (s *UserRepositoryTestSuite) TestUpdate_Sessions_RevokedAt() {
	userID := insertActiveUserRow(s.T())
	found, err := s.repo.FindByID(context.Background(), userID)
	s.Require().NoError(err)
	s.Nil(found.SessionsRevokedAt, "no watermark until the first revocation")

	watermark := time.Date(2026, 10, 2, 12, 0, 6, 0, time.UTC)
	s.Require().NoError(s.repo.UpdateSessionsRevokedAt(context.Background(), userID, watermark))

	found, err = s.repo.FindByID(context.Background(), userID)
	s.Require().NoError(err)
	s.Require().NotNil(found.SessionsRevokedAt)
	s.True(watermark.Equal(*found.SessionsRevokedAt))
}

func (s *UserRepositoryTestSuite) TestSet_Suspended_AtSetsAndClearsTheColumn() {
	userID := insertActiveUserRow(s.T())
	found, err := s.repo.FindByID(context.Background(), userID)
	s.Require().NoError(err)
	s.Nil(found.SuspendedAt)

	at := time.Date(2026, 10, 3, 18, 4, 0, 0, time.UTC)
	s.Require().NoError(s.repo.SetSuspendedAt(context.Background(), userID, &at))
	found, err = s.repo.FindByID(context.Background(), userID)
	s.Require().NoError(err)
	s.Require().NotNil(found.SuspendedAt)
	s.True(at.Equal(found.SuspendedAt.UTC()))
	s.Nil(found.SuspensionReason)

	s.Require().NoError(s.repo.SetSuspendedAt(context.Background(), userID, nil))
	found, err = s.repo.FindByID(context.Background(), userID)
	s.Require().NoError(err)
	s.Nil(found.SuspendedAt)
	s.EqualError(s.repo.SetSuspendedAt(context.Background(), uuid.Nil, &at), "set suspended at: user id is required")
}

func (s *UserRepositoryTestSuite) TestList_Orders_ByCreatedAtDescending() {
	older := insertActiveUserRow(s.T())
	newer := insertActiveUserRow(s.T())
	s.stampCreatedAt(older, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	s.stampCreatedAt(newer, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))

	first, total, err := s.repo.List(context.Background(), 1, 0)
	s.Require().NoError(err)
	s.Equal(int64(2), total)
	s.Require().Len(first, 1)
	s.Equal(newer, first[0].ID)

	second, total, err := s.repo.List(context.Background(), 1, 1)
	s.Require().NoError(err)
	s.Equal(int64(2), total)
	s.Require().Len(second, 1)
	s.Equal(older, second[0].ID)

	past, total, err := s.repo.List(context.Background(), 20, 2)
	s.Require().NoError(err)
	s.Equal(int64(2), total)
	s.Empty(past)

	_, _, err = s.repo.List(context.Background(), 0, 0)
	s.EqualError(err, "list users: limit and offset are invalid")
	_, _, err = s.repo.List(nil, 20, 0)
	s.EqualError(err, "list users: context is required")
}

func (s *UserRepositoryTestSuite) TestUpdate_Totp_SecretStoresTheSealedCredential() {
	userID := insertActiveUserRow(s.T())
	sealed, err := settings.Seal(facades.Crypt(), "totp-plaintext-marker")
	s.Require().NoError(err)

	s.Require().NoError(s.repo.UpdateTotpSecret(context.Background(), userID, sealed))

	var stored string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT secret FROM mfa_credentials WHERE subject_type = ? AND subject_id = ?`,
		models.MFASubjectUsers, userID,
	).Scan(&stored))
	if !settings.IsSealed(stored) || stored != sealed || strings.Contains(stored, "totp-plaintext-marker") {
		s.Fail("totp secret was not stored sealed on mfa_credentials")
	}
	var column int64
	s.Require().NoError(facades.Orm().Query().Raw(`
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'users'
		  AND column_name = 'totp_secret'`).Scan(&column))
	s.Zero(column)

	err = s.repo.UpdateTotpSecret(context.Background(), userID, "not-sealed-marker")
	s.Require().Error(err)
	s.NotContains(err.Error(), "not-sealed-marker")

	advanced, err := s.repo.AdvanceTotpCounter(context.Background(), userID, 40)
	s.Require().NoError(err)
	s.True(advanced)
	advanced, err = s.repo.AdvanceTotpCounter(context.Background(), userID, 40)
	s.Require().NoError(err)
	s.False(advanced, "login and withdrawal share the credential counter")

	_, err = facades.Orm().Query().Exec(`
		INSERT INTO mfa_credentials (
			id, subject_type, subject_id, secret, last_used_counter, created_at, updated_at
		) VALUES (?, ?, ?, 'enc:v1:platform-marker', 0, NOW(), NOW())`,
		uuid.New(), models.MFASubjectPlatformAdmins, uuid.New(),
	)
	s.Require().NoError(err)
	var subjects int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT count(DISTINCT subject_type) FROM mfa_credentials`,
	).Scan(&subjects))
	s.Equal(int64(2), subjects)
}

func (s *UserRepositoryTestSuite) stampCreatedAt(id uuid.UUID, at time.Time) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(`UPDATE users SET created_at = ? WHERE id = ?`, at, id)
	s.Require().NoError(err)
}
