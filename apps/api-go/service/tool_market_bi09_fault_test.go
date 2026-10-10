package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Test-only transport. It consumes a successful upstream response before
// losing it. The upstream handler has already run; this is not a dial failure.
// No production URL, TLS, credential, or retry policy is changed.
type bi09LostResponseTransport struct {
	base    http.RoundTripper
	enabled atomic.Bool
	dropped atomic.Int32
}

func bi09RPCMethod(req *http.Request) (string, error) {
	if req.Method != http.MethodPost {
		return "", nil
	}
	if req.GetBody == nil {
		return "", errors.New("BI09 fixture requires a replayable request body")
	}
	body, err := req.GetBody()
	if err != nil {
		return "", err
	}
	defer body.Close()
	var envelope struct {
		Method string `json:"method"`
	}
	err = json.NewDecoder(io.LimitReader(body, 256<<10)).Decode(&envelope)
	return envelope.Method, err
}

func (f *bi09LostResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	method, err := bi09RPCMethod(req)
	if err != nil {
		return nil, err
	}
	response, err := f.base.RoundTrip(req)
	if err != nil || method != "tools/call" || !f.enabled.Load() {
		return response, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("BI09 expected a successful upstream response, got %d", response.StatusCode)
	}
	// Reading to EOF proves that the successful response was generated. Drop
	// every execution response while armed, so a retry cannot hide the fault.
	raw, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(raw, &envelope) != nil || !bytes.Contains(envelope.Result, []byte("bi09-effect-complete")) {
		return nil, errors.New("BI09 fault did not observe a completed tool result")
	}
	f.dropped.Add(1)
	return nil, io.ErrUnexpectedEOF
}

// This self-test verifies only the failure injector, not marketplace billing.
// It can also run without application dependencies:
// GO111MODULE=off go test -race tool_market_bi09_fault_test.go
func TestToolMarketBI09FaultDropsOnlyCompletedCalls(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var in struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(w, "invalid fixture input", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if in.Method == "tools/call" {
			calls.Add(1)
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"bi09-effect-complete"}]}}`)
		} else {
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
		}
	}))
	defer server.Close()
	fault := &bi09LostResponseTransport{base: server.Client().Transport}
	fault.enabled.Store(true)
	client := &http.Client{Transport: fault}
	for _, method := range []string{"tools/list", "tools/call", "tools/call"} {
		req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q}`, method)))
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(req)
		if method == "tools/call" {
			if !errors.Is(err, io.ErrUnexpectedEOF) || response != nil {
				t.Fatalf("expected response loss after execution, response=%v err=%v", response, err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
		}
	}
	if calls.Load() != 2 || fault.dropped.Load() != 2 {
		t.Fatalf("injector masked execution attempts: upstream=%d dropped=%d", calls.Load(), fault.dropped.Load())
	}
	t.Logf("fault control only: upstream_calls=%d completed_responses_dropped=%d", calls.Load(), fault.dropped.Load())
}
