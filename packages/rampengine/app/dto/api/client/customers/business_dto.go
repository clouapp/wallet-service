// Package customers holds the DTOs of the client's customers routes.
package customers

// CreateBusinessDTO is POST /api/customers: the document a business draft is
// opened for, as the user typed it. The service canonicalises it.
type CreateBusinessDTO struct {
	IncorporationCountry string
	TaxIDType            string
	TaxID                string
} //	@name	ClientCreateBusinessDTO
