// This is an experiment executable, not the production extension main.
// Its modules and provider are synthetic. It uses the real module host and
// the opt-in handoff implementation without changing core contracts.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/handoff"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
)

var buildVersion = "unversioned"

type module struct {
	name string
	h    http.Handler
}

func (m module) Name() string          { return m.name }
func (m module) Handler() http.Handler { return m.h }
func emit(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	listen := flag.String("listen", "127.0.0.1:0", "loopback only")
	dir := flag.String("data", "", "private experiment state directory")
	group := flag.String("modules", "alpha,beta,gamma", "synthetic modules")
	upstream := flag.String("upstream", "", "loopback mock provider")
	barrier := flag.String("barrier", "", "controlled kill point")
	fail := flag.Bool("fail-start", false, "controlled start failure before ownership changes")
	flag.Parse()
	ip, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(ip) == nil || !net.ParseIP(ip).IsLoopback() {
		return errors.New("loopback listener required")
	}
	u, err := url.Parse(*upstream)
	if err != nil || u.Scheme != "http" || u.User != nil || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return errors.New("loopback mock provider required")
	}
	if *fail {
		return errors.New("injected start failure before bind or takeover")
	}
	// Listener binding must succeed before changing local ownership.
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	s, err := handoff.Open(*dir)
	if err != nil {
		return err
	}
	defer s.Close()
	owner, err := s.Takeover(buildVersion + "-" + strconv.Itoa(os.Getpid()))
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect disabled") }}
	pause := func(point string) {
		if *barrier != point {
			return
		}
		marker := filepath.Join(*dir, "barrier")
		if err := os.WriteFile(marker, []byte(point), 0600); err != nil {
			panic(err)
		}
		for {
			if _, err := os.Stat(filepath.Join(*dir, "resume")); err == nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	var registered []modules.Module
	selected, err := modules.Select(*group, nil, []string{"alpha", "beta", "gamma"})
	if err != nil {
		return err
	}
	for _, name := range selected {
		name := name
		mux := http.NewServeMux()
		mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
			emit(w, 200, map[string]any{"version": buildVersion, "module": name, "pid": os.Getpid(), "synthetic": true})
		})
		mux.HandleFunc("GET /stream", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			for i := 0; i < 80; i++ {
				if _, err := fmt.Fprintf(w, "data: %d\n\n", i); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
			_, _ = io.WriteString(w, "event: done\ndata: complete\n\n")
			w.(http.Flusher).Flush()
		})
		mux.HandleFunc("POST /callback", func(w http.ResponseWriter, r *http.Request) {
			b, e := io.ReadAll(io.LimitReader(r.Body, handoff.MaxPayload+1))
			if e != nil || len(b) > handoff.MaxPayload {
				emit(w, 400, "input")
				return
			}
			t, e := s.Receive(r.URL.Query().Get("key"), b)
			if e != nil {
				emit(w, 503, e.Error())
				return
			}
			pause("after_callback_commit")
			emit(w, 202, t)
		})
		mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
			d, e := s.Snapshot()
			if e != nil {
				emit(w, 503, e.Error())
				return
			}
			emit(w, 200, d)
		})
		mux.HandleFunc("POST /work", func(w http.ResponseWriter, r *http.Request) {
			key := r.URL.Query().Get("key")
			d, e := s.Snapshot()
			if e != nil {
				emit(w, 503, e.Error())
				return
			}
			prior, ok := d.Tasks[key]
			if ok && prior.State == "done" {
				emit(w, 200, prior)
				return
			}
			t, e := s.Claim(owner, key)
			if e != nil {
				emit(w, 409, e.Error())
				return
			}
			pause("after_claim_before_start")
			t, e = s.Start(owner, t)
			if e != nil {
				emit(w, 409, e.Error())
				return
			}
			pause("after_start_before_effect")
			payload, _ := json.Marshal(t)
			req, e := http.NewRequestWithContext(r.Context(), "POST", *upstream+"/apply", bytes.NewReader(payload))
			if e != nil {
				emit(w, 502, e.Error())
				return
			}
			resp, e := client.Do(req)
			if e != nil {
				emit(w, 502, "uncertain")
				return
			}
			defer resp.Body.Close()
			var receipt handoff.Receipt
			if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&receipt) != nil {
				emit(w, 502, "uncertain")
				return
			}
			pause("after_effect_before_commit")
			t, e = s.Complete(owner, t, receipt)
			if e != nil {
				emit(w, 409, e.Error())
				return
			}
			pause("after_commit_before_ack")
			emit(w, 200, t)
		})
		mux.HandleFunc("POST /reconcile", func(w http.ResponseWriter, r *http.Request) {
			d, e := s.Snapshot()
			if e != nil {
				emit(w, 503, e.Error())
				return
			}
			t, ok := d.Tasks[r.URL.Query().Get("key")]
			if !ok || t.State != "uncertain" {
				emit(w, 409, "not uncertain")
				return
			}
			// Lookup only. A missing result never causes another external apply.
			req, _ := http.NewRequestWithContext(r.Context(), "GET", *upstream+"/lookup?key="+url.QueryEscape(t.Key), nil)
			resp, e := client.Do(req)
			if e != nil {
				emit(w, 502, "uncertain")
				return
			}
			defer resp.Body.Close()
			var receipt handoff.Receipt
			if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&receipt) != nil {
				emit(w, 409, "unknown result retained")
				return
			}
			t, e = s.Complete(owner, t, receipt)
			if e != nil {
				emit(w, 409, e.Error())
				return
			}
			emit(w, 200, t)
		})
		registered = append(registered, module{name, mux})
	}
	h, err := modules.NewHost(registered, []byte(strings.Repeat("x", 32)))
	if err != nil {
		return err
	}
	server := &http.Server{Handler: h, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = h.MarkReady(); err != nil {
		return err
	}
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"listen": listener.Addr().String(), "pid": os.Getpid(), "version": buildVersion, "epoch": owner.Epoch})
	select {
	case err = <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		h.BeginDrain()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err = server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		if err = h.Wait(shutdown); err != nil {
			return err
		}
		err = <-result
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
