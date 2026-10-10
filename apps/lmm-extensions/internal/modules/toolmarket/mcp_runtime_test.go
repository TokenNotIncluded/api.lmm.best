package toolmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type dp26RoundTrip func(*http.Request) (*http.Response, error)

func (f dp26RoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type dp26Body struct {
	io.Reader
	closed *atomic.Int64
}

func (b *dp26Body) Close() error { b.closed.Add(1); return nil }

func dp26Fixture(body, kind string, status int) (*mcpSession, *atomic.Int64, *atomic.Int64) {
	calls, closed := &atomic.Int64{}, &atomic.Int64{}
	tr := dp26RoundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer dp26-test-only" {
			return nil, errors.New("fixture credential missing")
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {kind}}, Body: &dp26Body{strings.NewReader(body), closed}, Request: r}, nil
	})
	return &mcpSession{net: &network{client: &http.Client{Transport: tr}}, endpoint: "https://fixture.invalid/mcp", headers: http.Header{"Authorization": {"Bearer dp26-test-only"}}}, calls, closed
}

func TestDP26RPCValidation(t *testing.T) {
	cases := []struct {
		name, body string
		good       bool
	}{
		{"object", `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`, true},
		{"precise_integer", `{"jsonrpc":"2.0","id":1,"result":{"amount":9007199254740993}}`, true},
		{"array", `{"jsonrpc":"2.0","id":1,"result":[1,"a",true]}`, true},
		{"scalar_false", `{"jsonrpc":"2.0","id":1,"result":false}`, true},
		{"bad_version", `{"jsonrpc":"1.0","id":1,"result":{}}`, false},
		{"wrong_id", `{"jsonrpc":"2.0","id":2,"result":{}}`, false},
		{"string_id", `{"jsonrpc":"2.0","id":"1","result":{}}`, false},
		{"null_id", `{"jsonrpc":"2.0","id":null,"result":{}}`, false},
		{"method", `{"jsonrpc":"2.0","id":1,"method":"tools/call","result":{}}`, false},
		{"error", `{"jsonrpc":"2.0","id":1,"error":{"code":-1}}`, false},
		{"null_error", `{"jsonrpc":"2.0","id":1,"result":{},"error":null}`, false},
		{"missing_result", `{"jsonrpc":"2.0","id":1}`, false},
		{"null_result", `{"jsonrpc":"2.0","id":1,"result":null}`, false},
		{"duplicate_id", `{"jsonrpc":"2.0","id":2,"id":1,"result":{}}`, false},
		{"escaped_duplicate_id", `{"jsonrpc":"2.0","id":1,"\u0069d":1,"result":{}}`, false},
		{"duplicate_nested", `{"jsonrpc":"2.0","id":1,"result":{"amount":1,"amount":2}}`, false},
		{"duplicate_in_array", `{"jsonrpc":"2.0","id":1,"result":[{"x":1,"x":2}]}`, false},
		{"trailing_json", `{"jsonrpc":"2.0","id":1,"result":{}} {}`, false},
		{"malformed", `{"jsonrpc":"2.0","id":1,"result":`, false},
		{"deep", `{"jsonrpc":"2.0","id":1,"result":` + strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40) + "}", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var jsonResult []byte
			for _, kind := range []string{"application/json", "text/event-stream"} {
				body := c.body
				if kind == "text/event-stream" {
					body = "data: " + body + "\n\n"
				}
				s, calls, closed := dp26Fixture(body, kind, 200)
				got, err := s.rpc(context.Background(), "tools/call", map[string]string{"name": "fixture"})
				if (err == nil) != c.good {
					t.Fatalf("%s: good=%v got %s %v", kind, c.good, got, err)
				}
				if calls.Load() != 1 || closed.Load() < 1 {
					t.Fatalf("calls=%d closes=%d", calls.Load(), closed.Load())
				}
				if kind == "application/json" {
					jsonResult = got
				} else if !bytes.Equal(got, jsonResult) {
					t.Fatal("JSON/SSE result bytes differ")
				}
			}
		})
	}
}

func TestDP26SSEFraming(t *testing.T) {
	good := `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`
	for _, c := range []struct {
		name, body string
		good       bool
	}{
		{"notification_then_result", "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\ndata: " + good + "\n\n", true},
		{"duplicate_notification", "data: {\"method\":\"a\",\"method\":\"b\"}\n\ndata: " + good + "\n\n", false},
		{"multiline", "data: {\"jsonrpc\":\"2.0\",\n" + "data: \"id\":1,\"result\":{\"ok\":true}}\n\n", true},
		{"crlf", "data: " + good + "\r\n\r\n", true},
		{"no_terminal_blank", "data: " + good + "\n", false},
		{"no_terminal_newline", "data: " + good, false},
		{"oversized_event", "data: " + strings.Repeat("x", maxBody) + "\n\n", false},
		{"endless_comments", strings.Repeat(": keepalive\n", maxBody/10), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, calls, closed := dp26Fixture(c.body, "text/event-stream", 200)
			_, err := s.rpc(context.Background(), "tools/call", nil)
			if (err == nil) != c.good {
				t.Fatalf("good=%v err=%v", c.good, err)
			}
			if calls.Load() != 1 || closed.Load() < 1 {
				t.Fatal("body not closed or request retried")
			}
		})
	}
}

func TestDP26StatusesAndCancellation(t *testing.T) {
	for _, code := range []int{401, 403, 429, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			s, calls, closed := dp26Fixture("{}", "application/json", code)
			_, err := s.rpc(context.Background(), "tools/call", nil)
			want := ErrUpstream
			if code == 401 || code == 403 {
				want = ErrAuth
			}
			if code == 429 {
				want = ErrLimit
			}
			if !errors.Is(err, want) || calls.Load() != 1 || closed.Load() < 1 {
				t.Fatal(err, calls.Load(), closed.Load())
			}
		})
	}
	var calls atomic.Int64
	tr := dp26RoundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	s := &mcpSession{net: &network{client: &http.Client{Transport: tr}}, endpoint: "https://fixture.invalid/mcp", headers: http.Header{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := s.rpc(ctx, "tools/call", nil); !errors.Is(err, ErrUpstream) || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
}

func TestDP26ResponseOwnershipAndIsolation(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := fmt.Sprintf(`{"user":"fixture-%d","amount":9007199254740993}`, i)
			s, _, _ := dp26Fixture(`data: {"jsonrpc":"2.0","id":1,"result":`+want+"}\n\n", "text/event-stream", 200)
			got, err := s.rpc(context.Background(), "tools/call", nil)
			if err != nil || string(got) != want {
				t.Errorf("response isolation %d: %s %v", i, got, err)
			}
		}(i)
	}
	wg.Wait()
	raw := []byte(`{"jsonrpc":"2.0","id":1,"result":{"secret":"fixture"}}`)
	got, err := rpcResult(raw, json.RawMessage("1"))
	if err != nil {
		t.Fatal(err)
	}
	saved := string(got)
	for i := range raw {
		raw[i] = 'x'
	}
	if string(got) != saved {
		t.Fatal("response aliases caller buffer")
	}
}

func BenchmarkDP26RPC(b *testing.B) {
	for _, size := range []int{4096, 65536, 524288} {
		for _, kind := range []string{"application/json", "text/event-stream"} {
			name := fmt.Sprintf("%s/%d", kind, size)
			b.Run(name, func(b *testing.B) {
				result := `{"text":"` + strings.Repeat("x", size) + `","amount":9007199254740993}`
				body := `{"jsonrpc":"2.0","id":1,"result":` + result + "}"
				if kind == "text/event-stream" {
					body = "data: " + body + "\n\n"
				}
				s, _, _ := dp26Fixture(body, kind, 200)
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					s.next = 0
					got, err := s.rpc(context.Background(), "tools/call", nil)
					if err != nil || !bytes.Equal(got, []byte(result)) {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
