// Package settings is the account settings registry. The settings table
// stores values only. Type, label, help, default and whether a value is a
// secret are declared here, one group per save.
package settings

import (
	"slices"
	"strings"
)

// Scope is where a group's rows live.
type Scope string

const (
	// ScopeAccount is one row set per account.
	ScopeAccount Scope = "account"
	// ScopePlatform is one row set for the install. Platform groups are not
	// served on the account dashboard.
	ScopePlatform Scope = "platform"
)

// ManagedBy is who may write an account-scoped group.
type ManagedBy string

const (
	// ManagedByAccount lets owner and admin write the group.
	ManagedByAccount ManagedBy = "account"
	// ManagedByPlatform keeps the group readable by the account and writable
	// only by platform staff, which this audience does not have yet.
	ManagedByPlatform ManagedBy = "platform"
)

// Type is how a value is cast and how a form renders it.
type Type string

const (
	TypeString     Type = "string"
	TypeInt        Type = "int"
	TypeBool       Type = "bool"
	TypeDecimal    Type = "decimal"
	TypeStringList Type = "string_list"
	// TypeBigInt is an on-chain raw amount: a base-10 string, never a JSON
	// number. A negative value is refused.
	TypeBigInt Type = "bigint"
)

// Definition is one known setting.
type Definition struct {
	Key    string
	Label  string
	Help   string
	Type   Type
	Secret bool
	// Destination is a field the group's secrets are sent to. Changing it
	// requires the secret again; a blank secret does not keep the old one.
	Destination bool
	Options     []string
	Default     func() any
}

// Group is one save. Section and Block are navigation only.
type Group struct {
	Name      string
	Scope     Scope
	ManagedBy ManagedBy
	// Inherits names the platform group whose stored value sits between an
	// account row and this group's default. A missing parent is skipped.
	Inherits         string
	Settings         []Definition
	Section          string
	Block            string
	ViewPermission   string
	UpdatePermission string
	// CredentialGroups lists keys that must be present together. A pair is
	// judged by presence in the stored row or this write, because a secret
	// is left out of the map group validation sees.
	CredentialGroups [][]string
	Validate         func(effective map[string]string) error
}

// SectionName is the page this group belongs to.
func (g Group) SectionName() string {
	if g.Section == "" {
		return g.Name
	}
	return g.Section
}

// Registry returns the catalog. Callers must not modify it.
func Registry() []Group {
	return catalog()
}

func catalog() []Group {
	return slices.Clip(append(accountGroups(), platformGroups()...))
}

// GroupsInSection returns every catalog group on one page, in catalog order.
// An unknown or blank section is an empty slice. Scope is not filtered here.
func GroupsInSection(section string) []Group {
	section = strings.TrimSpace(section)
	if section == "" {
		return nil
	}
	var groups []Group
	for _, group := range Registry() {
		if group.SectionName() == section {
			groups = append(groups, group)
		}
	}
	return groups
}

// FindGroup resolves a group by name.
func FindGroup(name string) (Group, bool) {
	for _, group := range Registry() {
		if group.Name == name {
			return group, true
		}
	}
	return Group{}, false
}

// SecretPairs lists (group, key) whose stored text is a secret. The activity
// log records valueSet for these pairs and never the value.
func SecretPairs() [][]string {
	seen := map[string]struct{}{}
	var pairs [][]string
	for _, group := range Registry() {
		for _, definition := range group.Settings {
			if !definition.Secret {
				continue
			}
			token := group.Name + "\x00" + definition.Key
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			pairs = append(pairs, []string{group.Name, definition.Key})
		}
	}
	slices.SortFunc(pairs, func(a, b []string) int {
		if c := strings.Compare(a[0], b[0]); c != 0 {
			return c
		}
		return strings.Compare(a[1], b[1])
	})
	return pairs
}

// Find resolves one definition inside a group.
func Find(group, key string) (Definition, bool) {
	declared, ok := FindGroup(group)
	if !ok {
		return Definition{}, false
	}
	for _, definition := range declared.Settings {
		if definition.Key == key {
			return definition, true
		}
	}
	return Definition{}, false
}
