// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import (
	"reflect"
	"strings"
	"testing"
)

func TestAssistantSSEDecoderDoesNotDispatchEOF(t *testing.T) {
	for _, payload := range []string{`{"choices":[{"delta":{"content":"partial"}}]}`, `[DONE]`, `{"error":{"message":"failed"}}`} {
		for _, ending := range []string{"", "\n", "\r", "\r\n"} {
			t.Run(payload+"/"+strings.ReplaceAll(ending, "\r", "CR"), func(t *testing.T) {
				var decoder assistantSSEDecoder
				var events []string
				dispatch := func(data string) { events = append(events, data) }
				decoder.feed([]byte("data: "+payload+ending), dispatch)
				decoder.flush(dispatch)
				if len(events) != 0 {
					t.Fatalf("EOF dispatched an unfinished event: %q", events)
				}
			})
		}
	}
}

func TestAssistantSSEDecoderByteSplitLineEndings(t *testing.T) {
	for _, ending := range []string{"\n", "\r", "\r\n"} {
		t.Run(strings.ReplaceAll(ending, "\r", "CR"), func(t *testing.T) {
			var decoder assistantSSEDecoder
			var events []string
			dispatch := func(data string) { events = append(events, data) }
			input := strings.ReplaceAll(": heartbeat\nevent: message\ndata: 你好🌍\ndata: second line\n\ndata: [DONE]\n\n", "\n", ending)
			for _, b := range []byte(input) {
				decoder.feed([]byte{b}, dispatch)
			}
			want := []string{"你好🌍\nsecond line", "[DONE]"}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("complete events must dispatch before EOF: got %q, want %q", events, want)
			}
			decoder.flush(dispatch)
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("EOF dispatched an event twice: %q", events)
			}
		})
	}
}

func TestAssistantSSEDecoderKeepsOnlyCommittedEvents(t *testing.T) {
	var decoder assistantSSEDecoder
	var events []string
	dispatch := func(data string) { events = append(events, data) }
	decoder.feed([]byte("data: complete\n\ndata: incomplete\n"), dispatch)
	decoder.flush(dispatch)
	if !reflect.DeepEqual(events, []string{"complete"}) {
		t.Fatalf("EOF must retain earlier complete events without promoting the tail: %q", events)
	}
}

func TestAssistantSSEDecoderIgnoresEventsAfterDone(t *testing.T) {
	var decoder assistantSSEDecoder
	var events []string
	dispatch := func(data string) { events = append(events, data) }
	decoder.feed([]byte("data: complete\n\ndata: [DONE]\n\ndata: late\n\n"), dispatch)
	decoder.feed([]byte("data: later\n\n"), dispatch)
	decoder.flush(dispatch)
	if !reflect.DeepEqual(events, []string{"complete", "[DONE]"}) {
		t.Fatalf("a committed terminal must fence later events: %q", events)
	}
}
