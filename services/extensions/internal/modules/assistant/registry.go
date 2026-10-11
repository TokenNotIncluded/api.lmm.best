package assistant

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
)

type Property struct {
	Type      string   `json:"type"`
	Enum      []string `json:"enum,omitempty"`
	MinLength int      `json:"minLength,omitempty"`
	MaxLength int      `json:"maxLength,omitempty"`
	Minimum   *int64   `json:"minimum,omitempty"`
	Maximum   *int64   `json:"maximum,omitempty"`
}
type InputSchema struct {
	Type                 string              `json:"type"`
	Properties           map[string]Property `json:"properties"`
	Required             []string            `json:"required"`
	AdditionalProperties bool                `json:"additionalProperties"`
}

func objectSchema(properties map[string]Property) InputSchema {
	required := make([]string, 0, len(properties))
	for name := range properties {
		required = append(required, name)
	}
	sort.Strings(required)
	return InputSchema{Type: "object", Properties: properties, Required: required}
}
func textProperty(max int) Property { return Property{Type: "string", MinLength: 1, MaxLength: max} }
func enumProperty(values ...string) Property {
	return Property{Type: "string", Enum: values, MinLength: 1, MaxLength: 64}
}
func numberProperty(min, max int64) Property {
	return Property{Type: "integer", Minimum: &min, Maximum: &max}
}
func (s InputSchema) valid() bool {
	if s.Type != "object" || s.AdditionalProperties || len(s.Properties) > 12 || len(s.Required) != len(s.Properties) {
		return false
	}
	seen := map[string]bool{}
	for _, name := range s.Required {
		if seen[name] || !access.ID(name) {
			return false
		}
		seen[name] = true
		if _, ok := s.Properties[name]; !ok {
			return false
		}
	}
	for _, p := range s.Properties {
		switch p.Type {
		case "string":
			if p.MinLength < 0 || p.MaxLength < 1 || p.MaxLength > 4096 || p.MinLength > p.MaxLength || p.Minimum != nil || p.Maximum != nil {
				return false
			}
			if len(p.Enum) > 32 {
				return false
			}
			for _, v := range p.Enum {
				if utf8.RuneCountInString(v) < p.MinLength || utf8.RuneCountInString(v) > p.MaxLength {
					return false
				}
			}
		case "integer":
			if p.Minimum == nil || p.Maximum == nil || *p.Minimum > *p.Maximum || len(p.Enum) > 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
func (s InputSchema) decode(data json.RawMessage) (map[string]any, error) {
	var args map[string]any
	if e := access.Decode(data, &args); e != nil {
		return nil, e
	}
	if len(args) != len(s.Required) {
		return nil, access.ErrInvalid
	}
	for name, p := range s.Properties {
		value, exists := args[name]
		if !exists {
			return nil, access.ErrInvalid
		}
		switch p.Type {
		case "string":
			text, ok := value.(string)
			if !ok || !utf8.ValidString(text) || strings.ContainsRune(text, 0) || utf8.RuneCountInString(text) < p.MinLength || utf8.RuneCountInString(text) > p.MaxLength {
				return nil, access.ErrInvalid
			}
			if len(p.Enum) > 0 {
				found := false
				for _, v := range p.Enum {
					found = found || v == text
				}
				if !found {
					return nil, access.ErrInvalid
				}
			}
		case "integer":
			n, ok := value.(json.Number)
			if !ok {
				return nil, access.ErrInvalid
			}
			v, e := n.Int64()
			if e != nil || v < *p.Minimum || v > *p.Maximum {
				return nil, access.ErrInvalid
			}
			args[name] = v
		}
	}
	return args, nil
}

type Summary struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Permission   access.Permission `json:"permission"`
	MinLevel     int32             `json:"min_level"`
	PersonalOnly bool              `json:"personal_only,omitempty"`
}
type Definition struct {
	Summary
	Parameters InputSchema `json:"parameters"`
}
type Invocation struct {
	Credential string
	Principal  access.Principal
	Arguments  map[string]any
}
type Tool struct {
	Definition Definition
	Execute    func(context.Context, Invocation) (any, error)
}
type Registry struct {
	tools map[string]Tool
	names []string
}

// NewRegistry snapshots explicit, trusted code registrations. Tools cannot be
// registered by a model or user. The schema validator is also used at execution.
func NewRegistry(tools []Tool) (*Registry, error) {
	if len(tools) > 64 {
		return nil, access.ErrInvalid
	}
	r := &Registry{tools: map[string]Tool{}}
	for _, t := range tools {
		d := t.Definition
		if !access.ID(d.Name) || len(d.Description) == 0 || len(d.Description) > 160 || d.MinLevel < 0 || d.MinLevel > 6 || (d.Permission != access.Read && d.Permission != access.Manage) || !d.Parameters.valid() || t.Execute == nil {
			return nil, access.ErrInvalid
		}
		if _, ok := r.tools[d.Name]; ok {
			return nil, access.ErrConflict
		}
		// Break aliases to caller-owned schema maps, enum slices and integer bounds.
		b, err := json.Marshal(d)
		var snapshot Definition
		if err != nil || len(b) > 8192 || json.Unmarshal(b, &snapshot) != nil {
			return nil, access.ErrInvalid
		}
		t.Definition = snapshot
		r.tools[d.Name] = t
		r.names = append(r.names, d.Name)
	}
	sort.Strings(r.names)
	return r, nil
}
func allowed(p access.Principal, d Definition) bool {
	return p.Level >= d.MinLevel && (!d.PersonalOnly || p.Account.Kind == "personal") && (d.Permission != access.Manage || p.Account.Kind == "personal" || p.TeamRole == "owner" || p.TeamRole == "admin")
}
func (r *Registry) Discover(p access.Principal, query, after string, limit int) ([]Summary, string, error) {
	if len(query) > 64 || len(after) > 128 || limit < 1 || limit > 20 {
		return nil, "", access.ErrInvalid
	}
	out := []Summary{}
	next := ""
	query = strings.ToLower(query)
	for _, name := range r.names {
		t := r.tools[name]
		if name <= after || !allowed(p, t.Definition) || !strings.Contains(strings.ToLower(name+" "+t.Definition.Description), query) {
			continue
		}
		if len(out) == limit {
			next = out[len(out)-1].Name
			break
		}
		out = append(out, t.Definition.Summary)
	}
	return out, next, nil
}
func (r *Registry) Describe(p access.Principal, name string) (Definition, error) {
	t, ok := r.tools[name]
	if !ok || !allowed(p, t.Definition) {
		return Definition{}, access.ErrNotFound
	}
	var copy Definition
	b, _ := json.Marshal(t.Definition)
	_ = json.Unmarshal(b, &copy)
	return copy, nil
}
