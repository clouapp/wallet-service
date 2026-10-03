package features

// Flag is one switch on the wire. Enabled is the stored row, or the catalog
// default when that scope has no row for the key.
type Flag struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

// List is every account flag, in key order, including the ones that are off.
type List struct {
	Features []Flag `json:"features"`
}
