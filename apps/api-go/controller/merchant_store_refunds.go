package controller

import (
	"encoding/json"
	"io"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

// Refund bodies are strict: a misspelled amount field must not silently become
// a full refund or a different idempotency request.
func merchantStoreRefundBody(c *gin.Context, target any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(target); e != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return false
	}
	var extra any
	if e := decoder.Decode(&extra); e != io.EOF {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return false
	}
	return true
}
func GetMerchantStoreRefunds(c *gin.Context) {
	v, e := model.GetMerchantStoreRefunds(c.GetInt("id"), c.Param("id"))
	merchantStoreRespond(c, v, e)
}
func RequestMerchantStoreRefund(c *gin.Context) {
	var in model.MerchantStoreRefundInput
	if !merchantStoreRefundBody(c, &in) {
		return
	}
	v, e := model.RequestMerchantStoreRefund(c.GetInt("id"), c.Param("id"), in)
	merchantStoreRespond(c, v, e)
}
func ProactivelyRefundMerchantStoreOrder(c *gin.Context) {
	var in model.MerchantStoreRefundInput
	if !merchantStoreRefundBody(c, &in) {
		return
	}
	v, e := model.ProactivelyRefundMerchantStoreOrder(c.GetInt("id"), c.Param("id"), in)
	merchantStoreRespond(c, v, e)
}
func DecideMerchantStoreRefund(c *gin.Context) {
	var in model.MerchantStoreRefundDecision
	if !merchantStoreRefundBody(c, &in) {
		return
	}
	v, e := model.DecideMerchantStoreRefund(c.GetInt("id"), c.Param("id"), c.Param("refund_id"), in)
	merchantStoreRespond(c, v, e)
}
func CancelMerchantStoreRefund(c *gin.Context) {
	v, e := model.CancelMerchantStoreRefund(c.GetInt("id"), c.Param("id"), c.Param("refund_id"))
	merchantStoreRespond(c, v, e)
}
func GetMerchantStorePickupRefunds(c *gin.Context) {
	var proof model.MerchantStoreRefundPickupProof
	if !merchantStoreRefundBody(c, &proof) {
		return
	}
	v, e := model.GetMerchantStoreRefundsWithPickupProof(c.GetInt("id"), proof)
	merchantStoreRespond(c, v, e)
}
func RequestMerchantStorePickupRefund(c *gin.Context) {
	var in struct {
		model.MerchantStoreRefundPickupProof
		Input model.MerchantStoreRefundInput `json:"input"`
	}
	if !merchantStoreRefundBody(c, &in) {
		return
	}
	v, e := model.RequestMerchantStoreRefundWithPickupProof(c.GetInt("id"), in.MerchantStoreRefundPickupProof, in.Input)
	merchantStoreRespond(c, v, e)
}
