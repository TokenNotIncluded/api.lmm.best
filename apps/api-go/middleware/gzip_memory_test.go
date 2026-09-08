package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/pkg/admission"
	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func compressMemoryTestBody(t *testing.T, encoding string, payload []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	var writer io.WriteCloser
	switch encoding {
	case "gzip":
		writer = gzip.NewWriter(&buffer)
	case "br":
		writer = brotli.NewWriter(&buffer)
	case "zstd":
		var err error
		writer, err = zstd.NewWriter(&buffer, zstd.WithEncoderConcurrency(1))
		require.NoError(t, err)
	default:
		return payload
	}
	_, err := writer.Write(payload)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}

type memoryTestReadCloser struct {
	io.Reader
	closes int
}

func (r *memoryTestReadCloser) Close() error { r.closes++; return nil }

func TestDecompressionInvalidatesWireLengthAndSpills(t *testing.T) {
	previousConfig := common.GetDiskCacheConfig()
	previousMax := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 4
	t.Cleanup(func() { common.SetDiskCacheConfig(previousConfig); constant.MaxRequestBodyMB = previousMax })
	payload := bytes.Repeat([]byte("x"), 2<<20)
	for _, encoding := range []string{"gzip", "br", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			common.SetDiskCacheConfig(common.DiskCacheConfig{Enabled: true, ThresholdMB: 1, MaxSizeMB: 16, Path: t.TempDir()})
			compressed := compressMemoryTestBody(t, encoding, payload)
			require.Less(t, len(compressed), 1<<20)
			body := &memoryTestReadCloser{Reader: bytes.NewReader(compressed)}
			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(compressed))
			r.Body = body
			r.Header.Set("Content-Encoding", encoding)
			r.Header.Set("Content-Length", strconv.Itoa(len(compressed)))
			router := gin.New()
			router.POST("/", DecompressRequestMiddleware(), BodyStorageCleanup(), func(c *gin.Context) {
				assert.EqualValues(t, -1, c.Request.ContentLength)
				assert.Empty(t, c.GetHeader("Content-Length"))
				assert.Empty(t, c.GetHeader("Content-Encoding"))
				storage, err := common.GetBodyStorage(c)
				require.NoError(t, err)
				assert.True(t, storage.IsDisk(), "small wire length must not force decompressed data into RAM")
				got, err := io.ReadAll(storage)
				require.NoError(t, err)
				assert.Equal(t, payload, got)
				c.Status(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			assert.Equal(t, http.StatusNoContent, w.Code)
			assert.Equal(t, 1, body.closes, "only the body consumer closes the source")
		})
	}
}

func TestDecompressionLeavesUnreadWireBodyToServerOnEarlyReject(t *testing.T) {
	for _, encoding := range []string{"", "gzip", "br", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			compressed := compressMemoryTestBody(t, encoding, []byte("never dispatched"))
			body := &memoryTestReadCloser{Reader: bytes.NewReader(compressed)}
			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(compressed))
			r.Body = body
			r.Header.Set("Content-Encoding", encoding)
			router := gin.New()
			router.POST("/", DecompressRequestMiddleware(), func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Zero(t, body.closes, "do not drain the wire body before flushing rejection")
			require.NoError(t, body.Close()) // the HTTP server owns the original body
		})
	}
}

func TestCompressedRequestCannotBypassLargeRequestAdmission(t *testing.T) {
	limiter := admission.NewLargeRequestLimiter(1, 1<<20)
	release, ok := limiter.TryAcquire(admissionRequest("/", 1<<20))
	require.True(t, ok)
	defer release()
	for _, encoding := range []string{"gzip", "br", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			compressed := compressMemoryTestBody(t, encoding, bytes.Repeat([]byte("x"), 2<<20))
			body := &memoryTestReadCloser{Reader: bytes.NewReader(compressed)}
			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(compressed))
			r.Body = body
			r.Header.Set("Content-Encoding", encoding)
			router := gin.New()
			router.POST("/", DecompressRequestMiddleware(), relayRequestAdmission(limiter), func(c *gin.Context) { t.Error("compressed request bypassed admission") })
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			assert.Equal(t, http.StatusTooManyRequests, w.Code)
			assert.Zero(t, body.closes, "admission must not wait for a slow sender")
			require.NoError(t, body.Close())
		})
	}
}

func TestZstdDoesNotPrefetchBeforeAuthentication(t *testing.T) {
	spy := &admissionReadSpy{}
	body := &memoryTestReadCloser{Reader: spy}
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("unread frame")))
	r.Body = body
	r.Header.Set("Content-Encoding", "zstd")
	router := gin.New()
	router.POST("/", DecompressRequestMiddleware(), func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Zero(t, spy.reads, "zstd must not asynchronously decode before admission")
	assert.Zero(t, body.closes)
	require.NoError(t, body.Close())
}

func TestEmptyRequestStillBypassesAdmissionAfterDecompressionMiddleware(t *testing.T) {
	limiter := admission.NewLargeRequestLimiter(1, 4)
	release, ok := limiter.TryAcquire(admissionRequest("/", 4))
	require.True(t, ok)
	defer release()
	router := gin.New()
	router.POST("/", DecompressRequestMiddleware(), relayRequestAdmission(limiter), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))
	assert.Equal(t, http.StatusNoContent, w.Code)
}
