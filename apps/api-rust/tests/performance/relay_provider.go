// Local deterministic provider for measuring completed, billed relay requests.
// Build this file separately from http_load.go. No external network is used.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

const reply = `{"id":"chatcmpl-local-parity","object":"chat.completion","created":1700000000,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"A deterministic local response for the backend comparison."},"finish_reason":"stop"}],"usage":{"prompt_tokens":512,"completion_tokens":128,"total_tokens":640}}`

func main() {
	address := flag.String("listen", "127.0.0.1:0", "literal loopback listen address")
	flag.Parse()
	host, _, err := net.SplitHostPort(*address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		fmt.Fprintln(os.Stderr, "provider must bind to a literal loopback address")
		os.Exit(2)
	}
	var completed, rejected atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"completed": completed.Load(), "rejected": rejected.Load()})
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		decodeErr := decoder.Decode(&request)
		var trailing any
		if decodeErr != nil || decoder.Decode(&trailing) != io.EOF || request.Model != "gpt-4o-mini" || request.Stream || len(request.Messages) != 1 || request.Messages[0].Role != "user" || len(request.Messages[0].Content) < 1000 || r.Header.Get("Authorization") != "Bearer local-parity-upstream" {
			rejected.Add(1)
			http.Error(w, "invalid local benchmark request", http.StatusBadRequest)
			return
		}
		completed.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", fmt.Sprint(len(reply)))
		_, _ = io.WriteString(w, reply)
	})
	server := http.Server{Addr: *address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "local provider stopped:", err)
		os.Exit(1)
	}
}
