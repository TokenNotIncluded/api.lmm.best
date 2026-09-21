package ali

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

type pollTestTransport func(*http.Request) (*http.Response, error)

func (f pollTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func pollTestSuccess() *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"output":{"task_status":"SUCCEEDED"}}`))}
}

func TestAliPollingTransportErrorsRespectAttemptBound(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		http.DefaultTransport = pollTestTransport(func(*http.Request) (*http.Response, error) {
			attempts++
			// A terminal result on attempt 25 keeps even the broken implementation
			// finite; a correct 20-attempt loop can never reach this response.
			if attempts == 25 {
				return pollTestSuccess(), nil
			}
			return nil, errors.New("fixture transport failure")
		})
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://provider.invalid"}}
		started := time.Now()
		_, _, err := asyncTaskWait(c, info, "fictional-task")
		t.Logf("attempts=%d virtual_elapsed=%s returned_error=%v", attempts, time.Since(started), err)
		if attempts != 20 || err == nil || time.Since(started) != 195*time.Second {
			t.Errorf("want exactly 20 failed attempts with no trailing delay, got attempts=%d err=%v elapsed=%s", attempts, err, time.Since(started))
		}
	})
}

func TestAliPollingHonorsCanceledRequest(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		http.DefaultTransport = pollTestTransport(func(r *http.Request) (*http.Response, error) {
			attempts++
			t.Logf("upstream_context_error=%v", r.Context().Err())
			return pollTestSuccess(), nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil).WithContext(ctx)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://provider.invalid"}}
		started := time.Now()
		_, _, err := asyncTaskWait(c, info, "fictional-task")
		t.Logf("attempts=%d virtual_elapsed=%s returned_error=%v", attempts, time.Since(started), err)
		if attempts != 0 || !errors.Is(err, context.Canceled) || time.Since(started) != 0 {
			t.Error("canceled request still polled upstream and returned success")
		}
	})
}

func pollTestContext(ctx context.Context) (*gin.Context, *relaycommon.RelayInfo) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil).WithContext(ctx)
	return c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl: "https://provider.invalid", ApiKey: "fictional-key",
	}}
}

type pollTestBody struct {
	io.Reader
	closed bool
}

func (b *pollTestBody) Close() error { b.closed = true; return nil }

func TestAliPollingPreservesTerminalStatusesAndRawResponse(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, status := range []string{"SUCCEEDED", "FAILED", "CANCELED", "UNKNOWN", ""} {
		for _, transient := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/transient=%t", status, transient), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					attempts := 0
					var bodies []*pollTestBody
					raw := fmt.Sprintf(`{"output":{"task_status":%q,"message":"fixture","code":"fixture-code"},"usage":{"image_count":2}}`, status)
					started := time.Now()
					http.DefaultTransport = pollTestTransport(func(r *http.Request) (*http.Response, error) {
						attempts++
						deadline, ok := r.Context().Deadline()
						if !ok || deadline.Sub(started) != 205*time.Second {
							t.Error("poll must inherit the single 205-second deadline")
						}
						if r.Header.Get("Authorization") != "Bearer fictional-key" || r.URL.Path != "/api/v1/tasks/fictional-task" {
							t.Error("poll request changed")
						}
						if transient && attempts == 1 {
							return nil, errors.New("fixture network error")
						}
						body := raw
						if transient && attempts == 2 {
							body = `{"output":{"task_status":"PENDING"}}`
						}
						reader := &pollTestBody{Reader: strings.NewReader(body)}
						bodies = append(bodies, reader)
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: reader}, nil
					})
					c, info := pollTestContext(context.Background())
					response, body, err := asyncTaskWait(c, info, "fictional-task")
					wantAttempts := 1
					if transient {
						wantAttempts = 3
					}
					if err != nil || response == nil || response.Output.TaskStatus != status || string(body) != raw || attempts != wantAttempts {
						t.Fatalf("response=%+v body=%s attempts=%d err=%v", response, body, attempts, err)
					}
					if status != "" && (response.Output.Message != "fixture" || response.Output.Code != "fixture-code" || response.Usage.ImageCount != 2) {
						t.Error("provider result fields changed")
					}
					if elapsed := time.Since(started); elapsed != 5*time.Second+time.Duration(wantAttempts-1)*10*time.Second {
						t.Fatalf("poll timing changed: %s", elapsed)
					}
					for _, reader := range bodies {
						if !reader.closed {
							t.Error("poll response body not released")
						}
					}
				})
			})
		}
	}
}

// Simulates a transport body whose read is interrupted by its request context,
// as net/http does when a request is canceled after the response headers arrive.
type pollBlockingBody struct {
	ctx    context.Context
	closed bool
}

func (b *pollBlockingBody) Read([]byte) (int, error) { <-b.ctx.Done(); return 0, b.ctx.Err() }
func (b *pollBlockingBody) Close() error             { b.closed = true; return nil }

func TestAliPollingCancellationAndOverallDeadline(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, tc := range []struct {
		name           string
		stopAfter      time.Duration
		parentDeadline bool
		hang           string
		attempts       int
	}{
		{"initial-delay", time.Second, false, "", 0},
		{"between-polls", 7 * time.Second, false, "", 1},
		{"in-flight-headers", 7 * time.Second, false, "headers", 1},
		{"in-flight-body", 7 * time.Second, false, "body", 1},
		{"shorter-parent-deadline", 7 * time.Second, true, "headers", 1},
		{"hung-headers-total-budget", 205 * time.Second, false, "headers", 1},
		{"hung-body-total-budget", 205 * time.Second, false, "body", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if tc.parentDeadline {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, tc.stopAfter)
					defer stop()
				} else if tc.stopAfter < 205*time.Second {
					go func() { time.Sleep(tc.stopAfter); cancel() }()
				}
				attempts := 0
				var blocked *pollBlockingBody
				http.DefaultTransport = pollTestTransport(func(r *http.Request) (*http.Response, error) {
					attempts++
					if tc.hang == "headers" {
						<-r.Context().Done()
						return nil, r.Context().Err()
					}
					var body io.ReadCloser = io.NopCloser(strings.NewReader(`{"output":{"task_status":"PENDING"}}`))
					if tc.hang == "body" {
						blocked = &pollBlockingBody{ctx: r.Context()}
						body = blocked
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
				})
				c, info := pollTestContext(ctx)
				started := time.Now()
				_, _, err := asyncTaskWait(c, info, "fictional-task")
				wantErr := context.Canceled
				if tc.parentDeadline || tc.stopAfter == 205*time.Second {
					wantErr = context.DeadlineExceeded
				}
				if !errors.Is(err, wantErr) || attempts != tc.attempts || time.Since(started) != tc.stopAfter {
					t.Errorf("got err=%v attempts=%d elapsed=%s", err, attempts, time.Since(started))
				}
				if blocked != nil && !blocked.closed {
					t.Error("canceled body was not closed")
				}
			})
		})
	}
}

func TestAliTaskPollReusesChannelProxyClient(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "provider.invalid" || r.Header.Get("Authorization") != "Bearer fictional-key" {
			t.Error("poll did not use the channel proxy")
		}
		_, _ = io.WriteString(w, `{"output":{"task_status":"SUCCEEDED"}}`)
	}))
	defer proxy.Close()
	_, info := pollTestContext(context.Background())
	info.ChannelBaseUrl = "http://provider.invalid"
	info.ChannelSetting.Proxy = proxy.URL
	client, err := service.GetHttpClientWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
	if err != nil {
		t.Fatal(err)
	}
	defer service.InvalidateProxyClient(proxy.URL)
	again, err := service.GetHttpClientWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
	if err != nil || client != again {
		t.Fatal("channel client was not cached")
	}
	timeout := client.Timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		response, err, _ := updateTask(ctx, client, info, "fictional-task")
		if err != nil || response.Output.TaskStatus != "SUCCEEDED" {
			t.Fatalf("poll failed: %v", err)
		}
	}
	if client.Timeout != timeout {
		t.Error("poll mutated shared client timeout")
	}
}

func TestAliTaskPollCancelsRealHTTP(t *testing.T) {
	for _, flushHeaders := range []bool{false, true} {
		t.Run(fmt.Sprintf("headers-flushed=%t", flushHeaders), func(t *testing.T) {
			started := make(chan struct{})
			released := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if flushHeaders {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
				close(released)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, info := pollTestContext(ctx)
			info.ChannelBaseUrl = server.URL
			result := make(chan error, 1)
			go func() {
				_, err, _ := updateTask(ctx, server.Client(), info, "fictional-task")
				result <- err
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("local request did not start")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("want cancellation, got %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("poll did not stop after cancellation")
			}
			select {
			case <-released:
			case <-time.After(10 * time.Second):
				t.Fatal("upstream request was not released")
			}
		})
	}
}
