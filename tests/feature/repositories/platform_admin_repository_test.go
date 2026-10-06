package repositories_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type PlatformAdminRepositoryTestSuite struct {
	suite.Suite
	repo     *repositories.PlatformAdminRepository
	userRepo *repositories.UserRepository
}

func TestPlatformAdminRepositorySuite(t *testing.T) {
	suite.Run(t, new(PlatformAdminRepositoryTestSuite))
}

func (s *PlatformAdminRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewPlatformAdminRepository(nil)
	s.userRepo = repositories.NewUserRepository(nil)
}

func (s *PlatformAdminRepositoryTestSuite) TestContainsIsFalseUntilTheUserRowExistsAndRejectsADuplicate() {
	ctx := context.Background()
	userID := s.user()

	found, err := s.repo.Contains(ctx, userID)
	s.Require().NoError(err)
	s.False(found)

	_, err = facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)

	found, err = s.repo.Contains(ctx, userID)
	s.Require().NoError(err)
	s.True(found)

	_, err = facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Error(err)

	_, err = s.repo.Contains(ctx, uuid.Nil)
	s.Error(err)
}

func (s *PlatformAdminRepositoryTestSuite) user() uuid.UUID {
	s.T().Helper()
	user := &models.User{
		ID:           uuid.New(),
		Email:        "admin-" + uuid.NewString()[:8] + "@example.com",
		PasswordHash: "hashed",
		Status:       "active",
	}
	s.Require().NoError(s.userRepo.Create(context.Background(), user))
	return user.ID
}
