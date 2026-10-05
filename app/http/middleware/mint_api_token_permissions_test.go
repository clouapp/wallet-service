package middleware

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
)

func TestMintAPITokenPermissionsParsesACatalogSubset(t *testing.T) {
	t.Parallel()

	permissions, apply := mintAPITokenPermissions([]byte(`{"name":"ci","permissions":["wallets.read","webhooks.write"]}`))
	if !apply || len(permissions) != 2 || permissions[0] != "wallets.read" || permissions[1] != "webhooks.write" {
		t.Fatalf("apply=%v permissions=%v", apply, permissions)
	}

	permissions, apply = mintAPITokenPermissions([]byte(`{"name":"plain"}`))
	if !apply || permissions != nil {
		t.Fatal("an omitted permissions field is an empty grant")
	}

	permissions, apply = mintAPITokenPermissions([]byte(`{"name":"empty","permissions":[]}`))
	if !apply || len(permissions) != 0 {
		t.Fatal("an empty permissions array is an empty grant")
	}

	if _, apply = mintAPITokenPermissions([]byte(`{"name":"bad","permissions":["wallets:read"]}`)); apply {
		t.Fatal("a name outside the catalog stays with validation")
	}
	if _, apply = mintAPITokenPermissions([]byte(`{"permissions":"wallets.read"}`)); apply {
		t.Fatal("a permissions value that is not an array stays with validation")
	}
	if _, apply = mintAPITokenPermissions([]byte(`not-json`)); apply {
		t.Fatal("a body that is not a JSON object stays with validation")
	}

	denied, apply := mintAPITokenPermissions([]byte(`{"name":"nope","permissions":["wallets.read","wallets.create"]}`))
	if !apply {
		t.Fatal("catalog names are decided by the mint policy")
	}
	decision := policies.MintAPITokenPermissions(models.AccountRoleUser, denied)
	if decision.Allowed() || decision.Message() != "cannot grant a permission you do not hold" {
		t.Fatalf("user mint allowed=%v", decision.Allowed())
	}
	if !policies.MintAPITokenPermissions(models.AccountRoleOwner, denied).Allowed() {
		t.Fatal("owner holds the catalog")
	}
	if !policies.MintAPITokenPermissions(models.AccountRoleAuditor, []string{"transactions.read", "webhooks.read"}).Allowed() {
		t.Fatal("auditor holds the read scopes")
	}
	if policies.MintAPITokenPermissions(models.AccountRoleAuditor, []string{"transactions.read", "sweep.execute"}).Allowed() {
		t.Fatal("auditor must not mint sweep.execute")
	}
}
