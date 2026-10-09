package users

import (
	"testing"

	authsvc "github.com/macrowallets/waas/app/services/auth"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

func expectPanic(t *testing.T, want string, build func()) {
	t.Helper()
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("panic = %v, want %q", got, want)
		}
	}()
	build()
	t.Fatal("expected a panic")
}

func TestNew_User_ControllersRequireTheirServices(t *testing.T) {
	expectPanic(t, "dashboard user account controller: account service is required", func() { NewAccountController(nil) })
	expectPanic(t, "dashboard password controller: credentials are required", func() { NewPasswordController(nil) })
	expectPanic(t, "dashboard totp controller: totp enrollment is required", func() { NewTotpController(nil) })
	expectPanic(t, "dashboard profile controller: users service is required", func() { NewProfileController(nil, &featuressvc.Service{}) })
	expectPanic(t, "dashboard profile controller: feature flags are required", func() { NewProfileController(&usersvc.Service{}, nil) })
}

func TestNew_User_ControllersKeepTheirServices(t *testing.T) {
	users, features := &usersvc.Service{}, &featuressvc.Service{}
	if ctrl := NewProfileController(users, features); ctrl.users != users || ctrl.features != features {
		t.Fatal("profile controller did not keep its services")
	}
	credentials := &authsvc.Credentials{}
	if ctrl := NewPasswordController(credentials); ctrl.credentials != credentials {
		t.Fatal("password controller did not keep the credentials")
	}
	totp := &authsvc.TOTPEnrollment{}
	if ctrl := NewTotpController(totp); ctrl.totp != totp {
		t.Fatal("totp controller did not keep the enrollment")
	}
}
