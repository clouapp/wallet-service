package pixkeys

import (
	"testing"

	"github.com/stretchr/testify/assert"

	_ "github.com/macrowallets/waas/config"
)

func TestHashKey_TypeCasingDoesNotChangeHash(t *testing.T) {
	want := HashKey("cpf", "390.533.447-05")
	got := HashKey("CPF", "39053344705")
	assert.Equal(t, want, got)
}

func TestHashKey_SameNormalizedValueProducesSameHash(t *testing.T) {
	a := HashKey("cpf", "39053344705")
	b := HashKey("cpf", "390.533.447-05")
	assert.Equal(t, a, b)
	assert.Len(t, a, 64)
}

func TestHashKey_PhoneEquivalentFormatsProduceSameHash(t *testing.T) {
	a := HashKey("phone", "+5511999999999")
	b := HashKey("phone", "(11) 99999-9999")
	assert.Equal(t, a, b)
}

func TestHashKey_DifferentKeysProduceDifferentHashes(t *testing.T) {
	a := HashKey("cpf", "39053344705")
	b := HashKey("cpf", "39053344706")
	assert.NotEqual(t, a, b)
}

func TestHashKey_PhoneAndCPFWithSameDigitsDoNotCollide(t *testing.T) {
	cpfHash := HashKey("cpf", "11999999999")
	phoneHash := HashKey("phone", "11999999999")
	assert.NotEqual(t, cpfHash, phoneHash)
}

// Both providers register a CPF or CNPJ key with the key as the holder
// document, whatever the client sent alongside it.

func TestDestinationDocumentHash_EmptyForAKeyThatIsADocument(t *testing.T) {
	for _, keyType := range []string{"cpf", "cnpj", "CPF", "Cnpj"} {
		assert.Empty(t, DestinationDocumentHash(keyType, "39053344705"), keyType)
		assert.Empty(t, DestinationDocumentHash(keyType, ""), keyType)
	}
}

// Every other key type carries the document separately, and the same key
// under two documents is two registrations.

func TestDestinationDocumentHash_IdentifiesTheHolderOfAnyOtherKey(t *testing.T) {
	want := DestinationDocumentHash("email", "39053344705")
	assert.Len(t, want, 64)
	for _, keyType := range []string{"phone", "evp"} {
		assert.Equal(t, want, DestinationDocumentHash(keyType, "39053344705"), keyType)
	}
	assert.NotEqual(t, want, DestinationDocumentHash("email", "11122233396"))
}

// A document is its digits: the holder of "390.533.447-05" and of "39053344705"
// is one person, and one destination, however the client wrote it. Ozav's
// tax_id takes the same digits-only form (digits.Only).
func TestDestinationDocumentHash_IsTheSameHoweverTheDocumentIsWritten(t *testing.T) {
	cases := []struct {
		name     string
		spelling []string
	}{
		{"cpf", []string{"39053344705", "390.533.447-05", " 390 533 447 05 "}},
		{"cnpj", []string{"11222333000181", "11.222.333/0001-81"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := DestinationDocumentHash("email", c.spelling[0])
			for _, spelling := range c.spelling[1:] {
				assert.Equal(t, want, DestinationDocumentHash("email", spelling), spelling)
			}
		})
	}
	assert.NotEqual(t, DestinationDocumentHash("email", "390.533.447-05"), DestinationDocumentHash("email", "111.222.333-96"))
}

// Two companies whose alphanumeric CNPJs share every digit are two keys and
// two holders; one company's CNPJ is one, however it was written.
func TestHashes_TellAlphanumericCNPJsApartByTheirLetters(t *testing.T) {
	assert.Equal(t, HashKey("cnpj", "12ABC34501DE35"), HashKey("cnpj", "12.abc.345/01de-35"))
	assert.NotEqual(t, HashKey("cnpj", "12ABC34501DE35"), HashKey("cnpj", "12ABN34501DE35"))

	assert.Equal(t, DestinationDocumentHash("email", "12ABC34501DE35"), DestinationDocumentHash("email", "12abc34501de35"))
	assert.NotEqual(t, DestinationDocumentHash("email", "12ABC34501DE35"), DestinationDocumentHash("email", "12ABN34501DE35"))
}
