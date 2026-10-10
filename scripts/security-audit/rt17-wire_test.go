// RT-17 transport observations. This is NOT an application or billing test.
// All listeners and connections are loopback-only; there is no target URL flag.
package rt17

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type observation struct {
	URI                string              `json:"request_uri"`
	Path               string              `json:"decoded_path"`
	RawPath            string              `json:"raw_path"`
	Method             string              `json:"method"`
	Authorization      []string            `json:"authorization_values"`
	FirstAuthorization string              `json:"header_get_authorization"`
	Query              map[string][]string `json:"query"`
	ContentLength      int64               `json:"content_length"`
	TransferEncoding   []string            `json:"transfer_encoding"`
	Body               string              `json:"body"`
	BodyError          string              `json:"body_error,omitempty"`
}

type result struct {
	Name      string        `json:"case"`
	Scope     string        `json:"scope"`
	Runtime   string        `json:"runtime"`
	Raw       string        `json:"raw_request"`
	Status    []int         `json:"response_status"`
	Handler   []observation `json:"handler_observations"`
	ReadError string        `json:"response_read_error,omitempty"`
}

func request(path, auth, extra, body string, last bool) string {
	closeHeader := ""
	if last {
		closeHeader = "Connection: close\r\n"
	}
	return "POST " + path + " HTTP/1.1\r\nHost: rt17.invalid\r\nAuthorization: Bearer " + auth +
		"\r\nContent-Type: application/json\r\n" + closeHeader + extra + "\r\n" + body
}

// No URL or environment variable can redirect these raw bytes to another host.
func localObserve(t *testing.T, name, raw string) result {
	t.Helper()
	var mu sync.Mutex
	events := []observation{}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		e := observation{URI: r.RequestURI, Path: r.URL.Path, RawPath: r.URL.RawPath, Method: r.Method,
			Authorization: r.Header.Values("Authorization"), FirstAuthorization: r.Header.Get("Authorization"),
			Query: r.URL.Query(), ContentLength: r.ContentLength, TransferEncoding: r.TransferEncoding, Body: string(body)}
		if err != nil {
			e.BodyError = err.Error()
		}
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
		if err != nil {
			http.Error(w, "incomplete local probe", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(e); err != nil {
			t.Logf("response write: %v", err)
		}
	}))
	s.Config.ReadHeaderTimeout = time.Second
	s.Config.ReadTimeout = time.Second
	s.Config.WriteTimeout = time.Second
	s.Config.IdleTimeout = time.Second
	s.Start()
	defer s.Close()
	addr := s.Listener.Addr().(*net.TCPAddr)
	if !addr.IP.IsLoopback() {
		t.Fatal("refusing non-loopback listener")
	}
	c, err := net.DialTimeout("tcp", addr.String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(c, raw); err != nil {
		t.Fatal(err)
	}
	out := result{Name: name, Scope: "Go net/http only; not Gin, TokenAuth, relay or billing", Runtime: runtime.Version(), Raw: raw, Status: []int{}}
	reader := bufio.NewReader(c)
	for i := 0; i < 4; i++ {
		response, err := http.ReadResponse(reader, nil)
		if err != nil {
			if err != io.EOF {
				out.ReadError = err.Error()
			}
			break
		}
		out.Status = append(out.Status, response.StatusCode)
		_, err = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if err != nil {
			out.ReadError = err.Error()
			break
		}
		if response.Close {
			break
		}
	}
	// Wait for the server to stop before inspecting all handler observations.
	c.Close()
	s.Close()
	mu.Lock()
	out.Handler = append([]observation{}, events...)
	mu.Unlock()
	return out
}

func TestRT17HTTP1Wire(t *testing.T) {
	a := `{"model":"rt17-a","marker":"BODY_A"}`
	b := `{"model":"rt17-b","marker":"BODY_B"}`
	cl := func(s string) string { return fmt.Sprintf("Content-Length: %d\r\n", len(s)) }
	second := request("/v1/chat/completions?marker=URI_B", "TEST_B", cl(b), b, true)
	chunked := fmt.Sprintf("%x\r\n%s\r\n0\r\n\r\n", len(a), a)
	cases := []struct {
		name, first, wantBody, wantPath string
		rejected, two                   bool
	}{
		{"control-pipelined", request("/v1/chat/completions?marker=URI_A", "TEST_A", cl(a), a, false), a, "/v1/chat/completions", false, true},
		{"identical-content-length", request("/v1/chat/completions?marker=URI_A", "TEST_A", cl(a)+cl(a), a, false), a, "/v1/chat/completions", false, true},
		{"conflicting-content-length", request("/v1/chat/completions?marker=URI_A", "TEST_A", cl(a)+"Content-Length: 1\r\n", a, false), a, "", true, false},
		{"comma-content-length", request("/v1/chat/completions?marker=URI_A", "TEST_A", fmt.Sprintf("Content-Length: %d, %d\r\n", len(a), len(a)), a, false), a, "", true, false},
		{"chunked-control", request("/v1/chat/completions?marker=URI_A", "TEST_A", "Transfer-Encoding: chunked\r\n", chunked, false), a, "/v1/chat/completions", false, true},
		{"transfer-encoding-and-length", request("/v1/chat/completions?marker=URI_A", "TEST_A", cl(a)+"Transfer-Encoding: chunked\r\n", chunked, false), a, "/v1/chat/completions", false, false},
		{"duplicate-transfer-encoding", request("/v1/chat/completions?marker=URI_A", "TEST_A", "Transfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\n", chunked, false), a, "", true, false},
		{"duplicate-host", request("/v1/chat/completions?marker=URI_A", "TEST_A", cl(a)+"Host: second.invalid\r\n", a, false), a, "", true, false},
		{"duplicate-authorization", request("/v1/chat/completions?marker=URI_A", "TEST_A", cl(a)+"aUtHoRiZaTiOn: Bearer TEST_C\r\n", a, false), a, "/v1/chat/completions", false, true},
		{"duplicate-query", request("/v1/chat/completions?model=rt17-a&model=rt17-c&marker=URI_A", "TEST_A", cl(a), a, false), a, "/v1/chat/completions", false, true},
		{"encoded-path", request("/v1/%63hat/completions?marker=URI_A", "TEST_A", cl(a), a, false), a, "/v1/chat/completions", false, true},
		{"encoded-slash", request("/v1%2fchat/completions?marker=URI_A", "TEST_A", cl(a), a, false), a, "/v1/chat/completions", false, true},
		{"double-encoded-slash", request("/v1%252fchat/completions?marker=URI_A", "TEST_A", cl(a), a, false), a, "/v1%2fchat/completions", false, true},
		{"case-and-trailing-slash", request("/V1/chat/completions/?marker=URI_A", "TEST_A", cl(a), a, false), a, "/V1/chat/completions/", false, true},
		{"untrusted-forwarding-headers", request("/v1/chat/completions?marker=URI_A", "TEST_A", cl(a)+"X-Forwarded-For: 127.0.0.9\r\nX-Forwarded-User: ADMIN_MARKER\r\nX-Request-Id: TRACE_A\r\n", a, false), a, "/v1/chat/completions", false, true},
	}
	results := make([]result, 0, len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := localObserve(t, tc.name, tc.first+second)
			results = append(results, r)
			t.Logf("status=%v handler_calls=%d", r.Status, len(r.Handler))
			if tc.rejected {
				if len(r.Handler) != 0 || len(r.Status) == 0 || r.Status[0] < 400 {
					t.Errorf("invalid framing reached handler: %+v", r)
				}
				return
			}
			if len(r.Handler) < 1 || len(r.Handler) > 2 {
				t.Fatalf("unexpected handler count: %d", len(r.Handler))
			}
			first := r.Handler[0]
			if first.Body != tc.wantBody || first.Path != tc.wantPath || first.FirstAuthorization != "Bearer TEST_A" {
				t.Errorf("first request content/identity crossed boundary: %+v", first)
			}
			if tc.two && len(r.Handler) != 2 {
				t.Errorf("unambiguous second request missing: %+v", r)
			}
			if len(r.Handler) == 2 {
				second := r.Handler[1]
				if second.Body != b || second.FirstAuthorization != "Bearer TEST_B" || (len(second.Query["marker"]) != 1 || second.Query["marker"][0] != "URI_B") {
					t.Errorf("second request content/identity crossed boundary: %+v", second)
				}
			}
			if tc.name == "duplicate-authorization" && len(first.Authorization) != 2 {
				t.Errorf("expected both raw credential fields to remain visible: %+v", first)
			}
			if tc.name == "duplicate-query" && len(first.Query["model"]) != 2 {
				t.Errorf("query values lost: %+v", first)
			}
		})
	}
	if dir := os.Getenv("RT17_EVIDENCE_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "wire-observations.json"), append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRT17GoJSONObservations(t *testing.T) {
	// These observations do not stand in for gjson or the actual relay DTO.
	cases := []string{
		`{"model":"rt17-a","model":"rt17-b"}`,
		`{"model":"rt17-a","Model":"rt17-b"}`,
		`{"model":"rt17-a","mo\u0064el":"rt17-b"}`,
	}
	for _, raw := range cases {
		var typed struct {
			Model string `json:"model"`
		}
		var mapped map[string]any
		if err := json.Unmarshal([]byte(raw), &typed); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(raw), &mapped); err != nil {
			t.Fatal(err)
		}
		t.Logf("raw=%s Go_struct_model=%q Go_map_model=%q", raw, typed.Model, mapped["model"])
		if typed.Model != "rt17-b" {
			t.Errorf("unexpected local Go decoder result: %q", typed.Model)
		}
	}
	// Ordinary JSON escaping is valid, not itself an ambiguity or an attack.
	var one struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(strings.NewReader(`{"mo\u0064el":"rt17-a"}`)).Decode(&one); err != nil || one.Model != "rt17-a" {
		t.Fatalf("escaped unique field: %+v %v", one, err)
	}
}
