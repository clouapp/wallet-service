package repositories_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type RefreshTokenRepositoryTestSuite struct {
	suite.Suite
	repo     repositories.RefreshTokenRepository
	userRepo repositories.UserRepository
}

func TestRefreshTokenRepositorySuite(t *testing.T) {
	suite.Run(t, new(RefreshTokenRepositoryTestSuite))
}

func (s *RefreshTokenRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewRefreshTokenRepository()
	s.userRepo = repositories.NewUserRepository()
}

func (s *RefreshTokenRepositoryTestSuite) createUser() uuid.UUID {
	u := &models.User{ID: uuid.New(), Email: uuid.NewString() + "@test.com", PasswordHash: "h", Status: "active"}
	s.Require().NoError(s.userRepo.Create(u))
	return u.ID
}

func (s *RefreshTokenRepositoryTestSuite) TestCreate_Success() {
	userID := s.createUser()
	rt := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: "hash123",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	err := s.repo.Create(rt)
	s.NoError(err)
}

func (s *RefreshTokenRepositoryTestSuite) TestFindValidTokens() {
	userID := s.createUser()

	valid := &models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: "valid", ExpiresAt: time.Now().Add(24 * time.Hour)}
	s.Require().NoError(s.repo.Create(valid))

	expired := &models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: "expired", ExpiresAt: time.Now().Add(-1 * time.Hour)}
	s.Require().NoError(s.repo.Create(expired))

	revoked := &models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: "revoked", ExpiresAt: time.Now().Add(24 * time.Hour)}
	s.Require().NoError(s.repo.Create(revoked))
	now := time.Now()
	facades.Orm().Query().Model(revoked).Where("id = ?", revoked.ID).Update("revoked_at", now)

	tokens, err := s.repo.FindValidTokens()
	s.NoError(err)
	s.Len(tokens, 1)
	s.Equal(valid.ID, tokens[0].ID)
}

func (s *RefreshTokenRepositoryTestSuite) TestRevokeByID() {
	userID := s.createUser()
	rt := &models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: "tok", ExpiresAt: time.Now().Add(24 * time.Hour)}
	s.Require().NoError(s.repo.Create(rt))

	err := s.repo.RevokeByID(rt.ID)
	s.NoError(err)

	var check models.RefreshToken
	facades.Orm().Query().Where("id = ?", rt.ID).First(&check)
	s.NotNil(check.RevokedAt)
}

func (s *RefreshTokenRepositoryTestSuite) TestRevokeAllForUser() {
	userID := s.createUser()
	rt1 := &models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: "t1", ExpiresAt: time.Now().Add(24 * time.Hour)}
	rt2 := &models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: "t2", ExpiresAt: time.Now().Add(24 * time.Hour)}
	s.Require().NoError(s.repo.Create(rt1))
	s.Require().NoError(s.repo.Create(rt2))

	err := s.repo.RevokeAllForUser(userID)
	s.NoError(err)

	tokens, err := s.repo.FindValidTokens()
	s.NoError(err)
	s.Len(tokens, 0)
}
