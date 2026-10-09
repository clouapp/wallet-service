// Package pyjson reads and writes JSON byte-for-byte like CPython's json module
// (json.dumps with ensure_ascii=True and insertion-ordered objects). State files
// first written by Python tools (LocalStack snapshots and their digests, the e2e
// funding ledger, claim and lock files) keep exactly the same layout when Go
// rewrites them.
package pyjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	firstPrintableASCII   = ' '
	lastPrintableASCII    = '~'
	maxBasicMultilingual  = 0xFFFF
	surrogateOffset       = 0x10000
	highSurrogateBase     = 0xD800
	lowSurrogateBase      = 0xDC00
	surrogatePayloadShift = 10
	surrogatePayloadMask  = 0x3FF
)

// Field is one key/value pair of an Object.
type Field struct {
	Key   string
	Value any
}

// Object is an insertion-ordered JSON object, like a Python dict.
type Object []Field

// Get returns the value of key and whether it is present.
func (o Object) Get(key string) (any, bool) {
	for _, field := range o {
		if field.Key == key {
			return field.Value, true
		}
	}
	return nil, false
}

// String returns the value of key when it is a JSON string.
func (o Object) String(key string) string {
	value, _ := o.Get(key)
	text, _ := value.(string)
	return text
}

// Set replaces the value of key in place, or appends key when it is new (Python
// dict assignment semantics).
func (o Object) Set(key string, value any) Object {
	for index, field := range o {
		if field.Key == key {
			updated := append(Object(nil), o...)
			updated[index].Value = value
			return updated
		}
	}
	return append(append(Object(nil), o...), Field{Key: key, Value: value})
}

// Separators are the (item, key) separators of json.dumps.
type Separators struct {
	Item, Key string
}

var (
	// Compact is separators=(",", ":").
	Compact = Separators{Item: ",", Key: ":"}
	// Default is json.dumps without separators or indent.
	Default = Separators{Item: ", ", Key: ": "}
	// indented is what json.dumps uses once indent is set.
	indented = Separators{Item: ",", Key: ": "}
)

// Dumps mirrors json.dumps(value, separators=...). Supported values: nil, string,
// *string, bool, int, int64, json.Number, []any, []string, Object.
func Dumps(value any, separators Separators) (string, error) {
	var builder strings.Builder
	if err := encode(&builder, value, separators, -1, 0); err != nil {
		return "", err
	}
	return builder.String(), nil
}

// DumpsIndent mirrors json.dumps(value, indent=indent).
func DumpsIndent(value any, indent int) (string, error) {
	if indent < 0 {
		return "", errors.New("pyjson: indent must not be negative")
	}
	var builder strings.Builder
	if err := encode(&builder, value, indented, indent, 0); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func encode(builder *strings.Builder, value any, separators Separators, indent, depth int) error {
	switch typed := value.(type) {
	case nil:
		builder.WriteString("null")
	case string:
		builder.WriteString(Quote(typed))
	case *string:
		if typed == nil {
			builder.WriteString("null")
			return nil
		}
		builder.WriteString(Quote(*typed))
	case bool:
		builder.WriteString(strconv.FormatBool(typed))
	case int:
		builder.WriteString(strconv.Itoa(typed))
	case int64:
		builder.WriteString(strconv.FormatInt(typed, 10))
	case json.Number:
		builder.WriteString(typed.String())
	case []string:
		items := make([]any, len(typed))
		for index, item := range typed {
			items[index] = item
		}
		return encodeList(builder, items, separators, indent, depth)
	case []any:
		return encodeList(builder, typed, separators, indent, depth)
	case Object:
		return encodeObject(builder, typed, separators, indent, depth)
	default:
		return fmt.Errorf("pyjson: unsupported value type %T", value)
	}
	return nil
}

func encodeList(builder *strings.Builder, items []any, separators Separators, indent, depth int) error {
	if len(items) == 0 {
		builder.WriteString("[]")
		return nil
	}
	builder.WriteByte('[')
	for index, item := range items {
		writeItemPrefix(builder, index, separators, indent, depth+1)
		if err := encode(builder, item, separators, indent, depth+1); err != nil {
			return err
		}
	}
	writeClosingIndent(builder, indent, depth)
	builder.WriteByte(']')
	return nil
}

func encodeObject(builder *strings.Builder, object Object, separators Separators, indent, depth int) error {
	if len(object) == 0 {
		builder.WriteString("{}")
		return nil
	}
	builder.WriteByte('{')
	for index, field := range object {
		writeItemPrefix(builder, index, separators, indent, depth+1)
		builder.WriteString(Quote(field.Key))
		builder.WriteString(separators.Key)
		if err := encode(builder, field.Value, separators, indent, depth+1); err != nil {
			return err
		}
	}
	writeClosingIndent(builder, indent, depth)
	builder.WriteByte('}')
	return nil
}

func writeItemPrefix(builder *strings.Builder, index int, separators Separators, indent, depth int) {
	if index > 0 {
		builder.WriteString(separators.Item)
	}
	if indent >= 0 {
		builder.WriteByte('\n')
		builder.WriteString(strings.Repeat(" ", indent*depth))
	}
}

func writeClosingIndent(builder *strings.Builder, indent, depth int) {
	if indent >= 0 {
		builder.WriteByte('\n')
		builder.WriteString(strings.Repeat(" ", indent*depth))
	}
}

var shortEscapes = map[rune]string{
	'"':  `\"`,
	'\\': `\\`,
	'\b': `\b`,
	'\f': `\f`,
	'\n': `\n`,
	'\r': `\r`,
	'\t': `\t`,
}

// Quote is json.encoder.py_encode_basestring_ascii: every rune outside printable
// ASCII becomes \uXXXX (lowercase hex, surrogate pairs above the BMP).
func Quote(value string) string {
	var builder strings.Builder
	builder.Grow(len(value) + 2)
	builder.WriteByte('"')
	for _, character := range value {
		if escaped, ok := shortEscapes[character]; ok {
			builder.WriteString(escaped)
			continue
		}
		if character >= firstPrintableASCII && character <= lastPrintableASCII {
			builder.WriteRune(character)
			continue
		}
		writeUnicodeEscape(&builder, character)
	}
	builder.WriteByte('"')
	return builder.String()
}

func writeUnicodeEscape(builder *strings.Builder, character rune) {
	if character <= maxBasicMultilingual {
		fmt.Fprintf(builder, `\u%04x`, character)
		return
	}
	payload := character - surrogateOffset
	high := highSurrogateBase | ((payload >> surrogatePayloadShift) & surrogatePayloadMask)
	low := lowSurrogateBase | (payload & surrogatePayloadMask)
	fmt.Fprintf(builder, `\u%04x\u%04x`, high, low)
}

// Decode parses JSON keeping object key order: objects become Object, arrays
// []any, numbers json.Number, plus string, bool and nil.
func Decode(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("pyjson: trailing data after the JSON value")
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		return decodeObject(decoder)
	case '[':
		return decodeArray(decoder)
	default:
		return nil, fmt.Errorf("pyjson: unexpected delimiter %q", delimiter)
	}
}

func decodeObject(decoder *json.Decoder) (Object, error) {
	object := Object{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("pyjson: object key is not a string")
		}
		value, err := decodeValue(decoder)
		if err != nil {
			return nil, err
		}
		object = append(object, Field{Key: key, Value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return object, nil
}

func decodeArray(decoder *json.Decoder) ([]any, error) {
	items := []any{}
	for decoder.More() {
		value, err := decodeValue(decoder)
		if err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return items, nil
}
