package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

const (
	secureVerificationMethod2FA     = "2fa"
	secureVerificationMethodEmail   = "email"
	secureVerificationMethodPasskey = "passkey"
)

// SendSecurityEmailVerification sends a code only to the email already bound
// to the authenticated account. The email never comes from the request body,
// which prevents this endpoint from becoming a generic mail-sending oracle.
func SendSecurityEmailVerification(c *gin.Context) {
	user, err := model.GetUserCache(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	email := model.NormalizeEmail(user.Email)
	if email == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"success": false,
			"code":    "SECURITY_EMAIL_REQUIRED",
			"message": "请先绑定邮箱后再使用邮箱验证",
		})
		return
	}

	code := common.GenerateVerificationCode(6)
	common.RegisterVerificationCodeWithKey(email, code, common.SecurityEmailVerificationPurpose)
	subject := fmt.Sprintf("%s安全验证邮件", common.SystemName)
	content := fmt.Sprintf(
		"<p>您好，你正在进行%s敏感操作安全验证。</p>"+
			"<p>您的验证码为: <strong>%s</strong></p>"+
			"<p>验证码 %d 分钟内有效。如果不是本人操作，请忽略。</p>",
		common.SystemName,
		code,
		common.VerificationValidMinutes,
	)
	if err := common.SendEmail(subject, email, content); err != nil {
		common.DeleteKey(email, common.SecurityEmailVerificationPurpose)
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "安全验证码已发送",
		"data": gin.H{
			"email_hint": maskSecurityEmail(email),
		},
	})
}

func maskSecurityEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[0] == "" {
		return ""
	}
	local := parts[0]
	if len(local) <= 2 {
		return local[:1] + "***@" + parts[1]
	}
	return local[:1] + "***" + local[len(local)-1:] + "@" + parts[1]
}

type UniversalVerifyRequest struct {
	Method string `json:"method"`
	Code   string `json:"code,omitempty"`
	Scope  string `json:"scope"`
}

func UniversalVerify(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "当前认证方式不支持安全验证"})
		return
	}
	var request UniversalVerifyRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, fmt.Errorf("参数错误: %v", err))
		return
	}
	if !isAllowedSecurityProofScope(request.Scope) {
		common.ApiError(c, errors.New("不支持的安全验证范围"))
		return
	}
	preferredMethods, err := middleware.PreferredSecurityProofMethods(identity.UserID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !containsSecurityProofMethod(preferredMethods, request.Method) {
		common.ApiError(c, errors.New("请绑定邮箱后使用邮箱验证；未绑定邮箱时请使用 Passkey 验证"))
		return
	}

	switch request.Method {
	case secureVerificationMethod2FA:
		if strings.TrimSpace(request.Code) == "" {
			common.ApiError(c, errors.New("验证码不能为空"))
			return
		}
		twoFA, err := model.GetTwoFAByUserId(identity.UserID)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if twoFA == nil || !twoFA.IsEnabled {
			common.ApiError(c, errors.New("用户未启用2FA"))
			return
		}
		if !validateTwoFactorAuth(twoFA, request.Code) {
			common.ApiError(c, errors.New("验证失败，请检查验证码"))
			return
		}
	case secureVerificationMethodEmail:
		if strings.TrimSpace(request.Code) == "" {
			common.ApiError(c, errors.New("验证码不能为空"))
			return
		}
		user, err := model.GetUserCache(identity.UserID)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		email := model.NormalizeEmail(user.Email)
		if email == "" {
			common.ApiError(c, errors.New("用户尚未绑定邮箱"))
			return
		}
		if !common.VerifyCodeWithKey(email, strings.TrimSpace(request.Code), common.SecurityEmailVerificationPurpose) {
			common.ApiError(c, errors.New("验证失败，请检查邮箱验证码"))
			return
		}
		common.DeleteKey(email, common.SecurityEmailVerificationPurpose)
	case secureVerificationMethodPasskey:
		common.ApiError(c, errors.New("Passkey 验证必须使用 Passkey verify 流程"))
		return
	default:
		common.ApiError(c, errors.New("不支持的安全验证方式"))
		return
	}
	proofToken, expiresAt, err := service.IssueSecurityProof(identity, request.Method, []string{request.Scope})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordLog(identity.UserID, model.LogTypeSystem, fmt.Sprintf("通用安全验证成功 (验证方式: %s)", request.Method))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "验证成功",
		"data": gin.H{
			"proof_token": proofToken,
			"expires_at":  expiresAt,
			"method":      request.Method,
			"scope":       request.Scope,
		},
	})
}

const securityProofScopeReviewRunsDelete = "security.review_runs.delete"

func isAllowedSecurityProofScope(scope string) bool {
	switch scope {
	case securityProofScopeChannelKeyRead, securityProofScopePasskeyRegister, securityProofScopePasskeyDelete, securityProofScopeReviewRunsDelete:
		return true
	default:
		return false
	}
}

func containsSecurityProofMethod(methods []string, target string) bool {
	for _, method := range methods {
		if method == target {
			return true
		}
	}
	return false
}
