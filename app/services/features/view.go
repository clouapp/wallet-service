package features

// Flag is one account switch on the wire. Enabled is the stored row, or
// false when the account has no row for that key.
type Flag struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

// List is every account flag, in key order, including the ones that are off.
type List struct {
	Features []Flag `json:"features"`
}
