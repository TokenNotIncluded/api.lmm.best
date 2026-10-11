// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"bytes"
	"encoding/json"
	"io"
)

// The zero value is retained only as the old inert CatalogChange field. It is
// not a valid purchasable price. Every executable price passes Price.valid.
func (p Price) unset() bool { return p.Amount == 0 && p.Currency == "" && p.Unit == "" }

// MarshalJSON preserves the original external-price bytes. Legacy credits use
// their own exact unit; they never masquerade as currency minor units.
func (p Price) MarshalJSON() ([]byte, error) {
	if !p.valid() && !p.unset() {
		return nil, ErrInvalid
	}
	if p.Unit == "credits" {
		return json.Marshal(struct {
			Amount int64  `json:"amount_credits"`
			Unit   string `json:"unit"`
		}{p.Amount, "credits"})
	}
	return json.Marshal(struct {
		Amount   int64  `json:"amount_minor"`
		Currency string `json:"currency"`
	}{p.Amount, p.Currency})
}
func (p *Price) UnmarshalJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return ErrInvalid
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return ErrInvalid
		}
		name, ok := key.(string)
		if !ok {
			return ErrInvalid
		}
		switch name {
		case "amount_minor", "currency", "amount_credits", "unit":
		default:
			return ErrInvalid
		}
		if _, exists := fields[name]; exists {
			return ErrInvalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrInvalid
		}
		fields[name] = value
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') || d.Decode(new(any)) != io.EOF || len(fields) != 2 {
		return ErrInvalid
	}
	var value Price
	if credits, ok := fields["amount_credits"]; ok {
		if json.Unmarshal(credits, &value.Amount) != nil || json.Unmarshal(fields["unit"], &value.Unit) != nil || value.Unit != "credits" {
			return ErrInvalid
		}
	} else {
		if json.Unmarshal(fields["amount_minor"], &value.Amount) != nil || json.Unmarshal(fields["currency"], &value.Currency) != nil {
			return ErrInvalid
		}
	}
	if !value.valid() && !value.unset() {
		return ErrInvalid
	}
	*p = value
	return nil
}
