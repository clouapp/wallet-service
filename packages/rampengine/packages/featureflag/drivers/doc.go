// Package drivers holds the stores behind contracts.Driver: Memory, Redis,
// Database and Cached, which layers one of them in front of another. Each
// stores already-resolved values, JSON-encoded the same way (codec.go), and
// knows nothing about resolvers or scopes beyond the key it is handed.
package drivers
