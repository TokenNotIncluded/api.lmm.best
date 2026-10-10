package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"

	"github.com/gin-gonic/gin"
	"golang.org/x/image/webp"
)

// FileService 统一的文件处理服务
// 提供文件下载、解码、缓存等功能的统一入口

// getContextCacheKey 生成 URL context 缓存的 key
func getContextCacheKey(url string) string {
	return fmt.Sprintf("file_cache_%s", common.GenerateHMAC(url))
}

// getBase64ContextCacheKey 生成 base64 context 缓存的 key
// Hash the complete Base64 input; equal lengths and prefixes are not identities.
func getBase64ContextCacheKey(data string, mimeType string) string {
	digest := base64ContentDigest(data)
	keyMaterial := fmt.Sprintf("%d:%s:%x", len(data), mimeType, digest)
	return fmt.Sprintf("b64_cache_%s", common.GenerateHMAC(keyMaterial))
}

// base64ContentDigest hashes the full string in bounded chunks, without a
// temporary byte slice as large as the input.
func base64ContentDigest(data string) [sha256.Size]byte {
	hasher := sha256.New()
	var chunk [4096]byte
	for remaining := data; len(remaining) > 0; {
		n := copy(chunk[:], remaining)
		_, _ = hasher.Write(chunk[:n])
		remaining = remaining[n:]
	}
	var digest [sha256.Size]byte
	hasher.Sum(digest[:0])
	return digest
}

// LoadFileSource 加载文件源数据
// Sources and request-local cache registrations must be synchronized. A file
// cannot outlive its request by re-registering after terminal cleanup.
func LoadFileSource(c *gin.Context, source types.FileSource, reason ...string) (*types.CachedFileData, error) {
	if source == nil {
		return nil, fmt.Errorf("file source is nil")
	}
	if common.DebugEnabled {
		logger.LogDebug(c, "LoadFileSource starting for: %s", source.GetIdentifier())
	}

	// The source's mutex also protects the initial cache check. Checking
	// HasCache before locking races against concurrent SetCache/ClearCache.
	source.Mu().Lock()
	defer source.Mu().Unlock()

	if source.HasCache() {
		data := source.GetCache()
		if err := registerSourceForCleanup(c, source, data, ""); err != nil {
			return nil, err
		}
		return data, nil
	}

	var cachedData *types.CachedFileData
	var contextKey string
	var err error
	switch s := source.(type) {
	case *types.URLSource:
		if c != nil {
			contextKey = getContextCacheKey(s.URL)
			if cached, exists := c.Get(contextKey); exists {
				if data, ok := cached.(*types.CachedFileData); ok && data != nil {
					if err := registerSourceForCleanup(c, source, data, ""); err != nil {
						return nil, err
					}
					return data, nil
				}
			}
		}
		cachedData, err = loadFromURL(c, s.URL, reason...)
	case *types.Base64Source:
		if c != nil {
			contextKey = getBase64ContextCacheKey(s.Base64Data, s.MimeType)
			if cached, exists := c.Get(contextKey); exists {
				if data, ok := cached.(*types.CachedFileData); ok && data != nil {
					if err := registerSourceForCleanup(c, source, data, ""); err != nil {
						return nil, err
					}
					return data, nil
				}
			}
		}
		cachedData, err = loadFromBase64(s.Base64Data, s.MimeType)
	default:
		return nil, fmt.Errorf("unsupported file source type: %T", source)
	}
	if err != nil {
		return nil, err
	}

	// Publish the source/cache and cleanup registration in one critical
	// section. Cleanup may otherwise close an object before it is registered.
	if err := registerSourceForCleanup(c, source, cachedData, contextKey); err != nil {
		_ = cachedData.Close()
		return nil, err
	}
	return cachedData, nil
}

// This lock protects the read-modify-write sequence of the Gin context's
// cleanup registry and the final cleanup flag. Gin Get/Set alone only protect
// individual operations, not the full registration transaction.
var fileSourceRegistrationMu sync.Mutex

const contextFileSourcesCleanupDone = "file_sources_cleanup_completed"

// registerSourceForCleanup also publishes a newly loaded cache. It must only
// be called while source.Mu() is held, including on cache hits.
func registerSourceForCleanup(c *gin.Context, source types.FileSource, data *types.CachedFileData, contextKey string) error {
	if c == nil {
		source.SetCache(data)
		return nil
	}
	fileSourceRegistrationMu.Lock()
	defer fileSourceRegistrationMu.Unlock()

	if closed, _ := c.Get(contextFileSourcesCleanupDone); closed == true {
		return fmt.Errorf("cannot load file after request cleanup")
	}
	key := string(constant.ContextKeyFileSourcesToCleanup)
	value, _ := c.Get(key)
	sources, _ := value.([]types.FileSource)
	for _, registered := range sources {
		if registered == source {
			source.SetCache(data)
			if contextKey != "" {
				c.Set(contextKey, data)
			}
			return nil
		}
	}
	// FileSource itself is request-bound. Sharing a source object between
	// contexts can otherwise clear a live cache when the first one finishes.
	if source.IsRegistered() {
		return fmt.Errorf("file source belongs to another active request")
	}
	source.SetCache(data)
	source.SetRegistered(true)
	if contextKey != "" {
		c.Set(contextKey, data)
	}
	c.Set(key, append(sources, source))
	return nil
}

// CleanupFileSources ends request ownership. Repeated calls and calls racing
// with new registrations are safe; future loads on this context are rejected.
func CleanupFileSources(c *gin.Context) {
	if c == nil {
		return
	}
	key := string(constant.ContextKeyFileSourcesToCleanup)
	fileSourceRegistrationMu.Lock()
	if closed, _ := c.Get(contextFileSourcesCleanupDone); closed == true {
		fileSourceRegistrationMu.Unlock()
		return
	}
	c.Set(contextFileSourcesCleanupDone, true)
	value, _ := c.Get(key)
	sources, _ := value.([]types.FileSource)
	c.Set(key, []types.FileSource(nil))
	fileSourceRegistrationMu.Unlock()

	// Always acquire locks in source -> registration order while loading.
	// Cleanup releases the registration lock before waiting for source locks.
	for _, source := range sources {
		source.Mu().Lock()
		source.ClearCache()
		source.SetRegistered(false)
		source.Mu().Unlock()
	}
}

// loadFromURL 从 URL 加载文件
func loadFromURL(c *gin.Context, url string, reason ...string) (*types.CachedFileData, error) {
	// 下载文件
	var maxFileSize = constant.MaxFileDownloadMB * 1024 * 1024

	if common.DebugEnabled {
		logger.LogDebug(c, "loadFromURL: initiating download")
	}
	resp, err := DoDownloadRequest(url, reason...)
	if err != nil {
		return nil, fmt.Errorf("failed to download file from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to download file, status code: %d", resp.StatusCode)
	}

	// 读取文件内容（限制大小）
	if common.DebugEnabled {
		logger.LogDebug(c, "loadFromURL: reading response body")
	}
	fileBytes, err := common.ReadAllLimit(resp.Body, int64(maxFileSize))
	if err == common.ErrLimitExceeded {
		return nil, fmt.Errorf("file size exceeds maximum allowed size: %dMB", constant.MaxFileDownloadMB)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read file content: %w", err)
	}

	// 转换为 base64
	base64Data := base64.StdEncoding.EncodeToString(fileBytes)

	// 智能获取 MIME 类型
	mimeType := smartDetectMimeType(resp, url, fileBytes)

	// 判断是否使用磁盘缓存
	base64Size := int64(len(base64Data))
	var cachedData *types.CachedFileData

	if shouldUseDiskCache(base64Size) {
		// 使用磁盘缓存
		diskPath, err := writeToDiskCache(base64Data)
		if err != nil {
			// 磁盘缓存失败，回退到内存
			logger.LogWarn(c, fmt.Sprintf("Failed to write to disk cache, falling back to memory: %v", err))
			cachedData = types.NewMemoryCachedData(base64Data, mimeType, int64(len(fileBytes)))
		} else {
			cachedData = types.NewDiskCachedData(diskPath, mimeType, int64(len(fileBytes)))
			cachedData.DiskSize = base64Size
			cachedData.OnClose = func(size int64) {
				common.DecrementDiskFiles(size)
			}
			common.IncrementDiskFiles(base64Size)
			if common.DebugEnabled {
				logger.LogDebug(c, "File cached to disk: %s, size: %d bytes", diskPath, base64Size)
			}
		}
	} else {
		// 使用内存缓存
		cachedData = types.NewMemoryCachedData(base64Data, mimeType, int64(len(fileBytes)))
	}

	// 如果是图片，尝试获取图片配置
	if strings.HasPrefix(mimeType, "image/") {
		if common.DebugEnabled {
			logger.LogDebug(c, "loadFromURL: decoding image config")
		}
		config, format, err := decodeImageConfig(fileBytes)
		if err == nil {
			cachedData.ImageConfig = &config
			cachedData.ImageFormat = format
			// 如果通过图片解码获取了更准确的格式，更新 MIME 类型
			if mimeType == "application/octet-stream" || mimeType == "" {
				cachedData.MimeType = "image/" + format
			}
		}
	}

	return cachedData, nil
}

// shouldUseDiskCache 判断是否应该使用磁盘缓存
func shouldUseDiskCache(dataSize int64) bool {
	return common.ShouldUseDiskCache(dataSize)
}

// writeToDiskCache 将数据写入磁盘缓存
func writeToDiskCache(base64Data string) (string, error) {
	return common.WriteDiskCacheFileString(common.DiskCacheTypeFile, base64Data)
}

// smartDetectMimeType 智能检测 MIME 类型
func smartDetectMimeType(resp *http.Response, url string, fileBytes []byte) string {
	// 1. 尝试从 Content-Type header 获取
	mimeType := resp.Header.Get("Content-Type")
	if idx := strings.Index(mimeType, ";"); idx != -1 {
		mimeType = strings.TrimSpace(mimeType[:idx])
	}
	if mimeType != "" && mimeType != "application/octet-stream" {
		return mimeType
	}

	// 2. 尝试从 Content-Disposition header 的 filename 获取
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		parts := strings.Split(cd, ";")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(strings.ToLower(part), "filename=") {
				name := strings.TrimSpace(strings.TrimPrefix(part, "filename="))
				// 移除引号
				if len(name) > 2 && name[0] == '"' && name[len(name)-1] == '"' {
					name = name[1 : len(name)-1]
				}
				if dot := strings.LastIndex(name, "."); dot != -1 && dot+1 < len(name) {
					ext := strings.ToLower(name[dot+1:])
					if ext != "" {
						mt := GetMimeTypeByExtension(ext)
						if mt != "application/octet-stream" {
							return mt
						}
					}
				}
				break
			}
		}
	}

	// 3. 尝试从 URL 路径获取扩展名
	mt := guessMimeTypeFromURL(url)
	if mt != "application/octet-stream" {
		return mt
	}

	// 4. 使用 http.DetectContentType 内容嗅探
	if len(fileBytes) > 0 {
		sniffed := http.DetectContentType(fileBytes)
		if sniffed != "" && sniffed != "application/octet-stream" {
			// 去除可能的 charset 参数
			if idx := strings.Index(sniffed, ";"); idx != -1 {
				sniffed = strings.TrimSpace(sniffed[:idx])
			}
			return sniffed
		}

		// 4.5 尝试 HEIF/HEIC 检测（Go 标准库不识别）
		if heifMime := detectHEIF(fileBytes); heifMime != "" {
			return heifMime
		}
	}

	// 5. 尝试作为图片解码获取格式
	if len(fileBytes) > 0 {
		if _, format, err := decodeImageConfig(fileBytes); err == nil && format != "" {
			return "image/" + strings.ToLower(format)
		}
	}

	// 最终回退
	return "application/octet-stream"
}

// loadFromBase64 从 base64 字符串加载文件
func loadFromBase64(base64String string, providedMimeType string) (*types.CachedFileData, error) {
	var mimeType string
	var cleanBase64 string

	// 处理 data: 前缀
	if strings.HasPrefix(base64String, "data:") {
		idx := strings.Index(base64String, ",")
		if idx != -1 {
			header := base64String[:idx]
			cleanBase64 = base64String[idx+1:]

			if strings.Contains(header, ":") && strings.Contains(header, ";") {
				mimeStart := strings.Index(header, ":") + 1
				mimeEnd := strings.Index(header, ";")
				if mimeStart < mimeEnd {
					mimeType = header[mimeStart:mimeEnd]
				}
			}
		} else {
			cleanBase64 = base64String
		}
	} else {
		cleanBase64 = base64String
	}

	if providedMimeType != "" {
		mimeType = providedMimeType
	}

	decodedData, err := base64.StdEncoding.DecodeString(cleanBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 data: %w", err)
	}

	base64Size := int64(len(cleanBase64))
	var cachedData *types.CachedFileData

	if shouldUseDiskCache(base64Size) {
		diskPath, err := writeToDiskCache(cleanBase64)
		if err != nil {
			cachedData = types.NewMemoryCachedData(cleanBase64, mimeType, int64(len(decodedData)))
		} else {
			cachedData = types.NewDiskCachedData(diskPath, mimeType, int64(len(decodedData)))
			cachedData.DiskSize = base64Size
			cachedData.OnClose = func(size int64) {
				common.DecrementDiskFiles(size)
			}
			common.IncrementDiskFiles(base64Size)
		}
	} else {
		cachedData = types.NewMemoryCachedData(cleanBase64, mimeType, int64(len(decodedData)))
	}

	if mimeType == "" || strings.HasPrefix(mimeType, "image/") {
		config, format, err := decodeImageConfig(decodedData)
		if err == nil {
			cachedData.ImageConfig = &config
			cachedData.ImageFormat = format
			if mimeType == "" {
				cachedData.MimeType = "image/" + format
			}
		}
	}

	return cachedData, nil
}

// GetImageConfig 获取图片配置
func GetImageConfig(c *gin.Context, source types.FileSource) (image.Config, string, error) {
	cachedData, err := LoadFileSource(c, source, "get_image_config")
	if err != nil {
		return image.Config{}, "", err
	}

	if cachedData.ImageConfig != nil {
		return *cachedData.ImageConfig, cachedData.ImageFormat, nil
	}

	base64Str, err := cachedData.GetBase64Data()
	if err != nil {
		return image.Config{}, "", fmt.Errorf("failed to get base64 data: %w", err)
	}
	decodedData, err := base64.StdEncoding.DecodeString(base64Str)
	if err != nil {
		return image.Config{}, "", fmt.Errorf("failed to decode base64 for image config: %w", err)
	}

	config, format, err := decodeImageConfig(decodedData)
	if err != nil {
		return image.Config{}, "", err
	}

	cachedData.ImageConfig = &config
	cachedData.ImageFormat = format

	return config, format, nil
}

// GetBase64Data 获取 base64 编码的数据
func GetBase64Data(c *gin.Context, source types.FileSource, reason ...string) (string, string, error) {
	cachedData, err := LoadFileSource(c, source, reason...)
	if err != nil {
		return "", "", err
	}
	base64Str, err := cachedData.GetBase64Data()
	if err != nil {
		return "", "", fmt.Errorf("failed to get base64 data: %w", err)
	}
	return base64Str, cachedData.MimeType, nil
}

// GetMimeType 获取文件的 MIME 类型
func GetMimeType(c *gin.Context, source types.FileSource) (string, error) {
	source.Mu().Lock()
	if source.HasCache() {
		mimeType := source.GetCache().MimeType
		source.Mu().Unlock()
		return mimeType, nil
	}
	source.Mu().Unlock()

	if urlSource, ok := source.(*types.URLSource); ok {
		mimeType, err := GetFileTypeFromUrl(c, urlSource.URL, "get_mime_type")
		if err == nil && mimeType != "" && mimeType != "application/octet-stream" {
			return mimeType, nil
		}
	}

	cachedData, err := LoadFileSource(c, source, "get_mime_type")
	if err != nil {
		return "", err
	}
	return cachedData.MimeType, nil
}

// DetectFileType 检测文件类型
func DetectFileType(mimeType string) types.FileType {
	if strings.HasPrefix(mimeType, "image/") {
		return types.FileTypeImage
	}
	if strings.HasPrefix(mimeType, "audio/") {
		return types.FileTypeAudio
	}
	if strings.HasPrefix(mimeType, "video/") {
		return types.FileTypeVideo
	}
	return types.FileTypeFile
}

// decodeImageConfig 从字节数据解码图片配置
func decodeImageConfig(data []byte) (image.Config, string, error) {
	reader := bytes.NewReader(data)

	config, format, err := image.DecodeConfig(reader)
	if err == nil {
		return config, format, nil
	}

	reader.Seek(0, io.SeekStart)
	config, err = webp.DecodeConfig(reader)
	if err == nil {
		return config, "webp", nil
	}

	// Try HEIF/HEIC: parse ISOBMFF ispe box for dimensions
	if heifMime := detectHEIF(data); heifMime != "" {
		formatName := "heif"
		if heifMime == "image/heic" {
			formatName = "heic"
		}
		if w, h, ok := parseHEIFDimensions(data); ok {
			return image.Config{Width: w, Height: h}, formatName, nil
		}
		return image.Config{}, "", fmt.Errorf("failed to decode HEIF/HEIC image dimensions")
	}

	return image.Config{}, "", fmt.Errorf("failed to decode image config: unsupported format")
}

// detectHEIF checks ISOBMFF magic bytes to detect HEIC/HEIF files.
// Returns "image/heic", "image/heif", or "" if not recognized.
func detectHEIF(data []byte) string {
	if len(data) < 12 {
		return ""
	}
	// ISOBMFF: bytes[4:8] must be "ftyp"
	if string(data[4:8]) != "ftyp" {
		return ""
	}
	brand := string(data[8:12])
	switch brand {
	case "heic", "heix", "hevc", "hevx", "heim", "heis":
		return "image/heic"
	case "mif1", "msf1":
		return "image/heif"
	default:
		return ""
	}
}

// parseHEIFDimensions parses ISOBMFF box tree to find the ispe box
// and extract image width/height. Returns (width, height, ok).
func parseHEIFDimensions(data []byte) (int, int, bool) {
	size := len(data)
	if size < 12 {
		return 0, 0, false
	}

	// Walk top-level boxes to find "meta"
	offset := 0
	for offset+8 <= size {
		boxSize := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		boxType := string(data[offset+4 : offset+8])
		headerLen := 8

		if boxSize == 1 {
			// 64-bit extended size
			if offset+16 > size {
				break
			}
			boxSize = int(binary.BigEndian.Uint64(data[offset+8 : offset+16]))
			headerLen = 16
		} else if boxSize == 0 {
			// box extends to end of data
			boxSize = size - offset
		}

		if boxSize < headerLen || offset+boxSize > size {
			break
		}

		if boxType == "meta" {
			// meta is a full box: 4 bytes version/flags after header
			metaData := data[offset+headerLen : offset+boxSize]
			if len(metaData) < 4 {
				return 0, 0, false
			}
			return findISPE(metaData[4:])
		}
		offset += boxSize
	}
	return 0, 0, false
}

// Keep metadata parsing bounded; valid HEIF metadata has a shallow container path.
const maxHEIFBoxDepth = 64

// findISPE recursively searches for the ispe box within container boxes.
// Path: meta -> iprp -> ipco -> ispe
func findISPE(data []byte) (int, int, bool) {
	return findISPEAtDepth(data, 0)
}

func findISPEAtDepth(data []byte, depth int) (int, int, bool) {
	if depth > maxHEIFBoxDepth {
		return 0, 0, false
	}

	offset := 0
	size := len(data)
	for offset+8 <= size {
		boxSize := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		boxType := string(data[offset+4 : offset+8])
		if boxSize < 8 || offset+boxSize > size {
			break
		}
		content := data[offset+8 : offset+boxSize]
		switch boxType {
		case "iprp", "ipco":
			if w, h, ok := findISPEAtDepth(content, depth+1); ok {
				return w, h, true
			}
		case "ispe":
			// ispe is a full box: 4 bytes version/flags, then 4 bytes width, 4 bytes height
			if len(content) >= 12 {
				w := int(binary.BigEndian.Uint32(content[4:8]))
				h := int(binary.BigEndian.Uint32(content[8:12]))
				if w > 0 && h > 0 {
					return w, h, true
				}
			}
		}
		offset += boxSize
	}
	return 0, 0, false
}

// guessMimeTypeFromURL 从 URL 猜测 MIME 类型
func guessMimeTypeFromURL(url string) string {
	cleanedURL := url
	if q := strings.Index(cleanedURL, "?"); q != -1 {
		cleanedURL = cleanedURL[:q]
	}

	if slash := strings.LastIndex(cleanedURL, "/"); slash != -1 && slash+1 < len(cleanedURL) {
		last := cleanedURL[slash+1:]
		if dot := strings.LastIndex(last, "."); dot != -1 && dot+1 < len(last) {
			ext := strings.ToLower(last[dot+1:])
			return GetMimeTypeByExtension(ext)
		}
	}

	return "application/octet-stream"
}
