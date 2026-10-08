package settings

import (
	"encoding/json"
	"time"
)

// Field is one setting on the wire. A secret never carries Value: the form
// learns whether one is stored from IsSet.
type Field struct {
	Key     string
	Label   string
	Help    string
	Type    Type
	Secret  bool
	IsSet   bool
	Options []string
	Value   any
}

// MarshalJSON omits value on a secret so the ciphertext and the plaintext
// both stay off the response.
func (f Field) MarshalJSON() ([]byte, error) {
	payload := map[string]any{
		"key":    f.Key,
		"label":  f.Label,
		"type":   f.Type,
		"secret": f.Secret,
		"is_set": f.IsSet,
	}
	if f.Help != "" {
		payload["help"] = f.Help
	}
	if len(f.Options) > 0 {
		payload["options"] = f.Options
	}
	if !f.Secret {
		payload["value"] = f.Value
	}
	return json.Marshal(payload)
}

// GroupView is one save unit, with the values the caller may see.
type GroupView struct {
	Name      string     `json:"name"`
	Section   string     `json:"section"`
	Block     string     `json:"block,omitempty"`
	Scope     Scope      `json:"scope"`
	ManagedBy ManagedBy  `json:"managed_by"`
	CanUpdate bool       `json:"can_update"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	Fields    []Field    `json:"fields"`
}

// BlockView is a titled run of groups inside a section.
type BlockView struct {
	Title  string      `json:"title,omitempty"`
	Groups []GroupView `json:"groups"`
}

// SectionView is one settings page.
type SectionView struct {
	Name   string      `json:"name"`
	Blocks []BlockView `json:"blocks"`
}

// Permissions names the route pair this registry is gated on.
type Permissions struct {
	View   string `json:"view"`
	Update string `json:"update"`
}

// RegistryView is the account settings catalog, sections then blocks then groups.
type RegistryView struct {
	Permissions Permissions   `json:"permissions"`
	Sections    []SectionView `json:"sections"`
}
