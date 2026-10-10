package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deliveryResultWriter struct {
	*httptest.ResponseRecorder
	written int
	err     error
	data    []byte
	flushes int
}

func (w *deliveryResultWriter) Write(data []byte) (int, error) {
	w.data = append(w.data, data...)
	return w.written, w.err
}

func (w *deliveryResultWriter) Flush() { w.flushes++ }

func TestWriteResponseBytesPreservesDeliveryContract(t *testing.T) {
	previousGinMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousGinMode) })
	data := []byte("response")
	for _, tc := range []struct {
		name        string
		written     int
		err         error
		withoutInfo bool
		failed      bool
	}{
		{name: "complete write", written: len(data)},
		{name: "short write keeps nil error", written: len(data) / 2, failed: true},
		{name: "partial write keeps error", written: len(data) / 2, err: io.ErrClosedPipe, failed: true},
		{name: "full write with error", written: len(data), err: io.ErrClosedPipe, failed: true},
		{name: "writer without relay metadata", err: io.ErrClosedPipe, withoutInfo: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &deliveryResultWriter{ResponseRecorder: httptest.NewRecorder(), written: tc.written, err: tc.err}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{ResponseCompleted: true}
			if !tc.withoutInfo {
				common.SetContextKey(c, constant.ContextKeyRelayInfo, info)
			}
			originalWriter := c.Writer
			c.Status(http.StatusAccepted)
			c.Writer.Header().Set("Content-Type", "application/json")
			c.Writer.Header().Set("X-Delivery", "unchanged")
			previousHeaders := c.Writer.Header().Clone()

			written, err := WriteResponseBytes(c, data)
			assert.Equal(t, tc.written, written)
			if tc.err == nil {
				require.NoError(t, err, "the helper must not turn a short write into a returned API error")
			} else {
				require.Same(t, tc.err, err, "the helper must return the original writer error")
			}
			assert.Equal(t, data, writer.data)
			assert.Equal(t, previousHeaders, c.Writer.Header())
			assert.Equal(t, http.StatusAccepted, writer.Code)
			assert.Zero(t, writer.flushes)
			require.Same(t, originalWriter, c.Writer)
			assert.Equal(t, tc.failed, info.ResponseFailed)
			assert.True(t, info.ResponseCompleted, "the helper must not reset the adaptor's response state")
			assert.Empty(t, c.Errors, "failure metadata must not add an API error or affect retry handling")
		})
	}
}
