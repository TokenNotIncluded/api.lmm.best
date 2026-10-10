package controller

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

var merchantStoreGuestVerificationSender = service.SendMerchantStoreGuestVerificationEmail

type merchantStoreGuestEmailInput struct {
	Email string `json:"email"`
}

type merchantStoreGuestEmailConfirmationInput struct {
	Email       string `json:"email"`
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
}

func merchantStoreGuestEmailDecode(c *gin.Context, input any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	var raw json.RawMessage
	if decoder.Decode(&raw) != nil || decoder.Decode(new(any)) != io.EOF {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return false
	}
	required := []string{"email"}
	if _, confirm := input.(*merchantStoreGuestEmailConfirmationInput); confirm {
		required = append(required, "challenge_id", "code")
	}
	for _, field := range required {
		value := bytes.TrimSpace(fields[field])
		if len(value) == 0 || value[0] != '"' {
			merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
			return false
		}
	}
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	if strict.Decode(input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return false
	}
	return true
}

func GetMerchantStoreGuestEmailStatus(c *gin.Context) {
	var input merchantStoreGuestEmailInput
	if !merchantStoreGuestEmailDecode(c, &input) {
		return
	}
	verified, err := model.GetMerchantStoreGuestEmailStatus(c.GetHeader("X-Store-Guest"), input.Email)
	merchantStoreRespond(c, gin.H{"verified": verified}, err)
}

func SendMerchantStoreGuestEmailVerification(c *gin.Context) {
	var input merchantStoreGuestEmailInput
	if !merchantStoreGuestEmailDecode(c, &input) {
		return
	}
	token := c.GetHeader("X-Store-Guest")
	challenge, err := model.BeginMerchantStoreGuestEmailVerification(token, input.Email)
	if err != nil {
		merchantStoreRespond(c, nil, err)
		return
	}
	err = merchantStoreGuestVerificationSender(c.Request.Context(), token, challenge.Email, challenge.ChallengeID, challenge.Code)
	merchantStoreRespond(c, gin.H{"sent": err == nil, "challenge_id": challenge.ChallengeID, "expires_at": challenge.ExpiresAt}, err)
}

func ConfirmMerchantStoreGuestEmailVerification(c *gin.Context) {
	var input merchantStoreGuestEmailConfirmationInput
	if !merchantStoreGuestEmailDecode(c, &input) {
		return
	}
	err := model.ConfirmMerchantStoreGuestEmailVerification(c.GetHeader("X-Store-Guest"), input.Email, input.ChallengeID, input.Code)
	merchantStoreRespond(c, gin.H{"verified": err == nil}, err)
}
