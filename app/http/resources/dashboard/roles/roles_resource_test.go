package roles_test

import (
	"encoding/json"
	"testing"

	"github.com/macrowallets/waas/app/http/resources/dashboard/roles"
	"github.com/macrowallets/waas/app/policies"
)

func TestRole_Catalogs_KeepTheirWire(t *testing.T) {
	t.Parallel()

	grants := policies.EffectiveRoleGrants()
	want, err := json.Marshal(struct {
		Roles []policies.RoleGrant `json:"roles"`
	}{grants})
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(roles.NewRoleList(grants))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("roles wire changed\n got %s\nwant %s", got, want)
	}

	permissions := policies.AccountPermissionCatalog()
	want, err = json.Marshal(struct {
		Permissions []string `json:"permissions"`
	}{permissions})
	if err != nil {
		t.Fatal(err)
	}
	got, err = json.Marshal(roles.NewPermissionCatalog(permissions))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("permissions wire changed\n got %s\nwant %s", got, want)
	}
}
