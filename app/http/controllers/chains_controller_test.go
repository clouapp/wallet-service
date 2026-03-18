package controllers_test

import (
	"testing"

	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/stretchr/testify/suite"
)

type ChainsControllerTestSuite struct {
	authSuite
}

func TestChainsControllerSuite(t *testing.T) {
	suite.Run(t, new(ChainsControllerTestSuite))
}

func (s *ChainsControllerTestSuite) TestListChains() {
	s.SignedGet("/v1/chains").
		AssertOk().
		AssertFluentJson(func(json contractstestinghttp.AssertableJSON) {
			json.Has("data").
				Each("data", func(j contractstestinghttp.AssertableJSON) {
					j.Has("id").Has("name").Has("native_asset").Has("required_confirmations")
				})
		})
}

func (s *ChainsControllerTestSuite) TestListChains_ContainsExpectedChains() {
	resp := s.SignedGet("/v1/chains")

	j, err := resp.Json()
	s.Nil(err)

	data := j["data"].([]interface{})
	s.True(len(data) > 0, "should have at least one chain")

	first := data[0].(map[string]interface{})
	s.NotEmpty(first["id"])
	s.NotEmpty(first["name"])
	s.NotEmpty(first["native_asset"])
}
