package middleware

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/common/limiter"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const (
	ModelRequestRateLimitCountMark        = "MRRL"
	ModelRequestRateLimitSuccessCountMark = "MRRLS"
	modelRateLimitTimeFormat              = "2006-01-02T15:04:05.000Z"
)

type ModelRequestRateLimitCommit func(success bool)

// 检查Redis中的请求限制
func checkRedisRateLimit(ctx context.Context, rdb *redis.Client, key string, maxCount int, duration int64) (bool, error) {
	// 如果maxCount为0，表示不限制
	if maxCount == 0 {
		return true, nil
	}

	// 获取当前计数
	length, err := rdb.LLen(ctx, key).Result()
	if err != nil {
		return false, err
	}

	// 如果未达到限制，允许请求
	if length < int64(maxCount) {
		return true, nil
	}

	// 检查时间窗口
	oldTimeStr, _ := rdb.LIndex(ctx, key, -1).Result()
	oldTime, err := time.Parse(modelRateLimitTimeFormat, oldTimeStr)
	if err != nil {
		return false, err
	}

	nowTimeStr := time.Now().UTC().Format(modelRateLimitTimeFormat)
	nowTime, err := time.Parse(modelRateLimitTimeFormat, nowTimeStr)
	if err != nil {
		return false, err
	}
	// 如果在时间窗口内已达到限制，拒绝请求
	subTime := nowTime.Sub(oldTime).Seconds()
	if int64(subTime) < duration {
		rdb.Expire(ctx, key, rateLimitWindowDuration(setting.ModelRequestRateLimitDurationMinutes))
		return false, nil
	}

	return true, nil
}

// 记录Redis请求
func recordRedisRequest(ctx context.Context, rdb *redis.Client, key string, maxCount int) {
	// 如果maxCount为0，不记录请求
	if maxCount == 0 {
		return
	}

	now := time.Now().UTC().Format(modelRateLimitTimeFormat)
	rdb.LPush(ctx, key, now)
	rdb.LTrim(ctx, key, 0, int64(maxCount-1))
	rdb.Expire(ctx, key, rateLimitWindowDuration(setting.ModelRequestRateLimitDurationMinutes))
}

func modelRequestRateLimitConfig(c *gin.Context) (duration int64, totalMaxCount int, successMaxCount int) {
	duration = rateLimitDurationSeconds(setting.ModelRequestRateLimitDurationMinutes)
	totalMaxCount = setting.ModelRequestRateLimitCount
	successMaxCount = setting.ModelRequestRateLimitSuccessCount
	group := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	if group == "" {
		group = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	}
	if groupTotalCount, groupSuccessCount, found := setting.GetGroupRateLimit(group); found {
		totalMaxCount = groupTotalCount
		successMaxCount = groupSuccessCount
	}
	return duration, totalMaxCount, successMaxCount
}

func newModelRateLimitError(message string, statusCode int) *types.NewAPIError {
	return types.NewErrorWithStatusCode(
		fmt.Errorf("%s", message),
		types.ErrorCodeInvalidRequest,
		statusCode,
		types.ErrOptionWithSkipRetry(),
		types.ErrOptionWithNoRecordErrorLog(),
	)
}

// CheckModelRequestRateLimit checks and records the total-attempt quota now,
// then reserves successful-attempt capacity on the memory backend. Complete the
// returned callback on every exit path; completion is idempotent on both backends.
func CheckModelRequestRateLimit(c *gin.Context) (ModelRequestRateLimitCommit, *types.NewAPIError) {
	if !setting.ModelRequestRateLimitEnabled {
		return func(bool) {}, nil
	}
	duration, totalMaxCount, successMaxCount := modelRequestRateLimitConfig(c)
	return checkModelRequestRateLimit(c, duration, totalMaxCount, successMaxCount, common.RedisEnabled)
}

func checkModelRequestRateLimit(c *gin.Context, duration int64, totalMaxCount, successMaxCount int, useRedis bool) (ModelRequestRateLimitCommit, *types.NewAPIError) {
	userID := strconv.Itoa(c.GetInt("id"))

	if useRedis {
		ctx := context.Background()
		rdb := common.RDB
		successKey := fmt.Sprintf("rateLimit:%s:%s", ModelRequestRateLimitSuccessCountMark, userID)
		allowed, err := checkRedisRateLimit(ctx, rdb, successKey, successMaxCount, duration)
		if err != nil {
			return nil, newModelRateLimitError("rate_limit_check_failed", http.StatusInternalServerError)
		}
		if !allowed {
			return nil, newModelRateLimitError(fmt.Sprintf("您已达到请求数限制：%d分钟内最多请求%d次", setting.ModelRequestRateLimitDurationMinutes, successMaxCount), http.StatusTooManyRequests)
		}
		if totalMaxCount > 0 {
			totalKey := fmt.Sprintf("rateLimit:%s", userID)
			allowed, err = limiter.New(ctx, rdb).Allow(ctx, totalKey,
				limiter.WithCapacity(rateLimitCapacity(totalMaxCount, duration)),
				limiter.WithRate(int64(totalMaxCount)),
				limiter.WithRequested(duration),
			)
			if err != nil {
				return nil, newModelRateLimitError("rate_limit_check_failed", http.StatusInternalServerError)
			}
			if !allowed {
				return nil, newModelRateLimitError(fmt.Sprintf("您已达到总请求数限制：%d分钟内最多请求%d次，包括失败次数，请检查您的请求是否正确", setting.ModelRequestRateLimitDurationMinutes, totalMaxCount), http.StatusTooManyRequests)
			}
		}
		// Redis retains its existing sliding success window. Strict distributed
		// reservations require a separate atomic Redis protocol, not process-local
		// pins. Use the same outcome and exactly-once accounting here.
		var once sync.Once
		return func(success bool) {
			once.Do(func() {
				if success {
					recordRedisRequest(ctx, rdb, successKey, successMaxCount)
				}
			})
		}, nil
	}

	inMemoryRateLimiter.Init(rateLimitWindowDuration(setting.ModelRequestRateLimitDurationMinutes))
	totalKey := ModelRequestRateLimitCountMark + userID
	successKey := ModelRequestRateLimitSuccessCountMark + userID
	if totalMaxCount > 0 && !inMemoryRateLimiter.Request(totalKey, totalMaxCount, duration) {
		return nil, newModelRateLimitError(fmt.Sprintf("您已达到总请求数限制：%d分钟内最多请求%d次，包括失败次数，请检查您的请求是否正确", setting.ModelRequestRateLimitDurationMinutes, totalMaxCount), http.StatusTooManyRequests)
	}
	commit, allowed := inMemoryRateLimiter.Reserve(successKey, successMaxCount, duration)
	if !allowed {
		return nil, newModelRateLimitError(fmt.Sprintf("您已达到请求数限制：%d分钟内最多请求%d次", setting.ModelRequestRateLimitDurationMinutes, successMaxCount), http.StatusTooManyRequests)
	}
	return commit, nil
}

func rateLimitDurationSeconds(durationMinutes int) int64 {
	if durationMinutes <= 0 {
		return 0
	}
	minutes := int64(durationMinutes)
	if minutes > math.MaxInt64/60 {
		return math.MaxInt64
	}
	return minutes * 60
}

func rateLimitCapacity(count int, durationSeconds int64) int64 {
	if count <= 0 || durationSeconds <= 0 {
		return 0
	}
	c := int64(count)
	if c > math.MaxInt64/durationSeconds {
		return math.MaxInt64
	}
	return c * durationSeconds
}

func rateLimitWindowDuration(durationMinutes int) time.Duration {
	seconds := rateLimitDurationSeconds(durationMinutes)
	if seconds > math.MaxInt64/int64(time.Second) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(seconds) * time.Second
}

func isResponsesWebSocketHandshake(c *gin.Context) bool {
	return c != nil && c.Request != nil && c.Request.Method == http.MethodGet &&
		c.Request.URL != nil && c.Request.URL.Path == "/v1/responses" &&
		strings.EqualFold(c.Request.Header.Get("Upgrade"), "websocket")
}

// modelRequestSucceeded uses the final protocol outcome rather than headers
// committed before a stream has finished. Legacy adaptors must explicitly
// complete DoResponse when they do not publish a protocol-specific status.
func modelRequestSucceeded(c *gin.Context) bool {
	if c == nil || c.Writer == nil || c.Request == nil || c.Writer.Status() >= http.StatusBadRequest || c.Request.Context().Err() != nil || len(c.Errors) > 0 {
		return false
	}
	info, _ := common.GetContextKeyType[*relaycommon.RelayInfo](c, constant.ContextKeyRelayInfo)
	if info != nil && (info.ResponseFailed || info.LastError != nil) {
		return false
	}
	if info != nil {
		status := info.StreamStatus
		if status == nil {
			status = info.RateLimitStreamStatus
		}
		if status != nil {
			return status.IsNormalEnd() && status.EndError == nil && !status.HasErrors()
		}
	}
	stream := common.GetContextKeyBool(c, constant.ContextKeyIsStream) || (info != nil && info.IsStream) ||
		strings.HasPrefix(strings.ToLower(c.Writer.Header().Get("Content-Type")), "text/event-stream")
	return !stream || (info != nil && info.ResponseCompleted && !info.ResponseFailed)
}

func modelRateLimitHandler(duration int64, totalMaxCount, successMaxCount int, useRedis bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		commit, apiErr := checkModelRequestRateLimit(c, duration, totalMaxCount, successMaxCount, useRedis)
		if apiErr != nil {
			abortWithOpenAiMessage(c, apiErr.StatusCode, apiErr.Error(), apiErr.GetErrorCode())
			return
		}
		defer commit(false)
		c.Next()
		commit(modelRequestSucceeded(c))
	}
}

func redisRateLimitHandler(duration int64, totalMaxCount, successMaxCount int) gin.HandlerFunc {
	return modelRateLimitHandler(duration, totalMaxCount, successMaxCount, true)
}

func memoryRateLimitHandler(duration int64, totalMaxCount, successMaxCount int) gin.HandlerFunc {
	return modelRateLimitHandler(duration, totalMaxCount, successMaxCount, false)
}

// ModelRequestRateLimit 模型请求限流中间件
func ModelRequestRateLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		if isResponsesWebSocketHandshake(c) || isNativeVoiceHandshake(c) {
			c.Next()
			return
		}
		commit, apiErr := CheckModelRequestRateLimit(c)
		if apiErr != nil {
			abortWithOpenAiMessage(c, apiErr.StatusCode, apiErr.Error(), apiErr.GetErrorCode())
			return
		}
		defer commit(false)
		c.Next()
		commit(modelRequestSucceeded(c))
	}
}

// Voice protocols reveal their model in a session configuration. Their
// controller applies CheckModelRequestRateLimit after that configuration has
// passed model authorization, exactly once per admitted session.
func isNativeVoiceHandshake(c *gin.Context) bool {
	path := c.Request.URL.Path
	return path == "/v1/live/sessions" ||
		strings.HasPrefix(path, "/v1/realtime/translations") ||
		path == "/v1/realtime/transcription_sessions" ||
		path == "/v1/realtime/client_secrets" || path == "/v1/realtime/calls" ||
		(path == "/v1/realtime" && c.Query("intent") == "transcription")
}
