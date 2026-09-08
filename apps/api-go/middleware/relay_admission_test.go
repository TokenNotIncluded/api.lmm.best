package middleware

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/pkg/admission"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func admissionRequest(path string, length int64) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("body"))
	r.ContentLength = length
	return r
}

type admissionReadSpy struct{ reads int }

func (s *admissionReadSpy) Read([]byte) (int, error) { s.reads++; return 0, io.EOF }
func (*admissionReadSpy) Close() error               { return nil }

func TestRelayRequestAdmissionRejectsBeforeReadingOrDispatch(t *testing.T) {
	limiter := admission.NewLargeRequestLimiter(1, 4)
	release, allowed := limiter.TryAcquire(admissionRequest("/", 4))
	require.True(t, allowed)
	defer release()
	guard := relayRequestAdmission(limiter)
	for _, path := range []string{"/v1/responses", "/v1/messages", "/v1beta/models/gemini:generateContent", "/v1/models/gemini:generateContent"} {
		t.Run(path, func(t *testing.T) {
			called := false
			router := gin.New()
			router.POST(path, guard, func(c *gin.Context) { called = true; c.Status(http.StatusNoContent) })
			spy := &admissionReadSpy{}
			r := admissionRequest(path, -1)
			r.Body = spy
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			assert.Equal(t, http.StatusTooManyRequests, w.Code)
			assert.Equal(t, "1", w.Header().Get("Retry-After"))
			assert.Zero(t, spy.reads)
			assert.False(t, called)
			var envelope struct {
				Type  string         `json:"type"`
				Error map[string]any `json:"error"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
			switch {
			case strings.HasPrefix(path, "/v1/messages"):
				assert.Equal(t, "error", envelope.Type)
				assert.Equal(t, "rate_limit_error", envelope.Error["type"])
				assert.Equal(t, string(largeRequestConcurrencyError), envelope.Error["code"])
			case strings.Contains(path, "/models/"):
				assert.Equal(t, float64(http.StatusTooManyRequests), envelope.Error["code"])
				assert.Equal(t, "RESOURCE_EXHAUSTED", envelope.Error["status"])
			default:
				assert.Equal(t, string(largeRequestConcurrencyError), envelope.Error["code"])
			}
		})
	}
}

func TestRelayRequestAdmissionAuthenticationRunsBeforeCapacity(t *testing.T) {
	limiter := admission.NewLargeRequestLimiter(1, 4)
	release, allowed := limiter.TryAcquire(admissionRequest("/", 4))
	require.True(t, allowed)
	defer release()
	router := gin.New()
	router.POST("/", func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) },
		relayRequestAdmission(limiter), func(c *gin.Context) { t.Error("handler reached") })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, admissionRequest("/", 4))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Empty(t, w.Header().Get("Retry-After"))
}

func TestRelayRequestAdmissionDisabledByDefault(t *testing.T) {
	t.Setenv("RELAY_LARGE_REQUEST_MAX_CONCURRENCY", "")
	t.Setenv("RELAY_LARGE_REQUEST_THRESHOLD_MB", "4")
	router := gin.New()
	router.POST("/", RelayRequestAdmission(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for i := 0; i < 4; i++ {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, admissionRequest("/", 28<<20))
		assert.Equal(t, http.StatusNoContent, w.Code)
	}
}

func TestRelayRequestAdmissionSharedAcrossRoutesAndReleasesOnCancel(t *testing.T) {
	t.Setenv("RELAY_LARGE_REQUEST_MAX_CONCURRENCY", "2")
	t.Setenv("RELAY_LARGE_REQUEST_THRESHOLD_MB", "4")
	guard := RelayRequestAdmission()
	entered := make(chan struct{}, 2)
	finished := make(chan struct{}, 2)
	router := gin.New()
	handler := func(c *gin.Context) {
		if c.GetHeader("X-Test-Hold") == "1" {
			entered <- struct{}{}
			<-c.Request.Context().Done()
		}
		c.Status(http.StatusNoContent)
	}
	router.POST("/a", guard, handler)
	router.POST("/b", guard, handler)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, path := range []string{"/a", "/b"} {
		r := admissionRequest(path, 4<<20).WithContext(ctx)
		r.Header.Set("X-Test-Hold", "1")
		go func() { router.ServeHTTP(httptest.NewRecorder(), r); finished <- struct{}{} }()
	}
	<-entered
	<-entered
	full := httptest.NewRecorder()
	router.ServeHTTP(full, admissionRequest("/b", -1))
	assert.Equal(t, http.StatusTooManyRequests, full.Code)
	small := httptest.NewRecorder()
	router.ServeHTTP(small, admissionRequest("/b", (4<<20)-1))
	assert.Equal(t, http.StatusNoContent, small.Code)
	cancel()
	<-finished
	<-finished
	after := httptest.NewRecorder()
	router.ServeHTTP(after, admissionRequest("/a", 28<<20))
	assert.Equal(t, http.StatusNoContent, after.Code)
}

func TestRelayRequestAdmissionReleasesOnPanic(t *testing.T) {
	limiter := admission.NewLargeRequestLimiter(1, 4)
	router := gin.New()
	router.Use(gin.RecoveryWithWriter(io.Discard))
	router.POST("/", relayRequestAdmission(limiter), func(c *gin.Context) {
		if c.GetHeader("X-Test-Panic") == "1" {
			panic("test panic")
		}
		c.Status(http.StatusNoContent)
	})
	first := admissionRequest("/", 4)
	first.Header.Set("X-Test-Panic", "1")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, first)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	next := httptest.NewRecorder()
	router.ServeHTTP(next, admissionRequest("/", 4))
	assert.Equal(t, http.StatusNoContent, next.Code)
}

func TestRelayRequestAdmissionDoesNotRunAlreadyCanceledRequest(t *testing.T) {
	limiter := admission.NewLargeRequestLimiter(1, 4)
	router := gin.New()
	router.POST("/", relayRequestAdmission(limiter), func(c *gin.Context) { t.Error("canceled request dispatched") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	router.ServeHTTP(httptest.NewRecorder(), admissionRequest("/", 4).WithContext(ctx))
	release, ok := limiter.TryAcquire(admissionRequest("/", 4))
	require.True(t, ok)
	release()
}

func TestRelayRequestAdmissionInvalidThresholdFallsBackWithoutOverflow(t *testing.T) {
	for _, value := range []string{"0", "-1", "9223372036854775807", "not-a-number"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("RELAY_LARGE_REQUEST_MAX_CONCURRENCY", "1")
			t.Setenv("RELAY_LARGE_REQUEST_THRESHOLD_MB", value)
			guard := RelayRequestAdmission()
			entered := make(chan struct{})
			finish := make(chan struct{})
			stop := sync.OnceFunc(func() { close(finish) })
			defer stop()
			done := make(chan struct{})
			router := gin.New()
			router.POST("/", guard, func(c *gin.Context) {
				if c.GetHeader("X-Test-Hold") == "1" {
					close(entered)
					<-finish
				}
				c.Status(http.StatusNoContent)
			})
			r := admissionRequest("/", 4<<20)
			r.Header.Set("X-Test-Hold", "1")
			go func() { defer close(done); router.ServeHTTP(httptest.NewRecorder(), r) }()
			<-entered
			w := httptest.NewRecorder()
			router.ServeHTTP(w, admissionRequest("/", 4<<20))
			assert.Equal(t, http.StatusTooManyRequests, w.Code)
			stop()
			<-done
		})
	}
}
