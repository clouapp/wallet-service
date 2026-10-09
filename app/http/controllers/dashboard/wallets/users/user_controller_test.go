package users

import (
	"testing"

	"github.com/macrowallets/waas/app/services/walletrecords"
)

func TestNew_UserController_RequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name        string
		members     *walletrecords.Members
		memberships *walletrecords.Memberships
		panic       string
	}{
		{"wallet users", nil, &walletrecords.Memberships{}, "dashboard wallet users controller: wallet users service is required"},
		{"wallet memberships", &walletrecords.Members{}, nil, "dashboard wallet users controller: wallet memberships are required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewUserController(tc.members, tc.memberships)
			t.Fatal("expected a panic")
		})
	}
}

func TestNew_UserController_KeepsItsDependencies(t *testing.T) {
	members, memberships := &walletrecords.Members{}, &walletrecords.Memberships{}

	ctrl := NewUserController(members, memberships)

	if ctrl.members != members || ctrl.memberships != memberships {
		t.Fatal("user controller did not keep its dependencies")
	}
}
