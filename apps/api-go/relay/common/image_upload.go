package common

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
)

const imageUploadSignatureLimit = 512

// DetectSupportedImageUploadMediaType identifies an image from a bounded byte
// prefix. Filenames and client-provided Content-Type values are not trusted.
func DetectSupportedImageUploadMediaType(fileHeader *multipart.FileHeader) (string, error) {
	if fileHeader == nil {
		return "", errors.New("image file is missing")
	}
	file, err := fileHeader.Open()
	if err != nil {
		return "", fmt.Errorf("open image file: %w", err)
	}
	defer file.Close()

	signature := make([]byte, imageUploadSignatureLimit)
	read, err := io.ReadFull(file, signature)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("read image signature: %w", err)
	}
	if read == 0 {
		return "", errors.New("image file is empty")
	}

	mediaType := http.DetectContentType(signature[:read])
	switch mediaType {
	case "image/jpeg", "image/png", "image/webp":
		return mediaType, nil
	default:
		return "", fmt.Errorf("unsupported image content type %q; supported formats: JPEG, PNG, WebP", mediaType)
	}
}

// ValidateImageEditMultipartFiles validates every image or mask that the
// OpenAI-compatible image-edit adaptor can forward.
func ValidateImageEditMultipartFiles(form *multipart.Form) error {
	if form == nil {
		return errors.New("multipart image edit form is missing")
	}

	imageFiles := append([]*multipart.FileHeader(nil), form.File["image"]...)
	imageFiles = append(imageFiles, form.File["image[]"]...)
	imageArrayFields := make([]string, 0)
	for field := range form.File {
		if strings.HasPrefix(field, "image[") && field != "image[]" {
			imageArrayFields = append(imageArrayFields, field)
		}
	}
	sort.Strings(imageArrayFields)
	for _, field := range imageArrayFields {
		imageFiles = append(imageFiles, form.File[field]...)
	}
	if len(imageFiles) == 0 {
		return errors.New("image file is required")
	}
	for index, fileHeader := range imageFiles {
		if _, err := DetectSupportedImageUploadMediaType(fileHeader); err != nil {
			return fmt.Errorf("image file %d: %w", index+1, err)
		}
	}
	for index, fileHeader := range form.File["mask"] {
		if _, err := DetectSupportedImageUploadMediaType(fileHeader); err != nil {
			return fmt.Errorf("mask file %d: %w", index+1, err)
		}
	}
	return nil
}
