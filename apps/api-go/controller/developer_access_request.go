package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RetiredDeveloperAccessRequest is the compatibility response for retired L1
// application, review and archive URLs. Authentication stays in the router.
// No history is returned, no confirmation token is consumed, and no data changes.
func RetiredDeveloperAccessRequest(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusGone, gin.H{
		"success": false,
		"code":    "DEVELOPER_ACCESS_LETTER_RETIRED",
		"message": "此入口已停用，请通过内置助手开通 L1。This endpoint is retired. Use the built-in assistant to enable L1.",
	})
}
