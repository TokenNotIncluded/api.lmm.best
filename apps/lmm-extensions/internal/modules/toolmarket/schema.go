package toolmarket

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A deliberately closed JSON Schema subset. Unsupported constraints reject the
// tool at discovery; they are never silently ignored. No remote $ref fetching.
type schema struct {
	Type        string                     `json:"type"`
	Description string                     `json:"description,omitempty"`
	Title       string                     `json:"title,omitempty"`
	Properties  map[string]json.RawMessage `json:"properties,omitempty"`
	Required    []string                   `json:"required,omitempty"`
	Additional  *bool                      `json:"additionalProperties,omitempty"`
	Items       json.RawMessage            `json:"items,omitempty"`
	Enum        []any                      `json:"enum,omitempty"`
	Minimum     *json.Number               `json:"minimum,omitempty"`
	Maximum     *json.Number               `json:"maximum,omitempty"`
	MinLength   *int                       `json:"minLength,omitempty"`
	MaxLength   *int                       `json:"maxLength,omitempty"`
	MinItems    *int                       `json:"minItems,omitempty"`
	MaxItems    *int                       `json:"maxItems,omitempty"`
}

func decodeStrict(b []byte, v any) error {
	if len(b) == 0 || len(b) > maxBody || !utf8.Valid(b) {
		return ErrInvalid
	}
	if err := uniqueJSON(b); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func uniqueJSON(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return ErrInvalid
		}
		t, e := d.Token()
		if e != nil {
			return ErrInvalid
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return ErrInvalid
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return ErrInvalid
				}
				seen[key] = true
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e := walk(depth + 1); e != nil {
					return e
				}
			}
		default:
			return ErrInvalid
		}
		_, e = d.Token()
		return e
	}
	if e := walk(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrInvalid
	}
	return nil
}
func readSchema(raw json.RawMessage, depth int) (schema, error) {
	var s schema
	if depth > 12 || len(raw) > 65536 {
		return s, ErrInvalid
	}
	if e := decodeStrict(raw, &s); e != nil {
		return s, e
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return s, ErrInvalid
	}
	for _, v := range fields {
		if bytes.Equal(v, []byte("null")) {
			return s, ErrInvalid
		}
	}
	if s.Enum != nil && len(s.Enum) == 0 {
		return s, ErrInvalid
	}
	if len(s.Description) > 2000 || len(s.Title) > 200 || len(s.Properties) > 128 || len(s.Enum) > 128 {
		return s, ErrInvalid
	}
	switch s.Type {
	case "object":
		if len(s.Items) > 0 {
			return s, ErrInvalid
		}
		for k, v := range s.Properties {
			if len(k) == 0 || len(k) > 128 {
				return s, ErrInvalid
			}
			if _, e := readSchema(v, depth+1); e != nil {
				return s, e
			}
		}
		seen := map[string]bool{}
		for _, k := range s.Required {
			if _, ok := s.Properties[k]; !ok || seen[k] {
				return s, ErrInvalid
			}
			seen[k] = true
		}
	case "array":
		if len(s.Items) == 0 {
			return s, ErrInvalid
		}
		if _, e := readSchema(s.Items, depth+1); e != nil {
			return s, e
		}
	case "string", "number", "integer", "boolean", "null":
	default:
		return s, invalid("unsupported schema")
	}
	if s.Type != "object" && (len(s.Properties) > 0 || len(s.Required) > 0 || s.Additional != nil) {
		return s, ErrInvalid
	}
	if s.Type != "array" && (len(s.Items) > 0 || s.MinItems != nil || s.MaxItems != nil) {
		return s, ErrInvalid
	}
	if s.Type != "string" && (s.MinLength != nil || s.MaxLength != nil) {
		return s, ErrInvalid
	}
	if s.Type != "number" && s.Type != "integer" && (s.Minimum != nil || s.Maximum != nil) {
		return s, ErrInvalid
	}
	for _, n := range []*int{s.MinLength, s.MaxLength, s.MinItems, s.MaxItems} {
		if n != nil && (*n < 0 || *n > maxBody) {
			return s, ErrInvalid
		}
	}
	if s.MinLength != nil && s.MaxLength != nil && *s.MinLength > *s.MaxLength {
		return s, ErrInvalid
	}
	if s.MinItems != nil && s.MaxItems != nil && *s.MinItems > *s.MaxItems {
		return s, ErrInvalid
	}
	for _, n := range []*json.Number{s.Minimum, s.Maximum} {
		if n != nil {
			if _, ok := safeNumber(*n); !ok {
				return s, ErrInvalid
			}
		}
	}
	if s.Minimum != nil && s.Maximum != nil && numberCompare(*s.Minimum, *s.Maximum) > 0 {
		return s, ErrInvalid
	}
	return s, nil
}
func safeNumber(n json.Number) (*big.Rat, bool) {
	s := string(n)
	if len(s) > 100 {
		return nil, false
	}
	lower := strings.ToLower(s)
	if at := strings.IndexByte(lower, 'e'); at >= 0 {
		e, err := strconv.Atoi(lower[at+1:])
		if err != nil || e < -128 || e > 128 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(s)
}

// JSON Schema compares numbers by value, so enum 1 also matches 1.0.
func jsonEqual(a, b any) bool {
	if x, ok := a.(json.Number); ok {
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		xr, xo := safeNumber(x)
		yr, yo := safeNumber(y)
		return xo && yo && xr.Cmp(yr) == 0
	}
	if x, ok := a.([]any); ok {
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	if x, ok := a.(map[string]any); ok {
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !jsonEqual(v, w) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}
func numberCompare(a, b json.Number) int {
	x, ok := safeNumber(a)
	if !ok {
		return 1
	}
	y, ok := safeNumber(b)
	if !ok {
		return -1
	}
	return x.Cmp(y)
}
func validateArgs(raw json.RawMessage, value json.RawMessage) error {
	s, e := readSchema(raw, 0)
	if e != nil {
		return e
	}
	if s.Type != "object" {
		return ErrInvalid
	}
	var v any
	if e = decodeStrict(value, &v); e != nil {
		return e
	}
	return validateValue(s, v, 0)
}
func validateValue(s schema, v any, depth int) error {
	if depth > 12 {
		return ErrInvalid
	}
	if len(s.Enum) > 0 {
		found := false
		for _, x := range s.Enum {
			if jsonEqual(x, v) {
				found = true
			}
		}
		if !found {
			return ErrInvalid
		}
	}
	switch s.Type {
	case "object":
		o, ok := v.(map[string]any)
		if !ok {
			return ErrInvalid
		}
		for _, k := range s.Required {
			if _, ok = o[k]; !ok {
				return ErrInvalid
			}
		}
		for k, x := range o {
			r, exists := s.Properties[k]
			if !exists {
				if s.Additional != nil && !*s.Additional {
					return ErrInvalid
				}
				continue
			}
			sub, e := readSchema(r, depth+1)
			if e != nil {
				return e
			}
			if e = validateValue(sub, x, depth+1); e != nil {
				return e
			}
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return ErrInvalid
		}
		if s.MinItems != nil && len(a) < *s.MinItems || s.MaxItems != nil && len(a) > *s.MaxItems {
			return ErrInvalid
		}
		sub, e := readSchema(s.Items, depth+1)
		if e != nil {
			return e
		}
		for _, x := range a {
			if e = validateValue(sub, x, depth+1); e != nil {
				return e
			}
		}
	case "string":
		x, ok := v.(string)
		if !ok {
			return ErrInvalid
		}
		n := utf8.RuneCountInString(x)
		if s.MinLength != nil && n < *s.MinLength || s.MaxLength != nil && n > *s.MaxLength {
			return ErrInvalid
		}
	case "integer", "number":
		x, ok := v.(json.Number)
		if !ok || len(x) > 100 {
			return ErrInvalid
		}
		r, ok := safeNumber(x)
		if !ok || s.Type == "integer" && !r.IsInt() {
			return ErrInvalid
		}
		if s.Minimum != nil && numberCompare(x, *s.Minimum) < 0 || s.Maximum != nil && numberCompare(x, *s.Maximum) > 0 {
			return ErrInvalid
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return ErrInvalid
		}
	case "null":
		if v != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func short(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n])
	}
	return string(r)
}
