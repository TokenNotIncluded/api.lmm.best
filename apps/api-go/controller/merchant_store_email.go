package controller

import (
	"errors"
	"net/mail"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// Kept injectable for isolated HTTP tests. Tests must never send real mail.
var merchantStoreVerificationSender = service.SendMerchantStoreVerificationEmail

func GetMerchantStoreEmailStatus(c *gin.Context) {
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		merchantStoreRespond(c, nil, err)
		return
	}
	email := ""
	if address, parseErr := mail.ParseAddress(strings.TrimSpace(user.Email)); parseErr == nil && !strings.ContainsAny(address.Address, "\r\n") {
		email = address.Address
	}
	verified := false
	if email != "" {
		verifiedEmail, verifyErr := model.GetMerchantStoreVerifiedEmailAddress(user.Id)
		if verifyErr != nil && !errors.Is(verifyErr, model.ErrMerchantStoreEmailUnverified) {
			merchantStoreRespond(c, nil, verifyErr)
			return
		}
		verified = verifyErr == nil && verifiedEmail == email
	}
	merchantStoreRespond(c, gin.H{"verified": verified, "email": email}, nil)
}

func SendMerchantStoreEmailVerification(c *gin.Context) {
	email, code, err := model.BeginMerchantStoreEmailVerification(c.GetInt("id"))
	if err == nil {
		err = merchantStoreVerificationSender(c.Request.Context(), c.GetInt("id"), email, code)
	}
	merchantStoreRespond(c, gin.H{"sent": err == nil}, err)
}

func ConfirmMerchantStoreEmailVerification(c *gin.Context) {
	var input struct {
		Code string `json:"code"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	merchantStoreRespond(c, nil, model.VerifyMerchantStoreEmailVerification(c.GetInt("id"), input.Code))
}
