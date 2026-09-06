package middleware

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBodyStorageCleanupRequestLifecycle(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })

	for _, tc := range []struct {
		name   string
		status int
	}{
		{name: "return", status: http.StatusOK},
		{name: "abort", status: http.StatusBadRequest},
		{name: "panic", status: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(key, tempDir)
			}
			require.Equal(t, tempDir, os.TempDir())

			previousFileLimit, previousBodyLimit := constant.MaxFileDownloadMB, constant.MaxRequestBodyMB
			previousCache := common.GetDiskCacheConfig()
			t.Cleanup(func() {
				constant.MaxFileDownloadMB, constant.MaxRequestBodyMB = previousFileLimit, previousBodyLimit
				common.SetDiskCacheConfig(previousCache)
			})
			constant.MaxFileDownloadMB, constant.MaxRequestBodyMB = 1, 4
			common.SetDiskCacheConfig(common.DiskCacheConfig{
				Enabled: true, ThresholdMB: 1, MaxSizeMB: 16, Path: filepath.Join(tempDir, "body-cache"),
			})

			payload := bytes.Repeat([]byte("a"), 2<<20)
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("file", "audio.wav")
			require.NoError(t, err)
			_, err = part.Write(payload)
			require.NoError(t, err)
			require.NoError(t, writer.Close())

			var storage common.BodyStorage
			var spillPaths []string
			downstreamCalled := false
			router := gin.New()
			// Match production ordering: recovery wraps the cleanup middleware.
			router.Use(gin.RecoveryWithWriter(io.Discard), BodyStorageCleanup())
			router.POST("/", func(c *gin.Context) {
				storage, err = common.GetBodyStorage(c)
				require.NoError(t, err)
				t.Cleanup(func() { _ = storage.Close() })
				require.True(t, storage.IsDisk(), "the request body must spill too")

				var forms []*multipart.Form
				for range 2 {
					form, err := common.ParseMultipartFormReusable(c)
					require.NoError(t, err)
					t.Cleanup(func() { _ = form.RemoveAll() })
					forms = append(forms, form)
				}
				require.Nil(t, c.Request.MultipartForm, "net/http does not own these forms")

				// Both parse passes must remain readable until request handling ends.
				for _, form := range forms {
					require.Len(t, form.File["file"], 1)
					file, err := form.File["file"][0].Open()
					require.NoError(t, err)
					t.Cleanup(func() { _ = file.Close() })
					diskFile, ok := file.(*os.File)
					require.True(t, ok, "multipart content must exceed the 1 MiB memory limit")
					spillPaths = append(spillPaths, diskFile.Name())
					actual, err := io.ReadAll(file)
					require.NoError(t, err)
					require.NoError(t, file.Close())
					require.Equal(t, payload, actual)
				}
				require.NotEqual(t, spillPaths[0], spillPaths[1], "each parse creates its own spill")
				_, err = storage.Seek(0, io.SeekStart)
				require.NoError(t, err, "body storage must remain open during handling")

				switch tc.name {
				case "abort":
					c.AbortWithStatus(http.StatusBadRequest)
				case "panic":
					panic("request handler failed")
				}
			}, func(c *gin.Context) {
				downstreamCalled = true
				c.Status(http.StatusOK)
			})

			request := httptest.NewRequest(http.MethodPost, "/", &body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			assert.Equal(t, tc.status, response.Code)
			assert.Equal(t, tc.name == "return", downstreamCalled)
			require.Len(t, spillPaths, 2)
			for _, path := range spillPaths {
				assert.NoFileExists(t, path, "request completion must remove every multipart spill")
			}
			_, err = storage.Seek(0, io.SeekStart)
			assert.ErrorIs(t, err, common.ErrStorageClosed, "request completion must close body storage")
		})
	}
}
