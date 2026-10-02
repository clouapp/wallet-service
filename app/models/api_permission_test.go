package models

import "testing"

func TestAPITokenScopesAreChecked(t *testing.T) {
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

	if _, err := AuthorizeTokenSpend("{}", "eth", "1", 0); err != nil {
		t.Fatal(err)
	}
	next, err := AuthorizeTokenSpend(`{"daily_usd":10}`, "usdt", "4", 5)
	if err != nil || next != 9 {
		t.Fatalf("next=%v err=%v", next, err)
	}
	if _, err := AuthorizeTokenSpend(`{"daily_usd":10}`, "usdt", "6", 5); err == nil {
		t.Fatal("spend over the daily cap must fail")
	}
	if _, err := AuthorizeTokenSpend(`{"daily_usd":10}`, "eth", "1", 0); err == nil {
		t.Fatal("a usd cap must not ignore an unpriced asset")
	}
}
