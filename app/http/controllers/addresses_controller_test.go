package controllers_test

import (
	"testing"

	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/stretchr/testify/suite"

	"github.com/macromarkets/vault/tests/mocks"
)

type AddressesControllerTestSuite struct {
	authSuite
}

func TestAddressesControllerSuite(t *testing.T) {
	suite.Run(t, new(AddressesControllerTestSuite))
}

func (s *AddressesControllerTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *AddressesControllerTestSuite) createWallet(chain string) string {
	resp := s.SignedPost("/v1/wallets", `{"chain":"`+chain+`"}`)
	j, _ := resp.Json()
	return j["id"].(string)
}

func (s *AddressesControllerTestSuite) TestGenerateAddress_Success() {
	walletID := s.createWallet("eth")

	s.SignedPost("/v1/wallets/"+walletID+"/addresses",
		`{"external_user_id":"user_123","metadata":{"tier":"premium"}}`).
		AssertCreated().
		AssertJson(map[string]any{
			"external_user_id": "user_123",
			"chain":            "eth",
			"wallet_id":        walletID,
		}).
		AssertFluentJson(func(json contractstestinghttp.AssertableJSON) {
			json.Has("address").Has("derivation_index").Where("is_active", true)
		})
}

func (s *AddressesControllerTestSuite) TestGenerateAddress_MissingUserID() {
	walletID := s.createWallet("eth")
	s.SignedPost("/v1/wallets/"+walletID+"/addresses", `{}`).AssertBadRequest()
}

func (s *AddressesControllerTestSuite) TestGenerateAddress_MultipleForSameUser() {
	walletID := s.createWallet("eth")

	j1, _ := s.SignedPost("/v1/wallets/"+walletID+"/addresses", `{"external_user_id":"user_multi"}`).Json()
	addr1 := j1["address"].(string)

	j2, _ := s.SignedPost("/v1/wallets/"+walletID+"/addresses", `{"external_user_id":"user_multi"}`).Json()
	addr2 := j2["address"].(string)

	s.NotEqual(addr1, addr2)
}

func (s *AddressesControllerTestSuite) TestListWalletAddresses() {
	walletID := s.createWallet("eth")
	s.SignedPost("/v1/wallets/"+walletID+"/addresses", `{"external_user_id":"user1"}`)
	s.SignedPost("/v1/wallets/"+walletID+"/addresses", `{"external_user_id":"user2"}`)

	s.SignedGet("/v1/wallets/"+walletID+"/addresses").
		AssertOk().
		AssertFluentJson(func(json contractstestinghttp.AssertableJSON) {
			json.HasWithScope("data", 2, func(j contractstestinghttp.AssertableJSON) {
				j.Has("address").Has("external_user_id")
			})
		})
}

func (s *AddressesControllerTestSuite) TestLookupAddress_Success() {
	walletID := s.createWallet("eth")
	j, _ := s.SignedPost("/v1/wallets/"+walletID+"/addresses", `{"external_user_id":"lookup_user"}`).Json()
	address := j["address"].(string)

	s.SignedGet("/v1/addresses/"+address+"?chain=eth").
		AssertOk().AssertJson(map[string]any{
		"address":          address,
		"external_user_id": "lookup_user",
		"chain":            "eth",
	})
}

func (s *AddressesControllerTestSuite) TestLookupAddress_NotFound() {
	s.SignedGet("/v1/addresses/0xnonexistent?chain=eth").AssertNotFound()
}

func (s *AddressesControllerTestSuite) TestLookupAddress_MissingChain() {
	s.SignedGet("/v1/addresses/0xsomeaddress").AssertBadRequest()
}

func (s *AddressesControllerTestSuite) TestListUserAddresses() {
	walletID := s.createWallet("eth")
	s.SignedPost("/v1/wallets/"+walletID+"/addresses", `{"external_user_id":"target_user"}`)
	s.SignedPost("/v1/wallets/"+walletID+"/addresses", `{"external_user_id":"target_user"}`)
	s.SignedPost("/v1/wallets/"+walletID+"/addresses", `{"external_user_id":"other_user"}`)

	s.SignedGet("/v1/users/target_user/addresses").
		AssertOk().
		AssertFluentJson(func(json contractstestinghttp.AssertableJSON) {
			json.Count("data", 2).
				Each("data", func(j contractstestinghttp.AssertableJSON) {
					j.Where("external_user_id", "target_user")
				})
		})
}
