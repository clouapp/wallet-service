package middleware

import "testing"

func TestIs_TOTP_EnrollmentPath(t *testing.T) {
	t.Parallel()

	open := []string{
		"/v1/users/me/totp",
		"/v1/users/me/totp/",
		"/v1/users/me/totp/setup",
		"/v1/users/me/totp/verify",
	}
	closed := []string{
		"",
		"/v1/users/me",
		"/v1/users/me/password",
		"/v1/users/me/totp-extra",
		"/v1/accounts/11111111-1111-4111-8111-111111111111",
		"/v1/auth/login",
		"/v1/auth/2fa/verify",
	}
	for _, path := range open {
		if !isTOTPEnrollmentPath(path) {
			t.Errorf("%s should stay open for enrollment", path)
		}
	}
	for _, path := range closed {
		if isTOTPEnrollmentPath(path) {
			t.Errorf("%s should not be treated as enrollment", path)
		}
	}
}
