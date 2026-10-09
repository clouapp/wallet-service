package models

import "testing"

func TestAPI_Token_ScopesAreChecked(t *testing.T) {
	grants := []string{APIPermWalletsRead, APIPermWebhooksRead}
	if !APITokenAllows(grants, APIPermWalletsRead) {
		t.Fatal("granted scope must pass")
	}
	if APITokenAllows(grants, APIPermWithdrawalsWrite) || APITokenAllows(nil, APIPermWalletsRead) || APITokenAllows(grants, "") {
		t.Fatal("missing, empty or unnamed scopes must fail closed")
	}

	if !RoleMayMintAPIPermissions(AccountRoleOwner, []string{APIPermWalletsWrite, APIPermWithdrawalsWrite}) {
		t.Fatal("owner holds every api scope")
	}
	if RoleMayMintAPIPermissions(AccountRoleAuditor, []string{APIPermWithdrawalsWrite}) ||
		RoleMayMintAPIPermissions(AccountRoleUser, []string{APIPermWalletsWrite}) ||
		RoleMayMintAPIPermissions(AccountRoleAdmin, []string{"wallets:admin"}) ||
		RoleMayMintAPIPermissions(AccountRoleOwner, nil) {
		t.Fatal("a token must stay inside the creator's scopes and the catalog")
	}
	if !RoleMayMintAPIPermissions(AccountRoleAuditor, []string{APIPermWalletsRead, APIPermTransactionsRead, APIPermWebhooksRead}) {
		t.Fatal("auditor may mint read scopes")
	}

	if !ClientIPAllowed("", "203.0.113.5") {
		t.Fatal("an empty cidr is unrestricted")
	}
	if !ClientIPAllowed("203.0.113.5", "203.0.113.5") || !ClientIPAllowed("10.0.0.0/8", "10.1.2.3") {
		t.Fatal("matching ip or cidr must pass")
	}
	if ClientIPAllowed("10.0.0.0/8", "11.0.0.1") || ClientIPAllowed("not-a-cidr", "10.0.0.1") {
		t.Fatal("outside or invalid cidr must fail")
	}
}
