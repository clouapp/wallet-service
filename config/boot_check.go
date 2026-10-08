package config

import (
	"fmt"
	"strings"
)

// appKeyLength is the AES-256 key the Goravel cipher is built with. The
// framework also accepts 16 and 24 bytes; this service seals everything with
// one length so a shorter key is a mistake, not a choice.
const appKeyLength = 32

// jwtSecretMinLength is where a JWT signing secret stops being a guessable
// word. A shorter one boots with a warning.
const jwtSecretMinLength = 32

// ValidateSecrets checks the two secrets the process cannot run without: the
// APP_KEY the cipher is built from and the JWT_SECRET sessions are signed with.
// An empty or wrongly sized one is an error (golang-jwt signs and verifies with
// an empty key, so an empty JWT_SECRET would forge any session). A short
// JWT_SECRET is only a warning. The messages never repeat a secret.
func ValidateSecrets(appKey, jwtSecret string) (warnings []string, err error) {
	var problems []string
	if len(appKey) != appKeyLength {
		problems = append(problems, fmt.Sprintf(
			"APP_KEY must be exactly %d bytes (got %d); generate one with: go run . --env=.env.dev artisan key:generate",
			appKeyLength, len(appKey)))
	}
	switch {
	case strings.TrimSpace(jwtSecret) == "":
		problems = append(problems,
			"JWT_SECRET is empty; generate one with: go run . --env=.env.dev artisan jwt:secret")
	case len(jwtSecret) < jwtSecretMinLength:
		warnings = append(warnings, fmt.Sprintf(
			"JWT_SECRET is %d characters; use at least %d (go run . --env=.env.dev artisan jwt:secret)",
			len(jwtSecret), jwtSecretMinLength))
	}
	if len(problems) > 0 {
		return warnings, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return warnings, nil
}

// IsKeyGenerationCommand reports whether args (os.Args without the program
// name) run `artisan key:generate` or `artisan jwt:secret`, the commands that
// create the secrets ValidateSecrets demands and so must start without them.
func IsKeyGenerationCommand(args []string) bool {
	for i, arg := range args {
		if arg != "artisan" {
			continue
		}
		for _, next := range args[i+1:] {
			if strings.HasPrefix(next, "-") {
				continue
			}
			return next == "key:generate" || next == "jwt:secret"
		}
		return false
	}
	return false
}
