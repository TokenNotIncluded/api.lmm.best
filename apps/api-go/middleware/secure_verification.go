package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// SecureVerificationRequired protects channel key disclosure. Other sensitive
// operations validate their narrower proof scopes in their controller.
func SecureVerificationRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !RequireSecurityProof(c, "channel.key.read", []string{"email", "2fa", "passkey"}) {
			return
		}
		c.Set("secure_verified", true)
		c.Next()
	}
}

// RequireSecurityProof validates a proof against the authenticated dashboard
// session and writes the shared proof error contract on failure.
func RequireSecurityProof(c *gin.Context, requiredScope string, allowedMethods []string) bool {
	identity, ok := GetSessionAuthIdentity(c)
	if !ok {
		securityProofError(c, "SECURITY_PROOF_INVALID", "安全验证状态无效")
		return false
	}
	preferredMethods, err := PreferredSecurityProofMethods(identity.UserID)
	if err != nil {
		securityProofError(c, "SECURITY_PROOF_INVALID", "安全验证状态无效")
		return false
	}
	// The configured list remains part of the call contract, but the account
	// policy is authoritative: a bound email must use email verification; an
	// account without one may use only its existing Passkey.
	_ = allowedMethods
	raw := strings.TrimSpace(c.GetHeader("X-Security-Proof"))
	if raw == "" {
		securityProofError(c, "SECURITY_PROOF_REQUIRED", "需要安全验证")
		return false
	}
	if _, err := service.VerifySecurityProof(raw, identity, requiredScope, preferredMethods); err != nil {
		switch {
		case errors.Is(err, service.ErrAuthTokenExpired):
			securityProofError(c, "SECURITY_PROOF_EXPIRED", "安全验证已过期")
		case errors.Is(err, service.ErrProofScope):
			securityProofError(c, "SECURITY_PROOF_SCOPE_MISMATCH", "安全验证范围不匹配")
		case errors.Is(err, service.ErrProofMethod):
			securityProofError(c, "SECURITY_PROOF_METHOD_MISMATCH", "安全验证方式不匹配")
		default:
			securityProofError(c, "SECURITY_PROOF_INVALID", "安全验证状态无效")
		}
		return false
	}
	return true
}

// PreferredSecurityProofMethods returns the only proof method accepted for
// sensitive dashboard actions. Email is the primary path when bound, followed
// by an enabled 2FA factor; Passkey is the compatibility fallback otherwise.
func PreferredSecurityProofMethods(userID int) ([]string, error) {
	user, err := model.GetUserCache(userID)
	if err != nil {
		return nil, err
	}
	if model.NormalizeEmail(user.Email) != "" {
		return []string{"email"}, nil
	}
	twoFA, err := model.GetTwoFAByUserId(userID)
	if err != nil {
		return nil, err
	}
	if twoFA != nil && twoFA.IsEnabled {
		return []string{"2fa"}, nil
	}
	return []string{"passkey"}, nil
}

func securityProofError(c *gin.Context, code, message string) {
	c.JSON(http.StatusForbidden, gin.H{
		"success": false,
		"message": message,
		"code":    code,
	})
	c.Abort()
}
