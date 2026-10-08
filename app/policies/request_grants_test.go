package policies

import (
	"context"
	"testing"
)

func TestRequest_Grants_UseTheStoredRole(t *testing.T) {
	ctx := context.WithValue(context.Background(), RequestGrantsKey(), AttachRequestGrants(roleUser))
	wallet, ok := WalletRequestGrants(ctx)
	if !ok || !Can(wallet, PermAddressesCreate) || Can(wallet, PermWithdrawalsCreate) || Can(wallet, PermSweepExecute) || Can(wallet, PermWalletsCreate) {
		t.Fatalf("user wallet grants = %v ok=%v", wallet, ok)
	}
	account, ok := AccountGrants(ctx)
	if !ok || Can(account, PermWithdrawalsCreate) || Can(account, PermUsersRead) || Can(account, PermAccountWrite) || Can(account, PermTokensRead) {
		t.Fatalf("user account grants = %v ok=%v", account, ok)
	}

	second := context.WithValue(context.Background(), RequestGrantsKey(), AttachRequestGrants(roleUser))
	if again, _ := AccountGrants(second); Can(again, PermAccountWrite) {
		t.Fatal("user must still be refused on the next request")
	}

	empty := context.WithValue(context.Background(), RequestGrantsKey(), emptyRequestGrants())
	if _, ok := AccountGrants(empty); ok {
		t.Fatal("an unloaded grant stays unloaded")
	}
	if _, ok := WalletRequestGrants(empty); ok {
		t.Fatal("an unloaded wallet grant stays unloaded")
	}
}
