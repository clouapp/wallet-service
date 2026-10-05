package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

func TestInviteGrantForbiddenOmitsTheCause(t *testing.T) {
	cause := fmt.Errorf("insert account_invites: %w", accountsvc.ErrGrantRole)
	response := &recordingResponse{}
	inviteGrantForbidden(&recordingContext{base: context.Background(), response: response}, cause)

	if response.status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.status)
	}
	if bytes.Contains(response.raw, []byte("account_invites")) || bytes.Contains(response.raw, []byte(cause.Error())) {
		t.Fatal("response body contains the underlying error text")
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.raw, &body); err != nil {
		t.Fatal("response body is not the error envelope")
	}
	if body.Error.Code != responses.CodeForbidden || body.Error.Message != "forbidden" {
		t.Fatal("response envelope is not the fixed forbidden message")
	}
}
