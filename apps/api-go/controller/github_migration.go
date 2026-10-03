package controller

import (
	"errors"
	"net/http"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauth"
	"github.com/LIghtJUNction/api.lmm.best/service"
	passkeysvc "github.com/LIghtJUNction/api.lmm.best/service/passkey"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	webauthn "github.com/go-webauthn/webauthn/webauthn"
	"gorm.io/gorm"
)

const githubMigrationDeclinedMessage = "GitHub account ownership could not be verified. Sign in another way and relink GitHub in account settings."

type githubMigration struct {
	LegacyID  string `json:"legacy_id"`
	NumericID string `json:"numeric_id"`
}

type githubMigrationRequiredError struct {
	User           *model.User
	Migration      githubMigration
	VerifiedEmails []string
}

func (*githubMigrationRequiredError) Error() string { return githubMigrationDeclinedMessage }

type githubPasskeyLoginPayload struct {
	SessionData     webauthn.SessionData `json:"session_data"`
	AuthVersion     int64                `json:"auth_version"`
	GitHubMigration *githubMigration     `json:"github_migration,omitempty"`
}

func recordGitHubMigrationAudit(user *model.User, outcome, proof string, c *gin.Context) {
	model.RecordOperationAuditLog(user.Id, "GitHub legacy binding migration "+outcome, c.ClientIP(),
		"oauth_legacy_migration", map[string]interface{}{"provider": "github", "outcome": outcome, "proof": proof}, nil, nil)
}

func declineGitHubMigration(user *model.User, proof string, c *gin.Context) {
	recordGitHubMigrationAudit(user, "declined", proof, c)
	common.ApiErrorI18n(c, i18n.MsgOAuthGitHubMigrationDeclined)
}

// Legacy usernames select a candidate only. An enrolled account factor takes
// precedence over email evidence; no binding is rewritten at challenge start.
func beginGitHubMigration(candidate *githubMigrationRequiredError, c *gin.Context) {
	user := candidate.User
	if user.Status != common.UserStatusEnabled {
		declineGitHubMigration(user, "account_unavailable", c)
		return
	}
	twoFAEnabled, err := model.IsTwoFAEnabled(user.Id)
	if err != nil {
		writeAuthSessionError(c, err)
		return
	}
	if twoFAEnabled {
		payload, err := common.Marshal(twoFALoginFlowPayload{AuthVersion: user.AuthVersion, GitHubMigration: &candidate.Migration})
		if err != nil {
			writeAuthSessionError(c, err)
			return
		}
		expires := time.Now().Add(5 * time.Minute)
		token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: model.AuthFlowPurposeTwoFALogin,
			Provider: "github", Intent: model.AuthFlowIntentLogin, UserId: user.Id, Payload: string(payload), ExpiresAt: expires})
		if err != nil {
			writeAuthSessionError(c, err)
			return
		}
		setAuthNoStore(c)
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"require_2fa": true, "flow_token": token, "expires_at": expires.Unix()}})
		return
	}
	credentials, err := model.GetPasskeysByUserID(user.Id)
	if err != nil {
		writeAuthSessionError(c, err)
		return
	}
	if len(credentials) > 0 {
		if !system_setting.GetPasskeySettings().Enabled {
			declineGitHubMigration(user, "passkey_unavailable", c)
			return
		}
		wa, err := passkeysvc.BuildWebAuthn(c.Request)
		if err != nil {
			writeAuthSessionError(c, err)
			return
		}
		options, sessionData, err := wa.BeginLogin(passkeysvc.NewWebAuthnUser(user, credentials), webauthn.WithUserVerification(protocol.VerificationRequired))
		if err != nil {
			writeAuthSessionError(c, err)
			return
		}
		payload, err := common.Marshal(githubPasskeyLoginPayload{SessionData: *sessionData, AuthVersion: user.AuthVersion, GitHubMigration: &candidate.Migration})
		if err != nil {
			writeAuthSessionError(c, err)
			return
		}
		expires := time.Now().Add(5 * time.Minute)
		token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: model.AuthFlowPurposePasskeyLogin,
			Provider: "github", Intent: model.AuthFlowIntentLogin, UserId: user.Id, Payload: string(payload), ExpiresAt: expires})
		if err != nil {
			writeAuthSessionError(c, err)
			return
		}
		setAuthNoStore(c)
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"require_passkey": true, "options": options, "flow_token": token, "expires_at": expires.Unix()}})
		return
	}
	email := model.NormalizeEmail(user.Email)
	for _, verified := range candidate.VerifiedEmails {
		if email != "" && model.NormalizeEmail(verified) == email {
			completeGitHubMigrationLogin(user, candidate.Migration, email, "", "", "verified_email", nil, c)
			return
		}
	}
	declineGitHubMigration(user, "missing_account_evidence", c)
}

func completeGitHubMigrationLogin(user *model.User, migration githubMigration, expectedEmail, flowToken, purpose, proof string, action func(*gorm.DB) error, c *gin.Context) {
	var current model.User
	bundle, err := service.CreateLoginSessionWithAction(user.Id, user.AuthVersion, "oauth:github:"+proof, c.ClientIP(), c.Request.UserAgent(), func(tx *gorm.DB) error {
		if flowToken != "" {
			if _, err := model.ConsumeAuthFlowWithTx(tx, flowToken, model.AuthFlowMatch{Purpose: purpose, Provider: "github", Intent: model.AuthFlowIntentLogin, UserId: user.Id}); err != nil {
				return err
			}
		}
		if err := model.MigrateGitHubIdentityWithTx(tx, user.Id, user.AuthVersion, migration.LegacyID, migration.NumericID, expectedEmail); err != nil {
			return err
		}
		if action != nil {
			if err := action(tx); err != nil {
				return err
			}
		}
		return tx.Where("id = ?", user.Id).First(&current).Error
	})
	if err != nil {
		recordGitHubMigrationAudit(user, "declined", proof, c)
		if errors.Is(err, model.ErrGitHubMigrationInvalid) || errors.Is(err, model.ErrExternalIdentityAlreadyClaimed) ||
			errors.Is(err, model.ErrAuthFlowInvalid) || errors.Is(err, model.ErrAuthFlowExpired) ||
			errors.Is(err, model.ErrAuthFlowConsumed) || errors.Is(err, model.ErrTwoFALoginVerificationFailed) {
			common.ApiErrorI18n(c, i18n.MsgOAuthGitHubMigrationDeclined)
			return
		}
		writeAuthSessionError(c, err)
		return
	}
	recordGitHubMigrationAudit(&current, "success", proof, c)
	writeLoginBundle(&current, &current, bundle, c)
}

func finishGitHubPasskeyMigration(c *gin.Context, token string, flow *model.AuthFlow, payload githubPasskeyLoginPayload, wa *webauthn.WebAuthn, assertion *protocol.ParsedCredentialAssertionData) {
	user, err := model.GetUserById(flow.UserId, false)
	if err != nil {
		writeAuthSessionError(c, err)
		return
	}
	if flow.Provider != "github" || flow.Intent != model.AuthFlowIntentLogin || payload.AuthVersion <= 0 || user.AuthVersion != payload.AuthVersion || user.Status != common.UserStatusEnabled || user.GitHubId != payload.GitHubMigration.LegacyID {
		declineGitHubMigration(user, "passkey", c)
		return
	}
	credentials, err := model.GetPasskeysByUserID(user.Id)
	if err != nil {
		writeAuthSessionError(c, err)
		return
	}
	credential, err := wa.ValidateLogin(passkeysvc.NewWebAuthnUser(user, credentials), payload.SessionData, assertion)
	if err != nil {
		rejectGitHubPasskeyAssertion(user, token, c)
		return
	}
	if credential == nil || !credential.Flags.UserVerified {
		rejectGitHubPasskeyAssertion(user, token, c)
		return
	}
	completeGitHubMigrationLogin(user, *payload.GitHubMigration, "", token, model.AuthFlowPurposePasskeyLogin, "passkey", func(tx *gorm.DB) error {
		return model.UpdatePasskeyAssertionStateWithTx(tx, user.Id, credential, time.Now())
	}, c)
}

func rejectGitHubPasskeyAssertion(user *model.User, token string, c *gin.Context) {
	// Keep the existing passkey ceremony's single-attempt rule for rejected
	// assertions. A verified assertion whose session transaction fails remains
	// retryable because that transaction rolls challenge consumption back.
	if _, err := model.ConsumeAuthFlow(token, model.AuthFlowMatch{Purpose: model.AuthFlowPurposePasskeyLogin,
		Provider: "github", Intent: model.AuthFlowIntentLogin, UserId: user.Id}); err != nil {
		writeAuthSessionError(c, err)
		return
	}
	declineGitHubMigration(user, "passkey", c)
}

func findGitHubOAuthUser(oauthUser *oauth.OAuthUser) (*model.User, error) {
	if oauthUser == nil || !model.IsNumericGitHubID(oauthUser.ProviderUserID) {
		return nil, model.ErrGitHubMigrationInvalid
	}
	user, err := model.FindGitHubIdentity(oauthUser.ProviderUserID, true)
	if err == nil {
		if user.DeletedAt.Valid {
			return nil, &OAuthUserDeletedError{}
		}
		return user, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	legacy, _ := oauthUser.Extra["legacy_id"].(string)
	if legacy == "" || model.IsNumericGitHubID(legacy) {
		return nil, gorm.ErrRecordNotFound
	}
	user, err = model.FindGitHubIdentity(legacy, false)
	if err != nil {
		return nil, err
	}
	return nil, &githubMigrationRequiredError{User: user, Migration: githubMigration{LegacyID: legacy, NumericID: oauthUser.ProviderUserID}, VerifiedEmails: oauthUser.VerifiedEmails}
}
