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

type PasswordResetTokenRepositoryTestSuite struct {
	suite.Suite
	repo     *repositories.PasswordResetTokenRepository
	userRepo *repositories.UserRepository
}

func TestPasswordResetTokenRepositorySuite(t *testing.T) {
	suite.Run(t, new(PasswordResetTokenRepositoryTestSuite))
}

func (s *PasswordResetTokenRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewPasswordResetTokenRepository(nil)
	s.userRepo = repositories.NewUserRepository(nil)
}

func (s *PasswordResetTokenRepositoryTestSuite) createUser() uuid.UUID {
	u := &models.User{ID: uuid.New(), Email: uuid.NewString() + "@test.com", PasswordHash: "h", Status: "active"}
	s.Require().NoError(s.userRepo.Create(context.Background(), u))
	return u.ID
}

func (s *PasswordResetTokenRepositoryTestSuite) TestCreate_Success() {
	userID := s.createUser()
	prt := &models.PasswordResetToken{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: "hash",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	err := s.repo.Create(context.Background(), prt)
	s.NoError(err)
}

func (s *PasswordResetTokenRepositoryTestSuite) TestFindValidTokens() {
	userID := s.createUser()

	valid := &models.PasswordResetToken{ID: uuid.New(), UserID: userID, TokenHash: "valid", ExpiresAt: time.Now().Add(1 * time.Hour)}
	s.Require().NoError(s.repo.Create(context.Background(), valid))

	expired := &models.PasswordResetToken{ID: uuid.New(), UserID: userID, TokenHash: "expired", ExpiresAt: time.Now().Add(-1 * time.Hour)}
	s.Require().NoError(s.repo.Create(context.Background(), expired))

	used := &models.PasswordResetToken{ID: uuid.New(), UserID: userID, TokenHash: "used", ExpiresAt: time.Now().Add(1 * time.Hour)}
	s.Require().NoError(s.repo.Create(context.Background(), used))
	now := time.Now()
	facades.Orm().Query().Model(used).Where("id = ?", used.ID).Update("used_at", now)

	tokens, err := s.repo.FindValidTokens(context.Background())
	s.NoError(err)
	s.Len(tokens, 1)
	s.Equal(valid.ID, tokens[0].ID)
}

func (s *PasswordResetTokenRepositoryTestSuite) TestMarkUsed() {
	userID := s.createUser()
	prt := &models.PasswordResetToken{ID: uuid.New(), UserID: userID, TokenHash: "tok", ExpiresAt: time.Now().Add(1 * time.Hour)}
	s.Require().NoError(s.repo.Create(context.Background(), prt))

	err := s.repo.MarkUsed(context.Background(), prt.ID)
	s.NoError(err)

	var check models.PasswordResetToken
	facades.Orm().Query().Where("id = ?", prt.ID).First(&check)
	s.NotNil(check.UsedAt)
}
