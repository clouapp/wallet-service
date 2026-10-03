package authmodel

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

type UserPreferences struct {
	PreferredFiatCode string `json:"preferred_fiat_code,omitempty"`
	DisplayInFiat     *bool  `json:"display_in_fiat,omitempty"`
}

func (p UserPreferences) Value() (driver.Value, error) {
	encoded, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	// A string, not []byte: the jsonb column rejects the bytea encoding GORM
	// sends for a []byte driver value.
	return string(encoded), nil
}

func (p *UserPreferences) Scan(src interface{}) error {
	if src == nil {
		return nil
	}
	var data []byte
	switch v := src.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("UserPreferences.Scan: unsupported type %T", src)
	}
	return json.Unmarshal(data, p)
}

func (p *UserPreferences) GetPreferredFiat() string {
	if p == nil || p.PreferredFiatCode == "" {
		return "USD"
	}
	return p.PreferredFiatCode
}

func (p *UserPreferences) IsDisplayInFiat() bool {
	if p == nil || p.DisplayInFiat == nil {
		return true
	}
	return *p.DisplayInFiat
}
