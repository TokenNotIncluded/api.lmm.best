package logger

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

const (
	loggerINFO  = "INFO"
	loggerWarn  = "WARN"
	loggerError = "ERR"
	loggerDebug = "DEBUG"
)

const maxLogCount = 1000000

var logCount atomic.Int64
var setupLogLock sync.Mutex
var setupLogWorking atomic.Bool
var currentLogPath string
var currentLogPathMu sync.RWMutex
var currentLogFile *os.File

func GetCurrentLogPath() string {
	currentLogPathMu.RLock()
	defer currentLogPathMu.RUnlock()
	return currentLogPath
}

func SetupLogger() error {
	defer func() {
		setupLogWorking.Store(false)
	}()
	if *common.LogDir != "" {
		ok := setupLogLock.TryLock()
		if !ok {
			log.Println("setup log is already working")
			return nil
		}
		defer func() {
			setupLogLock.Unlock()
		}()
		logPath := filepath.Join(*common.LogDir, fmt.Sprintf("oneapi-%s.log", time.Now().Format("20060102150405")))
		fd, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		currentLogPathMu.Lock()
		oldFile := currentLogFile
		currentLogPath = logPath
		currentLogFile = fd
		currentLogPathMu.Unlock()

		common.LogWriterMu.Lock()
		gin.DefaultWriter = io.MultiWriter(os.Stdout, fd)
		gin.DefaultErrorWriter = io.MultiWriter(os.Stderr, fd)
		if oldFile != nil {
			_ = oldFile.Close()
		}
		common.LogWriterMu.Unlock()
	}
	return nil
}

// InitializeLogger is the error-returning process startup entry point.
func InitializeLogger() error {
	return SetupLogger()
}

func LogInfo(ctx context.Context, msg string) {
	logHelper(ctx, loggerINFO, msg)
}

func LogWarn(ctx context.Context, msg string) {
	logHelper(ctx, loggerWarn, msg)
}

func LogError(ctx context.Context, msg string) {
	logHelper(ctx, loggerError, msg)
}

func LogDebug(ctx context.Context, msg string, args ...any) {
	if common.DebugEnabled {
		if len(args) > 0 {
			msg = fmt.Sprintf(msg, args...)
		}
		logHelper(ctx, loggerDebug, msg)
	}
}

func logHelper(ctx context.Context, level string, msg string) {
	var id any = "SYSTEM"
	if ctx != nil {
		if requestID := ctx.Value(common.RequestIdKey); requestID != nil {
			id = requestID
		}
	}
	now := time.Now()
	common.LogWriterMu.RLock()
	writer := gin.DefaultErrorWriter
	if level == loggerINFO {
		writer = gin.DefaultWriter
	}
	_, _ = fmt.Fprintf(writer, "[%s] %v | %s | %s \n", level, now.Format("2006/01/02 - 15:04:05"), id, msg)
	common.LogWriterMu.RUnlock()
	// Logging is called concurrently by request and task workers.  The counter
	// is only a rotation hint, but it still must be race-free; the CAS ensures
	// that at most one asynchronous rotation is scheduled at a time.
	if logCount.Add(1) > maxLogCount && setupLogWorking.CompareAndSwap(false, true) {
		logCount.Store(0)
		gopool.Go(func() {
			if err := SetupLogger(); err != nil {
				log.Printf("rotate log file: %v", err)
			}
		})
	}
}

func LogQuota(quota int) string {
	return FormatQuota(quota)
}

// FormatQuota records monetary amounts in USD independently of display settings.
func FormatQuota(quota int) string {
	anchor, err := common.CreditsPerUSD()
	if err != nil {
		return "USD unavailable"
	}
	usd := decimal.NewFromInt(int64(quota)).DivRound(anchor, 64)
	text := usd.StringFixed(6)
	if !usd.IsZero() && usd.Round(6).IsZero() {
		text = usd.String()
	}
	return text + " USD"
}

// LogJson 仅供测试使用 only for test
func LogJson(ctx context.Context, msg string, obj any) {
	if !common.DebugEnabled {
		return
	}
	jsonStr, err := common.Marshal(obj)
	if err != nil {
		LogError(ctx, fmt.Sprintf("json marshal failed: %s", err.Error()))
		return
	}
	LogDebug(ctx, "%s | %s", msg, jsonStr)
}
