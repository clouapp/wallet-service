package sweep

// Box is the typed container key for Service. A nil interface cannot be a
// Goravel binding key: every nil interface compares equal.
type Box struct {
	Service Service
}
