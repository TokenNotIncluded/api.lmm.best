package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestShouldCopyUpstreamHeaderRejectsSensitiveHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	assert.False(t, ShouldCopyUpstreamHeader(c, "Set-Cookie", []string{"sid=abc"}))
	assert.False(t, ShouldCopyUpstreamHeader(c, "set-cookie", []string{"sid=abc"}))
	assert.False(t, ShouldCopyUpstreamHeader(c, "Connection", []string{"keep-alive"}))
	assert.False(t, ShouldCopyUpstreamHeader(c, "Transfer-Encoding", []string{"chunked"}))
	assert.True(t, ShouldCopyUpstreamHeader(c, "Content-Type", []string{"audio/mpeg"}))
	assert.True(t, ShouldCopyUpstreamHeader(c, "ETag", []string{`"abc"`}))
}
