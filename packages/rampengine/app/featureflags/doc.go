// Package featureflags is the vocabulary the application declares its feature
// flags in: a Definition, with the metadata the admin UI renders — a human
// label, a help text, the scope types it may be toggled for — and the scope
// types themselves.
//
// The flags live one subpackage per domain (admin, ramp, requestlog), one file
// per flag, and catalog lists them and registers them on the manager at boot.
// A new flag is a new file in its domain plus one line in that domain's
// Definitions.
package featureflags
