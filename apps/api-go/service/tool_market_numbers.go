package service

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

// Numeric lexical limits prevent exact rational schema validation from turning a
// short, untrusted exponent into an unbounded big integer allocation.
const marketMaxNumberDigits = 256
const marketMaxNumberExponent = 1024

func marketDecodeExactJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrMarketRemoteInput
	}
	return nil
}

func marketExactJSONBounded(value any, depth int) bool {
	if depth > 32 {
		return false
	}
	switch value := value.(type) {
	case json.Number:
		number := value.String()
		parts := strings.FieldsFunc(number, func(r rune) bool { return r == 'e' || r == 'E' })
		if len(parts) == 0 || len(parts) > 2 || len(strings.TrimLeft(strings.ReplaceAll(parts[0], ".", ""), "-")) > marketMaxNumberDigits {
			return false
		}
		if len(parts) == 2 {
			exponent, err := strconv.ParseInt(parts[1], 10, 32)
			if err != nil || exponent < -marketMaxNumberExponent || exponent > marketMaxNumberExponent {
				return false
			}
		}
	case map[string]any:
		for _, child := range value {
			if !marketExactJSONBounded(child, depth+1) {
				return false
			}
		}
	case []any:
		for _, child := range value {
			if !marketExactJSONBounded(child, depth+1) {
				return false
			}
		}
	}
	return true
}

// ValidateToolMarketArguments validates and returns the same exact numeric tree
// that the executor sends to MCP. No floating-point validation copy is involved.
func ValidateToolMarketArguments(schemaRaw, argumentsRaw json.RawMessage) (map[string]any, error) {
	schema, err := marketSchema(schemaRaw)
	if err != nil {
		return nil, err
	}
	var arguments map[string]any
	if len(argumentsRaw) == 0 || len(argumentsRaw) > 128<<10 || marketDecodeExactJSON(argumentsRaw, &arguments) != nil || arguments == nil || !marketExactJSONBounded(arguments, 0) || schema.Validate(arguments) != nil {
		return nil, ErrMarketRemoteInput
	}
	return arguments, nil
}
