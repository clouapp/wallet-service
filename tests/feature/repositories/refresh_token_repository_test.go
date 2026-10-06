package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type RefreshTokenRepositoryTestSuite struct {
	suite.Suite
	repo     *repositories.RefreshTokenRepository
	userRepo *repositories.UserRepository
}

func TestRefresh_Token_RepositorySuite(t *testing.T) {
	suite.Run(t, new(RefreshTokenRepositoryTestSuite))
}

func (s *RefreshTokenRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewRefreshTokenRepository(nil)
	s.userRepo = repositories.NewUserRepository(nil)
}

func (s *RefreshTokenRepositoryTestSuite) createUser() uuid.UUID {
	u := &models.User{ID: uuid.New(), Email: uuid.NewString() + "@test.com", PasswordHash: "h", Status: "active"}
	s.Require().NoError(s.userRepo.Create(context.Background(), u))
	return u.ID
}

func (s *RefreshTokenRepositoryTestSuite) TestRefreshTokenRepository_Create_Success() {
	userID := s.createUser()
	rt := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: "hash123",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	err := s.repo.Create(context.Background(), rt)
	s.NoError(err)
}

func (s *RefreshTokenRepositoryTestSuite) TestFind_Valid_Tokens() {
	userID := s.createUser()

	valid := &models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: "valid", ExpiresAt: time.Now().Add(24 * time.Hour)}
	s.Require().NoError(s.repo.Create(context.Background(), valid))

	expired := &models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: "expired", ExpiresAt: time.Now().Add(-1 * time.Hour)}
	s.Require().NoError(s.repo.Create(context.Background(), expired))

	revoked := &models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: "revoked", ExpiresAt: time.Now().Add(24 * time.Hour)}
	s.Require().NoError(s.repo.Create(context.Background(), revoked))
	now := time.Now()
	facades.Orm().Query().Model(revoked).Where("id = ?", revoked.ID).Update("revoked_at", now)

	tokens, err := s.repo.FindValidTokens(context.Background())
	s.Require().NoError(err)
	s.Require().Len(tokens, 1)
	s.Equal(valid.ID, tokens[0].ID)
}

func (s *RefreshTokenRepositoryTestSuite) TestRevoke_If_Active() {
	userID := insertActiveUserRow(s.T())
	rt := &models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: "tok", ExpiresAt: time.Now().Add(24 * time.Hour)}
	s.Require().NoError(s.repo.Create(context.Background(), rt))

	revoked, err := s.repo.RevokeIfActive(context.Background(), rt.ID)
	s.Require().NoError(err)
	s.True(revoked)

	var check models.RefreshToken
	s.Require().NoError(facades.Orm().Query().Where("id = ?", rt.ID).First(&check))
	s.NotNil(check.RevokedAt)
}

func (s *RefreshTokenRepositoryTestSuite) TestRevoke_IfActive_SecondRevocationReportsFalse() {
	userID := insertActiveUserRow(s.T())
	rt := &models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: "tok", ExpiresAt: time.Now().Add(24 * time.Hour)}
	s.Require().NoError(s.repo.Create(context.Background(), rt))

	first, err := s.repo.RevokeIfActive(context.Background(), rt.ID)
	s.Require().NoError(err)
	second, err := s.repo.RevokeIfActive(context.Background(), rt.ID)
	s.Require().NoError(err)

	s.True(first)
	s.False(second, "a token can only be rotated once")
}

func (s *RefreshTokenRepositoryTestSuite) TestRevoke_All_ForUser() {
	userID := s.createUser()
	rt1 := &models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: "t1", ExpiresAt: time.Now().Add(24 * time.Hour)}
	rt2 := &models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: "t2", ExpiresAt: time.Now().Add(24 * time.Hour)}
	s.Require().NoError(s.repo.Create(context.Background(), rt1))
	s.Require().NoError(s.repo.Create(context.Background(), rt2))

	err := s.repo.RevokeAllForUser(context.Background(), userID)
	s.Require().NoError(err)

	tokens, err := s.repo.FindValidTokens(context.Background())
	s.Require().NoError(err)
	s.Len(tokens, 0)
}
