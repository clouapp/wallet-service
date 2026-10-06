package mpcshare

// DiscardPassphrase drops a passphrase after the operation that needed it.
// The value is not stored, compared, or logged here.
func DiscardPassphrase(value *string) {
	if value == nil {
		return
	}
	*value = ""
}
