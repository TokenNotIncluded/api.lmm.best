// A dependency-free HTTP load driver. Build outside the source tree:
// go build -o /path/to/http-load apps/api-rust/tests/performance/http_load.go
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"
)

type observation struct {
	LatencyMS float64
	Bytes     int
	Error     string
}

func decodeJSON(body []byte, destination *any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	// Financial counters can exceed float64's exact integer range. Never let
	// two different balances become an apparently identical response.
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func percentile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	position := float64(len(values)-1) * q
	lower := int(position)
	if lower+1 == len(values) {
		return values[lower]
	}
	return values[lower] + (values[lower+1]-values[lower])*(position-float64(lower))
}

func main() {
	endpoint := flag.String("url", "", "loopback HTTP URL")
	requests := flag.Int("requests", 10000, "total requests")
	concurrency := flag.Int("concurrency", 16, "concurrent connections")
	expectedFile := flag.String("expected-json", "", "JSON body the endpoint must return")
	requestFile := flag.String("request-json", "", "optional JSON body; uses POST when supplied")
	headersFile := flag.String("headers-json", "", "optional JSON object of request headers")
	flag.Parse()
	u, err := url.Parse(*endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || *requests < 1 || *concurrency < 1 {
		fmt.Fprintln(os.Stderr, "a literal loopback HTTP URL and positive request/concurrency counts are required")
		os.Exit(2)
	}
	var expected any
	if *expectedFile != "" {
		raw, err := os.ReadFile(*expectedFile)
		if err != nil || decodeJSON(raw, &expected) != nil {
			fmt.Fprintln(os.Stderr, "invalid expected JSON file")
			os.Exit(2)
		}
	}
	expectedJSON, _ := json.Marshal(expected)
	method := http.MethodGet
	var requestBody []byte
	if *requestFile != "" {
		requestBody, err = os.ReadFile(*requestFile)
		var value any
		if err != nil || decodeJSON(requestBody, &value) != nil {
			fmt.Fprintln(os.Stderr, "invalid request JSON file")
			os.Exit(2)
		}
		method = http.MethodPost
	}
	headers := make(map[string]string)
	if *headersFile != "" {
		raw, readErr := os.ReadFile(*headersFile)
		if readErr != nil || json.Unmarshal(raw, &headers) != nil {
			fmt.Fprintln(os.Stderr, "invalid request headers file")
			os.Exit(2)
		}
	}
	transport := &http.Transport{
		MaxIdleConns: *concurrency, MaxIdleConnsPerHost: *concurrency,
		MaxConnsPerHost: *concurrency, DisableCompression: true,
		ResponseHeaderTimeout: 10 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	jobs := make(chan struct{}, *concurrency)
	results := make(chan observation, *requests)
	var workers sync.WaitGroup
	started := time.Now()
	for range *concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range jobs {
				begin := time.Now()
				result := observation{}
				request, err := http.NewRequest(method, *endpoint, bytes.NewReader(requestBody))
				if err != nil {
					results <- observation{Error: "request"}
					continue
				}
				if method == http.MethodPost {
					request.Header.Set("Content-Type", "application/json")
				}
				for name, value := range headers {
					request.Header.Set(name, value)
				}
				response, err := client.Do(request)
				if err != nil {
					result.Error = "transport"
				} else {
					body, readErr := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
					response.Body.Close()
					result.Bytes = len(body)
					var value any
					if readErr != nil {
						result.Error = "body_read"
					} else if len(body) > 4<<20 {
						result.Error = "body_too_large"
					} else if response.StatusCode != http.StatusOK {
						result.Error = fmt.Sprintf("http_%d", response.StatusCode)
					} else if decodeJSON(body, &value) != nil {
						result.Error = "invalid_json"
					} else {
						if object, ok := value.(map[string]any); ok && object["success"] == false {
							result.Error = "business_failure"
						}
						canonical, _ := json.Marshal(value)
						if *expectedFile != "" && !bytes.Equal(canonical, expectedJSON) {
							result.Error = "body_mismatch"
						}
					}
				}
				result.LatencyMS = float64(time.Since(begin).Nanoseconds()) / 1e6
				results <- result
			}
		}()
	}
	for range *requests {
		jobs <- struct{}{}
	}
	close(jobs)
	workers.Wait()
	close(results)
	elapsed := time.Since(started).Seconds()
	latencies := make([]float64, 0, *requests)
	errors := map[string]int{}
	totalBytes := int64(0)
	for result := range results {
		if result.Error == "" {
			latencies = append(latencies, result.LatencyMS)
		} else {
			errors[result.Error]++
		}
		totalBytes += int64(result.Bytes)
	}
	sort.Float64s(latencies)
	output := map[string]any{
		"requests": *requests, "concurrency": *concurrency,
		"successes": len(latencies), "errors": errors, "elapsed_seconds": elapsed,
		"successful_requests_per_second": float64(len(latencies)) / elapsed,
		"latency_ms":                     map[string]float64{"p50": percentile(latencies, .5), "p95": percentile(latencies, .95), "p99": percentile(latencies, .99)},
		"received_bytes":                 totalBytes,
	}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		os.Exit(1)
	}
	if len(errors) != 0 {
		os.Exit(1)
	}
}
