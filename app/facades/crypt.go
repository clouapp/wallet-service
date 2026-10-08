package facades

import (
	"errors"

	"github.com/goravel/framework/contracts/crypt"
	goravelfacades "github.com/goravel/framework/facades"
)

// Crypt returns the process cipher. Callers keep the same encrypt and decrypt calls.
func Crypt() crypt.Crypt {
	return goravelfacades.Crypt()
}

// ErrCryptUnavailable is what LateCrypt answers when the framework could not
// build the cipher, which happens when APP_KEY is not a valid AES key.
var ErrCryptUnavailable = errors.New("crypt is not available: APP_KEY is not a valid key")

// LateCrypt returns a cipher that looks the process cipher up when it is used,
// not when it is built. A constructor that only stores the cipher takes this one,
// so the process can still boot to run `artisan key:generate`, the one command
// that has to start while APP_KEY is empty.
func LateCrypt() crypt.Crypt {
	return lateCrypt{}
}

type lateCrypt struct{}

func (lateCrypt) EncryptString(value string) (string, error) {
	cipher := goravelfacades.Crypt()
	if cipher == nil {
		return "", ErrCryptUnavailable
	}
	return cipher.EncryptString(value)
}

func (lateCrypt) DecryptString(payload string) (string, error) {
	cipher := goravelfacades.Crypt()
	if cipher == nil {
		return "", ErrCryptUnavailable
	}
	return cipher.DecryptString(payload)
}
