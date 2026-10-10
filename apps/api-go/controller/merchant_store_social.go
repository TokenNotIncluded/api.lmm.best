package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func GetMerchantStoreProductLikes(c *gin.Context) {
	view, err := model.GetMerchantStoreProductLikes(merchantStoreViewer(c), c.Param("id"))
	merchantStoreRespond(c, view, err)
}

func merchantStoreSetProductLike(c *gin.Context, liked bool) {
	// The HTTP method is the absolute desired state. Optional empty JSON cannot
	// replace the authenticated account or provide a counter to persist.
	if c.Request.ContentLength != 0 {
		var input struct{}
		if !merchantStoreAccessBody(c, &input) {
			return
		}
	}
	view, err := model.SetMerchantStoreProductLike(c.GetInt("id"), c.Param("id"), liked)
	merchantStoreRespond(c, view, err)
}

func LikeMerchantStoreProduct(c *gin.Context)   { merchantStoreSetProductLike(c, true) }
func UnlikeMerchantStoreProduct(c *gin.Context) { merchantStoreSetProductLike(c, false) }
