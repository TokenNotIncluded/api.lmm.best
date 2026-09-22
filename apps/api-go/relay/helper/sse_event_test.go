package helper

import (
	"bufio"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

func TestSSEEventBoundaries(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
		done       bool
	}{
		{"plain multiline", "data: hello\ndata: world\n\n", []string{"hello\nworld"}, false},
		{"complete JSON first", "data: {}\ndata: []\n\n", []string{"{}\n[]"}, false},
		{"empty fields", "data\ndata:\ndata: tail\ndata:\n\n", []string{"\n\ntail\n"}, false},
		{"empty event", "data:\n\n", []string{""}, false},
		{"marker inside event", "data: [DONE]\ndata: more\n\ndata: next\n\n", []string{"[DONE]\nmore", "next"}, false},
		{"marker after empty field", "data:\ndata: [DONE]\n\n", []string{"\n[DONE]"}, false},
		{"marker event", "data: [DONE]\n\ndata: ignored\n\n", nil, true},
		{"bare compatibility", "data: kept\n[DONE]\n", []string{"kept"}, true},
		{"comment and colon payload", ": heartbeat\ndata: :payload\n: between\ndata: two\n\n", []string{":payload\ntwo"}, false},
		{"whitespace", "data:  padded  \n\n", []string{" padded  "}, false},
		{"unfinished EOF", "data: {}\n", nil, false},
		{"BOM", "\uFEFFdata: hello\n\n", []string{"hello"}, false},
	}
	for _, tc := range cases {
		for _, ending := range []string{"\n", "\r\n", "\r"} {
			for _, fragmented := range []bool{false, true} {
				t.Run(tc.name+"/"+strings.ReplaceAll(ending, "\r", "CR"), func(t *testing.T) {
					decoder := sseEventDecoder{limit: 1024}
					var reader io.Reader = strings.NewReader(strings.ReplaceAll(tc.body, "\n", ending))
					if fragmented {
						reader = iotest.OneByteReader(reader)
					}
					scanner := bufio.NewScanner(reader)
					scanner.Split(splitSSELines())
					var got []string
					ended := false
					for scanner.Scan() {
						payload, ready, done, err := decoder.line(scanner.Text())
						if err != nil {
							t.Fatal(err)
						}
						if ready {
							got = append(got, payload)
						}
						if done {
							ended = true
							break
						}
					}
					if err := scanner.Err(); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, tc.want) || ended != tc.done {
						t.Fatalf("got %#v done=%v; want %#v done=%v", got, ended, tc.want, tc.done)
					}
				})
			}
		}
	}
}

func TestSSESizeIncludesEmptyFieldSeparators(t *testing.T) {
	decoder := sseEventDecoder{limit: 2}
	for i := 0; i < 3; i++ {
		if _, _, _, err := decoder.line("data:"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := decoder.line("data:"); err != ErrSSEEventTooLarge {
		t.Fatalf("got %v", err)
	}
}
