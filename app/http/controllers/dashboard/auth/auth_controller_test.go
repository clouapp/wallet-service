package auth

import (
	"testing"

	authsvc "github.com/macrowallets/waas/app/services/auth"
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

func TestNew_Auth_ControllersRequireTheirServices(t *testing.T) {
	expectPanic(t, "dashboard auth controller: sign in is required", func() { NewAuthController(nil) })
	expectPanic(t, "dashboard password reset controller: users service is required", func() {
		NewPasswordController(nil, &authsvc.Credentials{})
	})
	expectPanic(t, "dashboard password reset controller: credentials are required", func() {
		NewPasswordController(&usersvc.Service{}, nil)
	})
}

func TestNew_Auth_ControllersKeepTheirServices(t *testing.T) {
	signIn := &authsvc.SignIn{}
	if ctrl := NewAuthController(signIn); ctrl.signIn != signIn {
		t.Fatal("auth controller did not keep the sign-in flows")
	}
	users, credentials := &usersvc.Service{}, &authsvc.Credentials{}
	if ctrl := NewPasswordController(users, credentials); ctrl.users != users || ctrl.credentials != credentials {
		t.Fatal("password controller did not keep its services")
	}
}
