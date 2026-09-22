package service

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"

	"github.com/gin-gonic/gin"
)

// GetFileTypeFromUrl 获取文件类型，返回 mime type， 例如 image/jpeg, image/png, image/gif, image/bmp, image/tiff, application/pdf
// 如果获取失败，返回 application/octet-stream
func GetFileTypeFromUrl(c *gin.Context, url string, reason ...string) (string, error) {
	response, err := DoDownloadRequest(url, []string{"get_mime_type", strings.Join(reason, ", ")}...)
	if err != nil {
		common.SysLog(fmt.Sprintf("fail to get file type from url: %s, error: %s", url, err.Error()))
		return "", err
	}
	defer response.Body.Close()

	if response.StatusCode != 200 {
		logger.LogError(c, fmt.Sprintf("failed to download file from %s, status code: %d", url, response.StatusCode))
		return "", fmt.Errorf("failed to download file, status code: %d", response.StatusCode)
	}

	if headerType := strings.TrimSpace(response.Header.Get("Content-Type")); headerType != "" {
		if i := strings.Index(headerType, ";"); i != -1 {
			headerType = headerType[:i]
		}
		if headerType != "application/octet-stream" {
			return headerType, nil
		}
	}

	if cd := response.Header.Get("Content-Disposition"); cd != "" {
		parts := strings.Split(cd, ";")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(strings.ToLower(part), "filename=") {
				name := strings.TrimSpace(strings.TrimPrefix(part, "filename="))
				if len(name) > 2 && name[0] == '"' && name[len(name)-1] == '"' {
					name = name[1 : len(name)-1]
				}
				if dot := strings.LastIndex(name, "."); dot != -1 && dot+1 < len(name) {
					ext := strings.ToLower(name[dot+1:])
					if ext != "" {
						mt := GetMimeTypeByExtension(ext)
						if mt != "application/octet-stream" {
							return mt, nil
						}
					}
				}
				break
			}
		}
	}

	cleanedURL := url
	if q := strings.Index(cleanedURL, "?"); q != -1 {
		cleanedURL = cleanedURL[:q]
	}
	if slash := strings.LastIndex(cleanedURL, "/"); slash != -1 && slash+1 < len(cleanedURL) {
		last := cleanedURL[slash+1:]
		if dot := strings.LastIndex(last, "."); dot != -1 && dot+1 < len(last) {
			ext := strings.ToLower(last[dot+1:])
			if ext != "" {
				mt := GetMimeTypeByExtension(ext)
				if mt != "application/octet-stream" {
					return mt, nil
				}
			}
		}
	}

	var readData []byte
	limits := []int{512, 8 * 1024, 24 * 1024, 64 * 1024}
	for _, limit := range limits {
		logger.LogDebug(c, "Trying to read %d bytes to determine file type", limit)
		if len(readData) < limit {
			need := limit - len(readData)
			tmp := make([]byte, need)
			n, _ := io.ReadFull(response.Body, tmp)
			if n > 0 {
				readData = append(readData, tmp[:n]...)
			}
		}

		if len(readData) == 0 {
			continue
		}

		sniffed := http.DetectContentType(readData)
		if sniffed != "" && sniffed != "application/octet-stream" {
			return sniffed, nil
		}

		// Try HEIF/HEIC detection (Go standard library doesn't recognize it)
		if heifMime := detectHEIF(readData); heifMime != "" {
			return heifMime, nil
		}

		if _, format, err := image.DecodeConfig(bytes.NewReader(readData)); err == nil {
			switch strings.ToLower(format) {
			case "jpeg", "jpg":
				return "image/jpeg", nil
			case "png":
				return "image/png", nil
			case "gif":
				return "image/gif", nil
			case "bmp":
				return "image/bmp", nil
			case "tiff":
				return "image/tiff", nil
			default:
				if format != "" {
					return "image/" + strings.ToLower(format), nil
				}
			}
		}
	}

	// Fallback
	return "application/octet-stream", nil
}

func GetMimeTypeByExtension(ext string) string {
	// Convert to lowercase for case-insensitive comparison
	ext = strings.ToLower(ext)
	switch ext {
	// Text files
	case "txt", "md", "markdown", "csv", "json", "xml", "html", "htm":
		return "text/plain"

	// Image files
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "jfif":
		return "image/jpeg"
	case "heic":
		return "image/heic"
	case "heif":
		return "image/heif"

	// Audio files
	case "mp3":
		return "audio/mp3"
	case "wav":
		return "audio/wav"
	case "mpeg":
		return "audio/mpeg"

	// Video files
	case "mp4":
		return "video/mp4"
	case "wmv":
		return "video/wmv"
	case "flv":
		return "video/flv"
	case "mov":
		return "video/mov"
	case "mpg":
		return "video/mpg"
	case "avi":
		return "video/avi"
	case "mpegps":
		return "video/mpegps"

	// Document files
	case "pdf":
		return "application/pdf"

	default:
		return "application/octet-stream" // Default for unknown types
	}
}
