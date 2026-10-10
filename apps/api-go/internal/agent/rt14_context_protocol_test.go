// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"fmt"
	"reflect"
	"testing"
)

// Invalid tool exchanges must not become acceptable merely because they fit
// the byte budget. This checks protocol structure, not tool authorization.
func TestRT14CompactValidatesSmallToolExchanges(t *testing.T) {
	call := Call{ID: "read-1", Type: "function", Function: CallFunction{Name: "read_public_fixture", Arguments: `{}`}}
	cases := []struct {
		name     string
		messages []Message
	}{
		{"orphan_result", []Message{{Role: "tool", ToolCallID: "missing", Content: `{}`}}},
		{"incomplete_round", []Message{{Role: "assistant", ToolCalls: []Call{call}}}},
		{"mismatched_result", []Message{{Role: "assistant", ToolCalls: []Call{call}}, {Role: "tool", ToolCallID: "different", Content: `{}`}}},
		{"duplicate_result", []Message{{Role: "assistant", ToolCalls: []Call{call}}, {Role: "tool", ToolCallID: call.ID, Content: `{}`}, {Role: "tool", ToolCallID: call.ID, Content: `{}`}}},
		{"duplicate_call_id", []Message{{Role: "assistant", ToolCalls: []Call{call, call}}, {Role: "tool", ToolCallID: call.ID, Content: `{}`}}},
		{"call_on_user_role", []Message{{Role: "user", ToolCalls: []Call{call}}, {Role: "tool", ToolCallID: call.ID, Content: `{}`}}},
		{"empty_call_id", []Message{{Role: "assistant", ToolCalls: []Call{{Type: "function", Function: call.Function}}}, {Role: "tool", Content: `{}`}}},
	}
	for _, tc := range cases {
		for _, budget := range []int{1, Bytes(tc.messages), Bytes(tc.messages) + 1024} {
			t.Run(fmt.Sprintf("%s/budget_%d", tc.name, budget), func(t *testing.T) {
				got, err := Compact(tc.messages, budget)
				if err == nil || got != nil {
					t.Fatalf("invalid exchange accepted: error=%v, returned_messages=%d", err, len(got))
				}
			})
		}
	}
}

func TestRT14CompactAcceptsCompleteSmallRound(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "policy"},
		{Role: "user", Content: "Read the public fixture."},
		{Role: "assistant", ToolCalls: []Call{{ID: "read-1", Type: "function", Function: CallFunction{Name: "read_public_fixture", Arguments: `{}`}}}},
		{Role: "tool", ToolCallID: "read-1", Content: `{"ok":true}`},
	}
	for _, budget := range []int{Bytes(messages), Bytes(messages) + 1024} {
		got, err := Compact(messages, budget)
		if err != nil || !reflect.DeepEqual(got, messages) {
			t.Fatalf("valid round changed: %v", err)
		}
	}
	got, err := Compact(nil, 1)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty history rejected: %v", err)
	}
}
