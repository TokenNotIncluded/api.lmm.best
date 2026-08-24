package common

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

var imageUploadPNG = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
var imageUploadJPEG = []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 'J', 'F', 'I', 'F'}
var imageUploadWebP = []byte{'R', 'I', 'F', 'F', 4, 0, 0, 0, 'W', 'E', 'B', 'P', 'V', 'P', '8', ' '}
var imageUploadHEIC = []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c'}

func imageUploadForm(t *testing.T, files map[string][]struct {
	name string
	data []byte
}) *multipart.Form {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for field, entries := range files {
		for _, entry := range entries {
			part, err := writer.CreateFormFile(field, entry.name)
			require.NoError(t, err)
			_, err = part.Write(entry.data)
			require.NoError(t, err)
		}
	}
	require.NoError(t, writer.Close())
	request := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	require.NoError(t, request.ParseMultipartForm(1<<20))
	t.Cleanup(func() { _ = request.MultipartForm.RemoveAll() })
	return request.MultipartForm
}

func TestDetectSupportedImageUploadMediaTypeUsesBytesNotFilename(t *testing.T) {
	for _, test := range []struct {
		name      string
		filename  string
		content   []byte
		mediaType string
		wantError string
	}{
		{name: "extensionless PNG", filename: "blob", content: imageUploadPNG, mediaType: "image/png"},
		{name: "JPEG with wrong extension", filename: "image.heic", content: imageUploadJPEG, mediaType: "image/jpeg"},
		{name: "WebP uppercase", filename: "IMAGE.WEBP", content: imageUploadWebP, mediaType: "image/webp"},
		{name: "fake PNG", filename: "image.png", content: []byte("not an image"), wantError: "unsupported image content type"},
		{name: "HEIC named PNG", filename: "image.png", content: imageUploadHEIC, wantError: "unsupported image content type"},
	} {
		t.Run(test.name, func(t *testing.T) {
			form := imageUploadForm(t, map[string][]struct {
				name string
				data []byte
			}{"image": {{name: test.filename, data: test.content}}})
			mediaType, err := DetectSupportedImageUploadMediaType(form.File["image"][0])
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				require.Empty(t, mediaType)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.mediaType, mediaType)
		})
	}
}

func TestValidateImageEditMultipartFilesCoversImageArraysAndMask(t *testing.T) {
	valid := imageUploadForm(t, map[string][]struct {
		name string
		data []byte
	}{
		"image":   {{name: "blob", data: imageUploadPNG}},
		"image[]": {{name: "second.bin", data: imageUploadJPEG}},
		"mask":    {{name: "mask.dat", data: imageUploadWebP}},
	})
	require.NoError(t, ValidateImageEditMultipartFiles(valid))

	for _, test := range []struct {
		name  string
		files map[string][]struct {
			name string
			data []byte
		}
		wantError string
	}{
		{name: "missing image", files: map[string][]struct {
			name string
			data []byte
		}{"mask": {{name: "mask.png", data: imageUploadPNG}}}, wantError: "image file is required"},
		{name: "image array HEIC", files: map[string][]struct {
			name string
			data []byte
		}{"image[]": {{name: "blob", data: imageUploadHEIC}}}, wantError: "image file 1"},
		{name: "mask HEIC", files: map[string][]struct {
			name string
			data []byte
		}{
			"image": {{name: "image.png", data: imageUploadPNG}},
			"mask":  {{name: "mask.png", data: imageUploadHEIC}},
		}, wantError: "mask file 1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorContains(t, ValidateImageEditMultipartFiles(imageUploadForm(t, test.files)), test.wantError)
		})
	}
}
