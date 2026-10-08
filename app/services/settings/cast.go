package settings

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// ValidationError is a 422 body: one list of messages per field.
type ValidationError struct {
	Fields map[string][]string
}

func (e *ValidationError) Error() string {
	return "validation failed"
}

func (e *ValidationError) add(field, message string) {
	if e.Fields == nil {
		e.Fields = map[string][]string{}
	}
	e.Fields[field] = append(e.Fields[field], message)
}

func (e *ValidationError) empty() bool {
	return e == nil || len(e.Fields) == 0
}

func castIn(value any, definition Definition) (string, error) {
	switch definition.Type {
	case TypeString:
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("expected string")
		}
		text = strings.TrimSpace(text)
		if err := requireOption(text, definition.Options); err != nil {
			return "", err
		}
		return text, nil
	case TypeDecimal:
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("decimals must be strings")
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return "", nil
		}
		parsed, ok := new(big.Rat).SetString(text)
		if !ok || parsed == nil {
			return "", fmt.Errorf("expected a decimal number")
		}
		return text, nil
	case TypeInt:
		text, err := integerString(value)
		if err != nil {
			return "", err
		}
		return text, nil
	case TypeBool:
		switch typed := value.(type) {
		case bool:
			return strconv.FormatBool(typed), nil
		case string:
			if typed != "true" && typed != "false" {
				return "", fmt.Errorf("expected boolean")
			}
			return typed, nil
		default:
			return "", fmt.Errorf("expected boolean")
		}
	case TypeStringList:
		items, err := stringList(value)
		if err != nil {
			return "", err
		}
		for _, item := range items {
			if err := requireOption(item, definition.Options); err != nil {
				return "", err
			}
		}
		return strings.Join(items, ","), nil
	case TypeBigInt:
		return bigIntString(value)
	default:
		return "", fmt.Errorf("unknown type %q", definition.Type)
	}
}

func castOut(raw string, definition Definition) any {
	switch definition.Type {
	case TypeInt:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return raw
		}
		return n
	case TypeBool:
		return raw == "true"
	case TypeStringList:
		return splitList(raw)
	case TypeBigInt:
		// Raw units stay strings so a wei-scale value is not rounded.
		return raw
	default:
		// Decimals stay strings: JSON numbers cannot keep a fixed scale.
		return raw
	}
}

func defaultStored(definition Definition) string {
	if definition.Default == nil {
		return ""
	}
	switch value := definition.Default().(type) {
	case string:
		return value
	case int:
		return strconv.Itoa(value)
	case bool:
		return strconv.FormatBool(value)
	case []string:
		return strings.Join(value, ",")
	default:
		return fmt.Sprint(value)
	}
}

func defaultValue(definition Definition) any {
	return castOut(defaultStored(definition), definition)
}

func requireOption(value string, options []string) error {
	if value == "" || len(options) == 0 {
		return nil
	}
	for _, option := range options {
		if value == option {
			return nil
		}
	}
	return fmt.Errorf("must be one of: %s", strings.Join(options, ", "))
}

func integerString(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		if _, err := strconv.Atoi(strings.TrimSpace(typed)); err != nil {
			return "", fmt.Errorf("expected integer")
		}
		return strings.TrimSpace(typed), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case float64:
		if typed != math.Trunc(typed) || typed > math.MaxInt64 || typed < math.MinInt64 {
			return "", fmt.Errorf("expected integer")
		}
		return strconv.FormatInt(int64(typed), 10), nil
	case json.Number:
		return integerString(typed.String())
	default:
		return "", fmt.Errorf("expected integer")
	}
}

func bigIntString(value any) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("raw units must be base-10 strings")
	}
	text = strings.TrimSpace(text)
	if text == "" || strings.ContainsAny(text, "+-.") {
		return "", fmt.Errorf("expected a non-negative base-10 integer")
	}
	parsed, ok := new(big.Int).SetString(text, 10)
	if !ok || parsed.Sign() < 0 {
		return "", fmt.Errorf("expected a non-negative base-10 integer")
	}
	return text, nil
}

func stringList(value any) ([]string, error) {
	switch typed := value.(type) {
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expected a list of strings")
			}
			text = strings.TrimSpace(text)
			if text == "" {
				return nil, fmt.Errorf("expected a list of strings")
			}
			items = append(items, text)
		}
		return items, nil
	case []string:
		return typed, nil
	case string:
		return splitList(typed), nil
	default:
		return nil, fmt.Errorf("expected a list of strings")
	}
}

func splitList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		items = append(items, part)
	}
	return items
}
