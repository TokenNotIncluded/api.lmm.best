package controller

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func ReconcileMerchantStoreRefund(c *gin.Context) {
	if c.Request.Body != nil {
		body, e := io.ReadAll(io.LimitReader(c.Request.Body, 1025))
		if e != nil || len(body) > 1024 || strings.TrimSpace(string(body)) != "" && strings.TrimSpace(string(body)) != "{}" {
			merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
			return
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	view, e := service.ReconcileMerchantStoreRefund(ctx, c.GetInt("id"), c.Param("id"), c.Param("refund_id"))
	merchantStoreRespond(c, view, e)
}

// Provider-side synchronization is a query and a local receipt import, never
// an original-channel refund submission. Only the seller or current root may
// request it. Amounts, recipients and evidence cannot be supplied by a browser.
func SyncMerchantStoreRefunds(c *gin.Context) {
	if c.Request.Body != nil {
		body, e := io.ReadAll(io.LimitReader(c.Request.Body, 1025))
		if e != nil || len(body) > 1024 || strings.TrimSpace(string(body)) != "" && strings.TrimSpace(string(body)) != "{}" {
			merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
			return
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	view, e := service.SyncMerchantStoreRefunds(ctx, c.GetInt("id"), c.Param("id"))
	merchantStoreRespond(c, view, e)
}
