package common

import (
	"bytes"
	"io"
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

func buildMultipartUpload(t *testing.T, fileSize int) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "whisper-1"))
	part, err := writer.CreateFormFile("file", "audio.wav")
	require.NoError(t, err)
	_, err = io.Copy(part, strings.NewReader(strings.Repeat("a", fileSize)))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return &body, writer.FormDataContentType()
}

func countTempSpillFiles(t *testing.T, dir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "multipart-*"))
	require.NoError(t, err)
	return len(matches)
}

func newMultipartTestContext(t *testing.T) (*gin.Context, string) {
	t.Helper()
	tempDir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, tempDir)
	}
	require.Equal(t, tempDir, os.TempDir())

	previousLimit := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 1
	t.Cleanup(func() { constant.MaxFileDownloadMB = previousLimit })

	body, contentType := buildMultipartUpload(t, 2<<20)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/audio/transcriptions", body)
	c.Request.Header.Set("Content-Type", contentType)
	t.Cleanup(func() {
		CleanupBodyStorage(c)
		CleanupMultipartForms(c)
	})
	return c, tempDir
}

func TestParseMultipartFormReusableReleasesSpilledTempFiles(t *testing.T) {
	c, tempDir := newMultipartTestContext(t)

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

func TestParseMultipartFormReusableReleasesEveryParsePass(t *testing.T) {
	c, tempDir := newMultipartTestContext(t)

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

func TestCleanupMultipartFormsIsIdempotent(t *testing.T) {
	c, tempDir := newMultipartTestContext(t)

	form, err := ParseMultipartFormReusable(c)
	require.NoError(t, err)
	require.NoError(t, form.RemoveAll(), "a caller may release the form itself")

	CleanupMultipartForms(c)
	CleanupMultipartForms(c)
	assert.Equal(t, 0, countTempSpillFiles(t, tempDir))
}

func TestCleanupMultipartFormsWithoutParsedForm(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))

	assert.NotPanics(t, func() { CleanupMultipartForms(c) })
	assert.NotPanics(t, func() { CleanupMultipartForms(nil) })
}
