package toolmarket

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type dp26LoadConfig struct {
	Scenario    string `json:"scenario"`
	Bytes       int    `json:"bytes"`
	InputBytes  int    `json:"input_bytes"`
	Rate        int    `json:"rate"`
	Count       int    `json:"count"`
	SlowMillis  int    `json:"slow_millis"`
	Kind        string `json:"kind"`
	Profile     string `json:"profile"`
	ProfilePath string `json:"profile_path"`
	StartNS     int64  `json:"start_ns"`
}

type dp26Observation struct {
	ID            int     `json:"id"`
	OK            bool    `json:"ok"`
	Rejected      bool    `json:"rejected"`
	LatencyMS     float64 `json:"latency_ms"`
	TTFBMS        float64 `json:"ttfb_ms"`
	ScheduleLagMS float64 `json:"schedule_lag_ms"`
}

type dp26SlowBody struct {
	io.ReadCloser
	delay time.Duration
	ctx   context.Context
}

func (b *dp26SlowBody) Read(p []byte) (int, error) {
	if len(p) > 4096 {
		p = p[:4096]
	}
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-time.After(b.delay):
	}
	return b.ReadCloser.Read(p)
}

func dp26CPU() float64 {
	var u syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &u); err != nil {
		panic(err)
	}
	return float64(u.Utime.Sec+u.Stime.Sec) + float64(u.Utime.Usec+u.Stime.Usec)/1e6
}
func dp26Phase(stage string) {
	fmt.Printf("DP26_PHASE {\"stage\":%q,\"time_ns\":%d}\n", stage, time.Now().UnixNano())
}
func dp26Percentile(xs []float64, fraction float64) any {
	if len(xs) == 0 {
		return nil
	}
	ys := append([]float64(nil), xs...)
	sort.Float64s(ys)
	i := int(float64(len(ys)-1)*fraction + 0.999999)
	if i >= len(ys) {
		i = len(ys) - 1
	}
	return ys[i]
}

func dp26TLSFixture(t *testing.T, c dp26LoadConfig) (*network, *atomic.Int64, *atomic.Int64, func()) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "dp26-local-fixture"}, DNSNames: []string{"fixture.invalid"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fixtureCalls, fixtureErrors := &atomic.Int64{}, &atomic.Int64{}
	result := `{"text":"` + strings.Repeat("x", c.Bytes) + `","amount":9007199254740993}`
	srv := &http.Server{ReadHeaderTimeout: time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 16 << 10,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fixtureCalls.Add(1)
			if r.Method != "POST" || r.URL.Path != "/mcp" || r.TLS == nil || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer dp26-test-only")) != 1 {
				fixtureErrors.Add(1)
				http.Error(w, "fixture auth", 401)
				return
			}
			defer r.Body.Close()
			var request struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      json.RawMessage `json:"id"`
				Method  string          `json:"method"`
				Params  any             `json:"params"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&request) != nil || request.JSONRPC != "2.0" || request.Method != "tools/call" || len(request.ID) == 0 {
				fixtureErrors.Add(1)
				http.Error(w, "fixture input", 400)
				return
			}
			w.Header().Set("Content-Type", c.Kind)
			prefix := `{"jsonrpc":"2.0","id":` + string(request.ID) + `,"result":`
			if c.Kind == "text/event-stream" {
				prefix = "data: " + prefix
			}
			if _, err := io.WriteString(w, prefix+result+"}"); err != nil {
				return
			}
			if c.Kind == "text/event-stream" {
				_, _ = io.WriteString(w, "\n\n")
			}
		}),
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}))
	}()
	address := listener.Addr().String()
	// The test transport pins all dials to this owned listener, inside an empty
	// network namespace. Verification is ON and uses only the fixture CA.
	tr := &http.Transport{Proxy: nil, MaxIdleConns: 16, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 4, IdleConnTimeout: time.Second, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: time.Second, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", address)
	}}
	var transport http.RoundTripper = tr
	if c.SlowMillis > 0 {
		transport = dp26RoundTrip(func(r *http.Request) (*http.Response, error) {
			resp, err := tr.RoundTrip(r)
			if err == nil {
				resp.Body = &dp26SlowBody{resp.Body, time.Duration(c.SlowMillis) * time.Millisecond, r.Context()}
			}
			return resp, err
		})
	}
	n := &network{client: &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	cleanup := func() {
		tr.CloseIdleConnections()
		_ = srv.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("fixture did not exit")
		}
	}
	return n, fixtureCalls, fixtureErrors, cleanup
}

func TestDP26Load(t *testing.T) {
	if os.Getenv("DP26_RUN") != "1" {
		t.Skip("explicit bounded local experiment only")
	}
	var c dp26LoadConfig
	if err := json.Unmarshal([]byte(os.Getenv("DP26_CONFIG")), &c); err != nil {
		t.Fatal(err)
	}
	if c.Bytes < 0 || c.Bytes > 524288 || c.InputBytes < 0 || c.InputBytes > 262144 || c.Count < 0 || c.Count > 240 || c.Rate < 1 || c.Rate > 120 || c.SlowMillis < 0 || c.SlowMillis > 2 || int64(c.Count)*int64(c.Bytes+c.InputBytes+2048) > 128<<20 {
		t.Fatal("experiment budget exceeded")
	}
	if c.Kind != "application/json" && c.Kind != "text/event-stream" {
		t.Fatal("bad content type")
	}
	if c.Profile != "off" && c.Profile != "cpu" && c.Profile != "alloc" {
		t.Fatal("bad profile")
	}
	if c.Profile == "alloc" {
		runtime.MemProfileRate = 4096
	}
	n, fixtureCalls, fixtureErrors, cleanup := dp26TLSFixture(t, c)
	defer cleanup()
	expected := sha256.Sum256([]byte(`{"text":"` + strings.Repeat("x", c.Bytes) + `","amount":9007199254740993}`))
	params := map[string]string{"name": "fixture", "input": strings.Repeat("q", c.InputBytes)}
	call := func(ctx context.Context) (bool, error) {
		session := &mcpSession{net: n, endpoint: "https://fixture.invalid/mcp", headers: http.Header{"Authorization": {"Bearer dp26-test-only"}}}
		out, err := session.rpc(ctx, "tools/call", params)
		return err == nil && sha256.Sum256(out) == expected, err
	}
	dp26Phase("idle")
	time.Sleep(120 * time.Millisecond)
	dp26Phase("warmup")
	warmup := 0
	if c.Count > 0 {
		warmup = 8
	}
	for i := 0; i < warmup; i++ {
		ok, err := call(context.Background())
		if !ok {
			t.Fatal("warmup", err)
		}
	}
	dp26Phase("warmed")
	time.Sleep(60 * time.Millisecond)
	if c.StartNS > 0 {
		target := time.Unix(0, c.StartNS)
		if delay := time.Until(target); delay > 0 && delay < 5*time.Second {
			time.Sleep(delay)
		}
	}
	var profile *os.File
	if c.Profile != "off" {
		var err error
		profile, err = os.OpenFile(c.ProfilePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer profile.Close()
		if c.Profile == "cpu" {
			if err = pprof.StartCPUProfile(profile); err != nil {
				t.Fatal(err)
			}
		}
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cpuStart := dp26CPU()
	start := time.Now()
	dp26Phase("load")
	results := make([]dp26Observation, c.Count)
	slots := make(chan struct{}, 8) // Driver guard, NOT the production host admission policy.
	var active, peak atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < c.Count; i++ {
		planned := start.Add(time.Duration(i) * time.Second / time.Duration(c.Rate))
		if wait := time.Until(planned); wait > 0 {
			time.Sleep(wait)
		}
		obs := dp26Observation{ID: i, TTFBMS: -1, ScheduleLagMS: float64(time.Since(planned)) / float64(time.Millisecond)}
		select {
		case slots <- struct{}{}:
		default:
			obs.Rejected = true
			results[i] = obs
			continue
		}
		wg.Add(1)
		go func(i int, planned time.Time, obs dp26Observation) {
			defer wg.Done()
			defer func() { <-slots }()
			count := active.Add(1)
			for count > peak.Load() {
				old := peak.Load()
				if count <= old || peak.CompareAndSwap(old, count) {
					break
				}
			}
			defer active.Add(-1)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			requestStart := time.Now()
			var firstByte atomic.Int64
			trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { firstByte.CompareAndSwap(0, time.Since(requestStart).Nanoseconds()+1) }}
			ok, _ := call(httptrace.WithClientTrace(ctx, trace))
			obs.OK = ok
			obs.LatencyMS = float64(time.Since(planned)) / float64(time.Millisecond)
			if value := firstByte.Load(); value > 0 {
				obs.TTFBMS = float64(value-1) / float64(time.Millisecond)
			}
			results[i] = obs
		}(i, planned, obs)
	}
	wg.Wait()
	duration := 2 * time.Second
	if c.Count > 0 {
		duration = time.Duration(c.Count) * time.Second / time.Duration(c.Rate)
	}
	if wait := time.Until(start.Add(duration)); wait > 0 {
		time.Sleep(wait)
	}
	elapsed := time.Since(start).Seconds()
	if c.Profile == "cpu" {
		pprof.StopCPUProfile()
	}
	cpu := dp26CPU() - cpuStart
	runtime.ReadMemStats(&after)
	dp26Phase("post_load")
	success, rejected, failed := 0, 0, 0
	latency, first, lag := []float64{}, []float64{}, []float64{}
	for _, obs := range results {
		lag = append(lag, obs.ScheduleLagMS)
		if obs.Rejected {
			rejected++
			continue
		}
		latency = append(latency, obs.LatencyMS)
		if obs.TTFBMS >= 0 {
			first = append(first, obs.TTFBMS)
		}
		if obs.OK {
			success++
		} else {
			failed++
		}
	}
	summary := map[string]any{
		"scope":  "source-slice RPC + local verified TLS fixture + driver; NOT model/ledger capacity",
		"config": c, "go_version": runtime.Version(), "gomaxprocs": runtime.GOMAXPROCS(0), "gomemlimit": os.Getenv("GOMEMLIMIT"), "gogc": os.Getenv("GOGC"),
		"offered": c.Count, "success": success, "rejected": rejected, "failed": failed, "elapsed_seconds": elapsed, "success_per_second": float64(success) / elapsed,
		"process_cpu_seconds": cpu, "mean_core_utilization": cpu / elapsed, "max_inflight": peak.Load(), "inflight_at_end": active.Load(),
		"latency_p50_ms": dp26Percentile(latency, .50), "latency_p95_ms": dp26Percentile(latency, .95), "latency_p99_ms": dp26Percentile(latency, .99), "ttfb_p99_ms": dp26Percentile(first, .99), "schedule_lag_p99_ms": dp26Percentile(lag, .99),
		"allocated_bytes": after.TotalAlloc - before.TotalAlloc, "allocations": after.Mallocs - before.Mallocs, "gc_cycles": after.NumGC - before.NumGC, "gc_pause_ns": after.PauseTotalNs - before.PauseTotalNs, "heap_alloc_end": after.HeapAlloc, "go_runtime_bytes_end": after.Sys - after.HeapReleased,
		"warmup_calls": warmup, "fixture_calls": fixtureCalls.Load(), "fixture_errors": fixtureErrors.Load(), "observations": results,
	}
	if success > 0 {
		summary["cpu_seconds_per_success"] = cpu / float64(success)
		summary["allocated_bytes_per_success"] = float64(after.TotalAlloc-before.TotalAlloc) / float64(success)
	}
	if c.Profile == "alloc" {
		if err := pprof.Lookup("allocs").WriteTo(profile, 0); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	dp26Phase("end")
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("DP26_RESULT %s\n", encoded)
	if rejected > 0 || failed > 0 || fixtureErrors.Load() != 0 || fixtureCalls.Load() != int64(success+failed+warmup) || active.Load() != 0 {
		t.Fatal("load correctness or admission guard failed")
	}
	if capText := os.Getenv("DP26_CPU_CEILING"); capText != "" {
		ceiling, err := strconv.ParseFloat(capText, 64)
		if err != nil || cpu/elapsed > ceiling {
			t.Fatal("CPU ceiling exceeded")
		}
	}
}
