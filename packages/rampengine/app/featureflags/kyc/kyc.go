// Package kyc declares the switches that open a KYC residence to the public
// only when an operator says so. app/services/client/kyc reads them.
package kyc

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// Definitions is this domain's part of the catalog.
func Definitions() []featureflags.Definition {
	return []featureflags.Definition{usaResidenceEnabled}
}

// ResidenceFlag is the switch that holds a KYC residence (alpha-3) shut until
// an operator opens it; ok is false for a residence no switch holds. The
// United States waits for every installed app to read an ssn for what it is
// (KYC USA spec R2).
func ResidenceFlag(alpha3 string) (flag string, ok bool) {
	switch alpha3 {
	case "USA":
		return USAResidenceEnabled, true
	}
	return "", false
}
