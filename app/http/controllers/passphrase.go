package controllers

import "github.com/macrowallets/waas/pkg/mpcshare"

// DiscardPassphrase drops a form passphrase after the service call returns.
func DiscardPassphrase(value *string) {
	mpcshare.DiscardPassphrase(value)
}
