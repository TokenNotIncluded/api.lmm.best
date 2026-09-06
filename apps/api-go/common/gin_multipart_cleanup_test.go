package common

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildMultipartUpload returns a multipart body whose single file part is large
// enough to force multipart.Reader.ReadForm to spill it to a temporary file.
func buildMultipartUpload(t *testing.T, fileSize int) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "whisper-1"))
	part, err := writer.CreateFormFile("file", "audio.wav")
	require.NoError(t, err)
	_, err = part.Write([]byte(strings.Repeat("a", fileSize)))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return &body, writer.FormDataContentType()
}

// countTempSpillFiles counts the multipart spill files Go creates in dir.
func countTempSpillFiles(t *testing.T, dir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "multipart-*"))
	require.NoError(t, err)
	return len(matches)
}

// newMultipartTestContext wires a gin context over a multipart upload with the
// multipart memory limit lowered so the file part is guaranteed to spill.
func newMultipartTestContext(t *testing.T, fileSize int) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)

	previousLimit := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 0 // multipartMemoryLimit falls back to 32 MiB
	t.Cleanup(func() { constant.MaxFileDownloadMB = previousLimit })

	body, contentType := buildMultipartUpload(t, fileSize)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/audio/transcriptions", body)
	c.Request.Header.Set("Content-Type", contentType)
	// Release the replayable body storage the same way BodyStorageCleanup does,
	// so the spill directory has no open handles left when the test tears down.
	t.Cleanup(func() { CleanupBodyStorage(c) })
	return c
}

// TestParseMultipartFormReusableReleasesSpilledTempFiles locks in the fix for
// the disk-exhaustion leak: a parsed form that is never attached to
// Request.MultipartForm is still released when the request ends, because
// net/http only auto-removes the form it owns.
func TestParseMultipartFormReusableReleasesSpilledTempFiles(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir) // honoured by os.TempDir on unix
	t.Setenv("TMP", tempDir)    // honoured by os.TempDir on windows
	t.Setenv("TEMP", tempDir)
	require.Equal(t, tempDir, os.TempDir(), "the test must control the spill directory")

	// 1 MiB over the 32 MiB fallback memory limit guarantees a disk spill.
	c := newMultipartTestContext(t, (32<<20)+(1<<20))

	form, err := ParseMultipartFormReusable(c)
	require.NoError(t, err)
	require.NotNil(t, form)
	require.Len(t, form.File["file"], 1)
	require.Equal(t, 1, countTempSpillFiles(t, tempDir), "the oversized part must spill to disk")

	// The vulnerable code path never assigns the form to the request, so the
	// standard library cleanup in (*response).finishRequest cannot see it.
	require.Nil(t, c.Request.MultipartForm)

	CleanupMultipartForms(c)
	assert.Equal(t, 0, countTempSpillFiles(t, tempDir), "request teardown must remove multipart spill files")
}

// TestParseMultipartFormReusableReleasesEveryParsePass covers the audio relay
// shape, where one request parses the same body more than once and each pass
// creates an independent spill file.
func TestParseMultipartFormReusableReleasesEveryParsePass(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	t.Setenv("TMP", tempDir)
	t.Setenv("TEMP", tempDir)
	require.Equal(t, tempDir, os.TempDir(), "the test must control the spill directory")

	c := newMultipartTestContext(t, (32<<20)+(1<<20))

	first, err := ParseMultipartFormReusable(c)
	require.NoError(t, err)
	require.NotNil(t, first)
	second, err := ParseMultipartFormReusable(c)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, 2, countTempSpillFiles(t, tempDir), "each parse pass spills its own copy")

	CleanupMultipartForms(c)
	assert.Equal(t, 0, countTempSpillFiles(t, tempDir), "no spill file may survive the request")
}

// TestCleanupMultipartFormsIsIdempotent guards the call sites that already run
// their own RemoveAll, or that attach the form so net/http removes it too.
func TestCleanupMultipartFormsIsIdempotent(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	t.Setenv("TMP", tempDir)
	t.Setenv("TEMP", tempDir)
	require.Equal(t, tempDir, os.TempDir(), "the test must control the spill directory")

	c := newMultipartTestContext(t, (32<<20)+(1<<20))

	form, err := ParseMultipartFormReusable(c)
	require.NoError(t, err)
	require.NoError(t, form.RemoveAll(), "a caller may release the form itself")

	CleanupMultipartForms(c)
	CleanupMultipartForms(c)
	assert.Equal(t, 0, countTempSpillFiles(t, tempDir))
}

// TestCleanupMultipartFormsWithoutParsedForm keeps the middleware cheap and
// safe on the majority of requests, which never parse a multipart body.
func TestCleanupMultipartFormsWithoutParsedForm(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))

	assert.NotPanics(t, func() { CleanupMultipartForms(c) })
	assert.NotPanics(t, func() { CleanupMultipartForms(nil) })
}
