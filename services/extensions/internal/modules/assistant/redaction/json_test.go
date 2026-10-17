// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package redaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestJSONMasksNestedFieldsAndConnectionSecrets(t *testing.T) {
	input := []byte(`{"content":[{"type":"text","text":" Cookie: a=firstFixture; b=secondFixture\nnext line "}],"structuredContent":{"password":{"value":"thirdFixture"},"nested":[{"refresh_token":"fourthFixture"}]},"opaque":"prefix-rawFixture-suffix","precise":9007199254740993,"isError":false}`)
	out, err := JSON(input, "rawFixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"firstFixture", "secondFixture", "thirdFixture", "fourthFixture", "rawFixture"} {
		if bytes.Contains(out, []byte(secret)) {
			t.Fatal("retained synthetic secret")
		}
	}
	if !bytes.Contains(out, []byte("9007199254740993")) || !bytes.Contains(out, []byte(`"isError":false`)) || !bytes.Contains(out, []byte(`\nnext line `)) {
		t.Fatal("safe structure, exact number or whitespace changed")
	}
	var value any
	if err := json.Unmarshal(out, &value); err != nil {
		t.Fatal(err)
	}
	again, err := JSON(out, "rawFixture")
	if err != nil || !bytes.Equal(again, out) {
		t.Fatal("not stable", err)
	}
}

func TestJSONRejectsAmbiguousAndUnboundedResults(t *testing.T) {
	for _, value := range []string{
		`{"x":1,"x":2}`, `{"x":1,"\u0078":2}`, `{} {}`, `{"x":`,
		strings.Repeat("[", 34) + "0" + strings.Repeat("]", 34), string([]byte{'"', 255, '"'}),
		`"` + strings.Repeat("x", 1<<20) + `"`, `{"secretFixture":1,"[REDACTED]":2}`,
	} {
		if _, err := JSON([]byte(value), "secretFixture"); !errors.Is(err, ErrInvalidJSON) {
			t.Fatal("accepted invalid fixture", err)
		}
	}
}

func TestJSONPreservesSafeMCPValues(t *testing.T) {
	for _, value := range []string{`null`, `false`, `0`, `[]`, `{}`, `" text "`, `{"content":[],"n":1.2300e+2}`} {
		out, err := JSON([]byte(value))
		if err != nil || string(out) != value {
			t.Fatalf("safe value changed: %s: %s %v", value, out, err)
		}
	}
}
