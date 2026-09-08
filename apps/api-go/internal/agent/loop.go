package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// NormalizeCalls repairs provider IDs before either the assistant message or
// its results are retained. IDs must be unique across the entire request.
func NormalizeCalls(calls []Call, step int, used map[string]bool) []Call {
	result := append([]Call(nil), calls...)
	for i := range result {
		call := &result[i]
		call.ID = strings.TrimSpace(call.ID)
		if call.ID == "" || used[call.ID] {
			base := fmt.Sprintf("assistant-call-%d-%d", step+1, i+1)
			call.ID = base
			for suffix := 1; used[call.ID]; suffix++ {
				call.ID = fmt.Sprintf("%s-%d", base, suffix)
			}
		}
		used[call.ID] = true
		if call.Type == "" {
			call.Type = "function"
		}
		call.Function.Name = strings.TrimSpace(call.Function.Name)
	}
	return result
}

type callState struct {
	attempts int
	success  bool
	epoch    int
}

// LoopGuard bounds identical failures and reads, and prevents a successful
// mutation from executing twice in one request. A successful mutation opens a
// fresh read epoch so a verification read can repeat the original lookup.
type LoopGuard struct {
	calls map[[32]byte]callState
	epoch int
}

func callFingerprint(call Call) [32]byte {
	arguments := strings.TrimSpace(call.Function.Arguments)
	if arguments == "" {
		arguments = "{}"
	}
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.UseNumber() // Float conversion could merge distinct large IDs.
	var input any
	if decoder.Decode(&input) == nil {
		if canonical, err := json.Marshal(fingerprintValue(input)); err == nil && json.Valid([]byte(arguments)) {
			arguments = string(canonical)
		}
	}
	return sha256.Sum256(bytes.Join([][]byte{[]byte(call.Function.Name), []byte(arguments)}, []byte{0}))
}

// Equivalent numeric spellings (10, 10.0, 1e1) execute identically and must
// share a write fence. Tagged objects keep numbers distinct from user strings
// and objects; rational normalization preserves large identifier precision.
func fingerprintValue(value any) any {
	switch value := value.(type) {
	case json.Number:
		text := string(value)
		bounded := len(text) <= 128
		if index := strings.IndexAny(text, "eE"); index >= 0 {
			exponent, err := strconv.Atoi(text[index+1:])
			bounded = bounded && err == nil && exponent >= -1024 && exponent <= 1024
		}
		if bounded {
			if number, ok := new(big.Rat).SetString(text); ok {
				text = number.RatString()
			}
		}
		return struct{ Number string }{text}
	case map[string]any:
		object := make(map[string]any, len(value))
		for key, item := range value {
			object[key] = fingerprintValue(item)
		}
		return struct{ Object map[string]any }{object}
	case []any:
		array := make([]any, len(value))
		for index, item := range value {
			array[index] = fingerprintValue(item)
		}
		return struct{ Array []any }{array}
	default:
		return value
	}
}

func (g *LoopGuard) Allow(call Call, readOnly bool) bool {
	if g.calls == nil {
		g.calls = make(map[[32]byte]callState)
	}
	key := callFingerprint(call)
	state := g.calls[key]
	if readOnly && state.epoch != g.epoch {
		state = callState{epoch: g.epoch}
	}
	if (!readOnly && state.success) || state.attempts >= 2 {
		return false
	}
	state.attempts++
	g.calls[key] = state
	return true
}

func (g *LoopGuard) Complete(call Call, readOnly, success bool) {
	if !success || readOnly {
		return
	}
	key := callFingerprint(call)
	state := g.calls[key]
	state.success = true
	g.calls[key] = state
	g.epoch++
}
