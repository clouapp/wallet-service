package models

// Customer types: who holds the money, a person or a company. They are the
// providers' own words for a customer (Bridge's and BlindPay's type), which
// is why a provider adapter sends them as they are.
const (
	CustomerTypeIndividual = "individual"
	CustomerTypeBusiness   = "business"
)

// CustomerTypes is every customer type, in the order a report lists them.
var CustomerTypes = []string{CustomerTypeIndividual, CustomerTypeBusiness}
