package controllers_test

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type ChainsControllerTestSuite struct {
	authSuite
}

func TestChainsControllerSuite(t *testing.T) {
	suite.Run(t, new(ChainsControllerTestSuite))
}

// TestListChains_Unauthenticated returns 401 without a Bearer token.
// /v1/chains uses SessionAuth and AccountHeader (JWT + X-Account-Id).
func (s *ChainsControllerTestSuite) TestListChains_Unauthenticated() {
	resp, err := s.Http(s.T()).Get("/v1/chains")
	s.Require().NoError(err)
	resp.AssertStatus(401)
}
