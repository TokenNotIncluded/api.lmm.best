package middleware

import (
	"compress/gzip"
	"io"
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
)

type readCloser struct {
	io.Reader
	closeFn func() error
}

func (rc *readCloser) Close() error {
	if rc.closeFn != nil {
		return rc.closeFn()
	}
	return nil
}

func DecompressRequestMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil || c.Request.Method == http.MethodGet ||
			(c.Request.Body == http.NoBody && c.GetHeader("Content-Encoding") == "") {
			c.Next()
			return
		}
		maxMB := constant.MaxRequestBodyMB
		if maxMB <= 0 {
			maxMB = 32
		}
		maxBytes := int64(maxMB) << 20

		// Bound the compressed stream before handing it to a decoder. The
		// post-decompression wrapper below still protects against expansion,
		// while this prevents arbitrarily large compressed member streams from
		// being read even when their decoded output is small.
		origBody := http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		wrapMaxBytes := func(body io.ReadCloser) io.ReadCloser {
			return http.MaxBytesReader(c.Writer, body, maxBytes)
		}
		decompressed := false

		switch c.GetHeader("Content-Encoding") {
		case "gzip":
			gzipReader, err := gzip.NewReader(origBody)
			if err != nil {
				_ = origBody.Close()
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}
			// Release decoder resources even if authentication/admission rejects
			// the request before a body consumer takes ownership.
			defer gzipReader.Close()
			// Replace the request body with the decompressed data, and enforce a max size (post-decompression).
			c.Request.Body = wrapMaxBytes(&readCloser{
				Reader: gzipReader,
				closeFn: func() error {
					_ = gzipReader.Close()
					return origBody.Close()
				},
			})
			decompressed = true
		case "br":
			reader := brotli.NewReader(origBody)
			c.Request.Body = wrapMaxBytes(&readCloser{
				Reader: reader,
				closeFn: func() error {
					return origBody.Close()
				},
			})
			decompressed = true
		case "zstd":
			// Avoid eager asynchronous decompression before authentication and
			// admission, and bound per-request decoder buffers/goroutines.
			reader, err := zstd.NewReader(origBody, zstd.WithDecoderConcurrency(1))
			if err != nil {
				_ = origBody.Close()
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}
			defer reader.Close()
			c.Request.Body = wrapMaxBytes(&readCloser{
				Reader: reader,
				closeFn: func() error {
					err := origBody.Close()
					reader.Close()
					return err
				},
			})
			decompressed = true
		default:
			// Even for uncompressed bodies, enforce a max size to avoid huge request allocations.
			c.Request.Body = wrapMaxBytes(origBody)
		}

		if decompressed {
			c.Request.Header.Del("Content-Encoding")
			// The wire length describes compressed bytes, not this reader. A
			// small compressed body must not bypass admission/spill thresholds
			// and grow into a large io.ReadAll allocation after decompression.
			c.Request.ContentLength = -1
			c.Request.Header.Del("Content-Length")
		}

		// net/http still owns the original wire body on early rejection. Do
		// not drain/close it here before the error response can be flushed;
		// only the decoder resources above are owned by this middleware.
		c.Next()
	}
}
