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
	Validate         func(effective map[string]string) error
}

// SectionName is the page this group belongs to.
func (g Group) SectionName() string {
	if g.Section == "" {
		return g.Name
	}
	return g.Section
}

var override []Group

// Registry returns the catalog. Callers must not modify it.
func Registry() []Group {
	if override != nil {
		return override
	}
	return catalog()
}

// UseForTest replaces the catalog until the returned function runs.
func UseForTest(groups []Group) func() {
	previous := override
	override = groups
	return func() { override = previous }
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
