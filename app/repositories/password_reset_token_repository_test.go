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

type PasswordResetTokenRepositoryTestSuite struct {
	suite.Suite
	repo     repositories.PasswordResetTokenRepository
	userRepo repositories.UserRepository
}

func TestPasswordResetTokenRepositorySuite(t *testing.T) {
	suite.Run(t, new(PasswordResetTokenRepositoryTestSuite))
}

func (s *PasswordResetTokenRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewPasswordResetTokenRepository()
	s.userRepo = repositories.NewUserRepository()
}

func (s *PasswordResetTokenRepositoryTestSuite) createUser() uuid.UUID {
	u := &models.User{ID: uuid.New(), Email: uuid.NewString() + "@test.com", PasswordHash: "h", Status: "active"}
	s.Require().NoError(s.userRepo.Create(u))
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
	err := s.repo.Create(prt)
	s.NoError(err)
}

func (s *PasswordResetTokenRepositoryTestSuite) TestFindValidTokens() {
	userID := s.createUser()

	valid := &models.PasswordResetToken{ID: uuid.New(), UserID: userID, TokenHash: "valid", ExpiresAt: time.Now().Add(1 * time.Hour)}
	s.Require().NoError(s.repo.Create(valid))

	expired := &models.PasswordResetToken{ID: uuid.New(), UserID: userID, TokenHash: "expired", ExpiresAt: time.Now().Add(-1 * time.Hour)}
	s.Require().NoError(s.repo.Create(expired))

	used := &models.PasswordResetToken{ID: uuid.New(), UserID: userID, TokenHash: "used", ExpiresAt: time.Now().Add(1 * time.Hour)}
	s.Require().NoError(s.repo.Create(used))
	now := time.Now()
	facades.Orm().Query().Model(used).Where("id = ?", used.ID).Update("used_at", now)

	tokens, err := s.repo.FindValidTokens()
	s.NoError(err)
	s.Len(tokens, 1)
	s.Equal(valid.ID, tokens[0].ID)
}

func (s *PasswordResetTokenRepositoryTestSuite) TestMarkUsed() {
	userID := s.createUser()
	prt := &models.PasswordResetToken{ID: uuid.New(), UserID: userID, TokenHash: "tok", ExpiresAt: time.Now().Add(1 * time.Hour)}
	s.Require().NoError(s.repo.Create(prt))

	err := s.repo.MarkUsed(prt.ID)
	s.NoError(err)

	var check models.PasswordResetToken
	facades.Orm().Query().Where("id = ?", prt.ID).First(&check)
	s.NotNil(check.UsedAt)
}
