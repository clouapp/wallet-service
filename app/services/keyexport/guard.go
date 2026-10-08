package keyexport

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
)

const (
	// ProductionConfirmationPhrase must be typed on the terminal to export in production.
	ProductionConfirmationPhrase = "EXPORTAR CHAVES PRIVADAS DE PRODUCAO"

	productionConfirmationPrompt = "APP_ENV é produção. Digite exatamente \"%s\" para continuar: "
)

var productionEnvironments = map[string]bool{"production": true, "prod": true}

var (
	ErrProductionRefused      = errors.New("refusing to export keys with APP_ENV=production; pass --allow-production and confirm on the terminal")
	ErrProductionNotConfirmed = errors.New("production export not confirmed: the confirmation phrase did not match")
)

// IsProductionEnvironment reports whether APP_ENV names production.
func IsProductionEnvironment(appEnv string) bool {
	return productionEnvironments[strings.ToLower(strings.TrimSpace(appEnv))]
}

// RequireEnvironmentAllowed lets every non-production environment through; in
// production it needs --allow-production and the confirmation phrase typed on the
// terminal.
func RequireEnvironmentAllowed(appEnv string, allowProduction bool, terminal Terminal) error {
	if !IsProductionEnvironment(appEnv) {
		return nil
	}
	if !allowProduction {
		return ErrProductionRefused
	}
	if terminal == nil {
		return ErrNoTerminal
	}
	typed, err := terminal.ReadLine(fmt.Sprintf(productionConfirmationPrompt, ProductionConfirmationPhrase))
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(typed)), []byte(ProductionConfirmationPhrase)) != 1 {
		return ErrProductionNotConfirmed
	}
	return nil
}
