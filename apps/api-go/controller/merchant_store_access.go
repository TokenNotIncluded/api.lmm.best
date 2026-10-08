package controller

import (
	"encoding/json"
	"io"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func merchantStoreAccessBody(c *gin.Context, target any) bool {
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(new(any)) != io.EOF {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return false
	}
	return true
}

func merchantStoreGuestHeader(c *gin.Context) string {
	values := c.Request.Header.Values("X-Store-Guest")
	if len(values) != 1 {
		return ""
	}
	return values[0]
}

func merchantStoreViewer(c *gin.Context) int {
	if len(c.Request.Header.Values("X-Store-Guest")) != 0 {
		return 0
	}
	return c.GetInt("id")
}

func CreateMerchantStoreGuestOrder(c *gin.Context) {
	var input model.MerchantStoreCheckoutInput
	if !merchantStoreAccessBody(c, &input) {
		return
	}
	input.BuyerID, input.GuestToken = 0, merchantStoreGuestHeader(c)
	order, created, e := model.CreateMerchantStoreOrder(input)
	merchantStoreRespond(c, gin.H{"order": order, "created": created}, e)
}

func CreateMerchantStoreGuestSession(c *gin.Context) {
	session, e := model.CreateMerchantStoreGuestSession()
	merchantStoreRespond(c, session, e)
}

func AcceptMerchantStoreGuestDisclaimer(c *gin.Context) {
	var input struct {
		Version  string `json:"version"`
		Accepted bool   `json:"accepted"`
	}
	if !merchantStoreAccessBody(c, &input) {
		return
	}
	if !input.Accepted {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	merchantStoreRespond(c, nil, model.AcceptMerchantStoreGuestDisclaimer(merchantStoreGuestHeader(c), input.Version))
}

func GetMerchantStoreProductTerms(c *gin.Context) {
	view, e := model.GetMerchantStoreProductTerms(merchantStoreViewer(c), merchantStoreGuestHeader(c), c.Param("id"))
	merchantStoreRespond(c, view, e)
}

func GetMyMerchantStoreTerms(c *gin.Context) {
	view, e := model.GetMerchantStoreSellerTerms(c.GetInt("id"))
	merchantStoreRespond(c, view, e)
}

func SaveMyMerchantStoreTerms(c *gin.Context) {
	var input model.MerchantStoreTermsInput
	if !merchantStoreAccessBody(c, &input) {
		return
	}
	view, e := model.SaveMerchantStoreSellerTerms(c.GetInt("id"), input)
	merchantStoreRespond(c, view, e)
}

func GetMerchantStoreGuestOrder(c *gin.Context) {
	o, e := model.GetMerchantStoreGuestOrder(merchantStoreGuestHeader(c), c.Param("id"))
	merchantStoreRespond(c, o, e)
}

func LookupMerchantStoreGuestOrder(c *gin.Context) {
	var input struct {
		RequestKey string `json:"request_key"`
	}
	if !merchantStoreAccessBody(c, &input) {
		return
	}
	o, e := model.FindMerchantStoreGuestOrderByRequestKey(merchantStoreGuestHeader(c), input.RequestKey)
	merchantStoreRespond(c, o, e)
}

func LookupMyMerchantStoreOrder(c *gin.Context) {
	o, e := model.FindMerchantStoreOrderByRequestKey(c.GetInt("id"), c.Param("request_key"))
	merchantStoreRespond(c, o, e)
}

func CancelMerchantStoreGuestOrder(c *gin.Context) {
	merchantStoreRespond(c, nil, model.CancelMerchantStoreGuestOrder(merchantStoreGuestHeader(c), c.Param("id")))
}

func PayMerchantStoreGuestOrder(c *gin.Context) {
	var input struct {
		Currency string `json:"currency"`
	}
	if !merchantStoreAccessBody(c, &input) {
		return
	}
	session, e := service.CreateMerchantStoreGuestPaymentSession(c.Request.Context(), merchantStoreGuestHeader(c), c.Param("id"), input.Currency)
	merchantStoreRespond(c, session, e)
}

func ReconcileMerchantStoreGuestOrder(c *gin.Context) {
	o, e := service.ReconcileMerchantStoreGuestPayment(c.Request.Context(), merchantStoreGuestHeader(c), c.Param("id"))
	merchantStoreRespond(c, o, e)
}

func GetMerchantStoreGuestPickupLink(c *gin.Context) {
	token, e := model.GetMerchantStoreGuestPickupToken(merchantStoreGuestHeader(c), c.Param("id"))
	url := ""
	if e == nil {
		url, e = merchantStorePickupURL(token)
	}
	merchantStoreRespond(c, gin.H{"pickup_url": url}, e)
}
