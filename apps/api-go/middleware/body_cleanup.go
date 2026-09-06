package middleware

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// BodyStorageCleanup 请求体存储清理中间件
// 在请求处理完成后自动清理磁盘/内存缓存
func BodyStorageCleanup() gin.HandlerFunc {
	return func(c *gin.Context) {
		// defer 按逆序执行，保持原清理顺序并覆盖下游 panic。
		defer service.CleanupFileSources(c)
		defer common.CleanupMultipartForms(c)
		defer common.CleanupBodyStorage(c)

		c.Next()
	}
}
