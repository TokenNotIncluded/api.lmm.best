package commerceimport

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed commerce-import.v1.schema.json
var schemaDocument []byte

var schemaOnce sync.Once
var schemas map[string]*jsonschema.Schema
var schemaError error

const schemaResource = "https://commerce-import.example/schema.json"

func compileSchemas() {
	var doc map[string]any
	if json.Unmarshal(schemaDocument, &doc) != nil {
		schemaError = ErrInvalidResponse
		return
	}
	// This is an offline schema identifier, never an HTTP target. The supplied
	// schema's deployment-specific $id cannot select an authorization origin.
	doc["$id"] = schemaResource
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	c.UseRegexpEngine(func(pattern string) (jsonschema.Regexp, error) {
		// The trusted protocol schema uses a JS absolute-end lookahead. RE2's
		// \z is exactly equivalent, including for a trailing newline.
		pattern = strings.ReplaceAll(pattern, `(?![\s\S])`, `\z`)
		pattern = unicodePatternEscape.ReplaceAllString(pattern, `\x{$1}`)
		return regexp.Compile(pattern)
	})
	if c.AddResource(schemaResource, doc) != nil {
		schemaError = ErrInvalidResponse
		return
	}
	schemas = make(map[string]*jsonschema.Schema)
	for _, name := range []string{"TokenResponse", "Catalog", "Listing", "CardIssueRequest", "CardBatch"} {
		s, err := c.Compile(schemaResource + "#/$defs/" + name)
		if err != nil {
			schemaError = err
			return
		}
		schemas[name] = s
	}
}

var unicodePatternEscape = regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)

func validateSchema(name string, value any) error {
	schemaOnce.Do(compileSchemas)
	if schemaError != nil || schemas[name] == nil {
		return ErrInvalidResponse
	}
	// The prose explicitly treats an omitted reference price as unknown. Keep
	// the original wire response intact while validating that omission as null.
	normalizeUnknownPrices(name, value)
	if schemas[name].Validate(value) != nil {
		return ErrInvalidResponse
	}
	return nil
}

// decodeJSON rejects duplicate keys at every depth rather than allowing
// encoding/json's last-value-wins behavior. It also bounds nesting/node count,
// rejects invalid UTF-8 and all numbers that are not finite float64 values.
func decodeJSON(raw []byte) (map[string]any, error) {
	if !utf8.Valid(raw) {
		return nil, ErrInvalidResponse
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	nodes := 0
	v, err := jsonValue(d, 0, &nodes)
	if err != nil {
		return nil, ErrInvalidResponse
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrInvalidResponse
	}
	object, ok := v.(map[string]any)
	if !ok {
		return nil, ErrInvalidResponse
	}
	return object, nil
}

func jsonValue(d *json.Decoder, depth int, nodes *int) (any, error) {
	*nodes++
	if depth > 64 || *nodes > 500000 {
		return nil, ErrInvalidResponse
	}
	token, err := d.Token()
	if err != nil {
		return nil, ErrInvalidResponse
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			object := make(map[string]any)
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return nil, ErrInvalidResponse
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, ErrInvalidResponse
				}
				if _, exists := object[key]; exists {
					return nil, ErrInvalidResponse
				}
				item, err := jsonValue(d, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				object[key] = item
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, ErrInvalidResponse
			}
			return object, nil
		case '[':
			array := []any{}
			for d.More() {
				item, err := jsonValue(d, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				array = append(array, item)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, ErrInvalidResponse
			}
			return array, nil
		default:
			return nil, ErrInvalidResponse
		}
	case json.Number:
		if len(value) > 1000 {
			return nil, ErrInvalidResponse
		}
		if at := strings.IndexAny(string(value), "eE"); at >= 0 {
			exponent, err := strconv.ParseInt(string(value)[at+1:], 10, 32)
			if err != nil || exponent < -324 || exponent > 308 {
				return nil, ErrInvalidResponse
			}
		}
		n, err := value.Float64()
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, ErrInvalidResponse
		}
		return value, nil
	default:
		return token, nil
	}
}

func normalizeUnknownPrices(name string, value any) {
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	variantPrice := func(value any) {
		if variant, ok := value.(map[string]any); ok {
			if _, exists := variant["price"]; !exists {
				variant["price"] = nil
			}
		}
	}
	switch name {
	case "Listing":
		if variants, ok := object["variants"].([]any); ok {
			for _, variant := range variants {
				variantPrice(variant)
			}
		}
	case "Catalog":
		if products, ok := object["products"].([]any); ok {
			for _, listing := range products {
				normalizeUnknownPrices("Listing", listing)
			}
		}
	case "CardBatch":
		variantPrice(object["variant"])
	}
}

var rawMessageType = reflect.TypeFor[json.RawMessage]()

// encoding/json matches struct field names case-insensitively. Response
// extensions such as CODES/EXPIRES_IN must not replace canonical schema-checked
// codes/expires_in. Project only exact JSON tags, recursively, before decoding.
func decodeFields(object map[string]any, destination any) error {
	t := reflect.TypeOf(destination)
	if t == nil || t.Kind() != reflect.Pointer {
		return ErrInvalidResponse
	}
	value := canonicalFields(object, t.Elem())
	raw, err := json.Marshal(value)
	if err != nil || json.Unmarshal(raw, destination) != nil {
		return ErrInvalidResponse
	}
	return nil
}

func canonicalFields(value any, t reflect.Type) any {
	if t == rawMessageType {
		return value
	}
	if t.Kind() == reflect.Pointer {
		return canonicalFields(value, t.Elem())
	}
	switch t.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return value
		}
		projected := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			key := strings.Split(field.Tag.Get("json"), ",")[0]
			if key == "" || key == "-" {
				continue
			}
			if item, exists := object[key]; exists {
				projected[key] = canonicalFields(item, field.Type)
			}
		}
		return projected
	case reflect.Slice:
		items, ok := value.([]any)
		if !ok {
			return value
		}
		projected := make([]any, len(items))
		for i, item := range items {
			projected[i] = canonicalFields(item, t.Elem())
		}
		return projected
	default:
		return value
	}
}

func unixSeconds(value any) (int64, error) {
	n, ok := value.(json.Number)
	if !ok {
		return 0, ErrInvalidResponse
	}
	r, ok := new(big.Rat).SetString(string(n))
	if !ok || r.Sign() <= 0 || r.Cmp(new(big.Rat).SetInt64(math.MaxInt64)) > 0 {
		return 0, ErrInvalidResponse
	}
	i := new(big.Int).Quo(r.Num(), r.Denom())
	if !i.IsInt64() || i.Int64() <= 0 {
		return 0, ErrInvalidResponse
	}
	return i.Int64(), nil
}

func identity(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) < 1 || utf8.RuneCountInString(s) > 100 {
		return false
	}
	for _, ch := range s {
		if ch < 0x20 || ch == 0x7f {
			return false
		}
	}
	return true
}

func secret(s string, minimum int) bool { return len(s) >= minimum && len(s) <= 4096 && ascii(s) }

var productIDPattern = regexp.MustCompile(`\A[A-Za-z0-9_-]{1,100}\z`)
var variantIDPattern = regexp.MustCompile(`\A[a-z0-9][a-z0-9_-]{0,39}\z`)
var currencyPattern = regexp.MustCompile(`\A[A-Z]{3,5}\z`)

func validateListing(listing Listing, issuer string) error {
	if listing.Schema != ListingSchema || !identity(listing.ID) || !identity(listing.ShopID) {
		return ErrInvalidResponse
	}
	u, err := publicHTTPSURL(listing.RedemptionURL)
	if err != nil {
		return ErrInvalidResponse
	}
	origin, err := publicHTTPSURL(issuer)
	if err != nil || !strings.EqualFold(u.Hostname(), origin.Hostname()) || callbackPort(u) != callbackPort(origin) {
		return ErrInvalidResponse
	}
	seen := map[string]bool{}
	for _, variant := range listing.Variants {
		if !variantIDPattern.MatchString(variant.ID) || !currencyPattern.MatchString(variant.Currency) || seen[variant.ID] {
			return ErrInvalidResponse
		}
		seen[variant.ID] = true
	}
	return nil
}
