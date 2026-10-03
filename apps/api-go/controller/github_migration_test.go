package controller

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauth"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type githubMigrationResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		AccessToken    string `json:"access_token"`
		FlowToken      string `json:"flow_token"`
		RequireTwoFA   bool   `json:"require_2fa"`
		RequirePasskey bool   `json:"require_passkey"`
		Action         string `json:"action"`
	} `json:"data"`
}

func setupGitHubMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	require.NoError(t, i18n.Init())
	previousGinMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType := common.MainDatabaseType()
	previousRedis, previousSecret := common.RedisEnabled, common.SessionSecret
	previousActiveLimit, previousIssuanceLimit := common.UserSessionActiveLimit, common.UserSessionIssuanceLimit
	previousIssuanceWindow := common.UserSessionIssuanceWindowSeconds
	previousRegister, previousOAuthRegister := common.RegisterEnabled, common.OAuthRegisterEnabled
	previousEmailVerification, previousGitHubEnabled := common.EmailVerificationEnabled, common.GitHubOAuthEnabled
	passkeySettings := system_setting.GetPasskeySettings()
	previousPasskeySettings := *passkeySettings
	legalSettings := system_setting.GetLegalSettings()
	previousLegalSettings := *legalSettings
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	options := make(map[string]string, len(previousOptions))
	for key, value := range previousOptions {
		options[key] = value
	}
	delete(options, common.RegistrationDisabledMethodsOptionKey)
	common.OptionMap = options
	common.OptionMapRWMutex.Unlock()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "github-migration.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuthFlow{},
		&model.ExternalIdentityClaim{}, &model.TwoFA{}, &model.TwoFABackupCode{},
		&model.PasskeyCredential{}, &model.Token{}, &model.TopUp{}, &model.Log{}))
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.SessionSecret = "github-migration-controller-test-secret"
	common.UserSessionActiveLimit = 10
	common.UserSessionIssuanceLimit = 100
	common.UserSessionIssuanceWindowSeconds = 3600
	common.RegisterEnabled, common.OAuthRegisterEnabled = true, true
	common.EmailVerificationEnabled, common.GitHubOAuthEnabled = false, true
	*passkeySettings = system_setting.PasskeySettings{Enabled: true, RPID: "example.com", Origins: "https://example.com", UserVerification: "preferred"}
	legalSettings.UserAgreement, legalSettings.PrivacyPolicy = "Test user agreement", "Test privacy policy"
	t.Cleanup(func() {
		gin.SetMode(previousGinMode)
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled, common.SessionSecret = previousRedis, previousSecret
		common.UserSessionActiveLimit, common.UserSessionIssuanceLimit = previousActiveLimit, previousIssuanceLimit
		common.UserSessionIssuanceWindowSeconds = previousIssuanceWindow
		common.RegisterEnabled, common.OAuthRegisterEnabled = previousRegister, previousOAuthRegister
		common.EmailVerificationEnabled, common.GitHubOAuthEnabled = previousEmailVerification, previousGitHubEnabled
		*passkeySettings, *legalSettings = previousPasskeySettings, previousLegalSettings
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func createGitHubMigrationUser(t *testing.T, db *gorm.DB, username, subject, email string) *model.User {
	t.Helper()
	user := &model.User{Username: username, AffCode: username, Password: "unused", GitHubId: subject,
		Email: email, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, db.Create(user).Error)
	return user
}

func newGitHubMigrationContext(method, path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(method, "https://example.com"+path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, response
}

func decodeGitHubMigrationResponse(t *testing.T, response *httptest.ResponseRecorder) githubMigrationResponse {
	t.Helper()
	var result githubMigrationResponse
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result), response.Body.String())
	return result
}

func requireGitHubMigrationState(t *testing.T, db *gorm.DB, userID int, subject string, sessionCount int64) {
	t.Helper()
	var user model.User
	require.NoError(t, db.First(&user, userID).Error)
	assert.Equal(t, subject, user.GitHubId)
	var count int64
	require.NoError(t, db.Model(&model.UserSession{}).Where("user_id = ?", userID).Count(&count).Error)
	assert.Equal(t, sessionCount, count)
}

func requireGitHubMigrationNoTokens(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	assert.Empty(t, decodeGitHubMigrationResponse(t, response).Data.AccessToken)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == service.RefreshCookieName {
			assert.Empty(t, cookie.Value, "a rejected or pending migration must not publish a refresh token")
		}
	}
}

func requireGitHubMigrationClaimCount(t *testing.T, db *gorm.DB, userID int, expected int64) {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&model.ExternalIdentityClaim{}).
		Where("provider = ? AND user_id = ?", model.ExternalIdentityProviderGitHub, userID).Count(&count).Error)
	assert.Equal(t, expected, count)
}

func githubMigrationCandidate(t *testing.T, user *model.User, verifiedEmails []string) *githubMigrationRequiredError {
	t.Helper()
	email := user.Email
	if len(verifiedEmails) > 0 {
		email = verifiedEmails[0]
	}
	_, err := findGitHubOAuthUser(&oauth.OAuthUser{ProviderUserID: "42", Email: email,
		EmailVerified: len(verifiedEmails) > 0, VerifiedEmails: verifiedEmails,
		Extra: map[string]any{"legacy_id": user.GitHubId}})
	var candidate *githubMigrationRequiredError
	require.ErrorAs(t, err, &candidate)
	require.Equal(t, user.Id, candidate.User.Id)
	return candidate
}

func beginGitHubMigrationForTest(t *testing.T, candidate *githubMigrationRequiredError) (githubMigrationResponse, *httptest.ResponseRecorder) {
	t.Helper()
	c, response := newGitHubMigrationContext(http.MethodGet, "/api/oauth/github", "")
	beginGitHubMigration(candidate, c)
	return decodeGitHubMigrationResponse(t, response), response
}

func addGitHubMigrationTwoFA(t *testing.T, db *gorm.DB, user *model.User) string {
	t.Helper()
	key, err := common.GenerateTOTPSecret(user.Username)
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.TwoFA{UserId: user.Id, Secret: key.Secret(), IsEnabled: true}).Error)
	return key.Secret()
}

func finishGitHubMigrationTwoFA(t *testing.T, token, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := common.Marshal(Verify2FARequest{FlowToken: token, Code: code})
	require.NoError(t, err)
	c, response := newGitHubMigrationContext(http.MethodPost, "/api/user/login/2fa", string(body))
	Verify2FALogin(c)
	return response
}

func TestGitHubMigrationUsesVerifiedNormalizedSecondaryEmailOnce(t *testing.T) {
	db := setupGitHubMigrationTestDB(t)
	user := createGitHubMigrationUser(t, db, "secondary-owner", "legacy-owner", " Owner@Example.com ")
	candidate := githubMigrationCandidate(t, user, []string{"primary@example.com", " OWNER@example.COM "})

	result, response := beginGitHubMigrationForTest(t, candidate)

	require.True(t, result.Success, response.Body.String())
	require.NotEmpty(t, result.Data.AccessToken)
	assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	var refreshCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == service.RefreshCookieName {
			refreshCookie = cookie
		}
	}
	require.NotNil(t, refreshCookie)
	assert.NotEmpty(t, refreshCookie.Value)
	assert.True(t, refreshCookie.HttpOnly)
	requireGitHubMigrationState(t, db, user.Id, "42", 1)
	requireGitHubMigrationClaimCount(t, db, user.Id, 1)
	var session model.UserSession
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&session).Error)
	assert.Equal(t, "oauth:github:verified_email", session.LoginMethod)
	var audit model.Log
	require.NoError(t, db.Where("content = ?", "GitHub legacy binding migration success").First(&audit).Error)
	assert.Contains(t, audit.Other, `"proof":"verified_email"`)
	assert.NotContains(t, audit.Other, "example.com")

	// A stale candidate cannot overwrite an already migrated binding or issue another session.
	replayed, replayResponse := beginGitHubMigrationForTest(t, candidate)
	assert.False(t, replayed.Success, replayResponse.Body.String())
	requireGitHubMigrationNoTokens(t, replayResponse)
	requireGitHubMigrationState(t, db, user.Id, "42", 1)
	requireGitHubMigrationClaimCount(t, db, user.Id, 1)
}

func TestGitHubMigrationRejectsMissingOrMismatchingVerifiedEmail(t *testing.T) {
	for _, test := range []struct {
		name           string
		email          string
		verifiedEmails []string
	}{
		{name: "unverified matching profile email", email: "owner@example.com"},
		{name: "different verified owner", email: "owner@example.com", verifiedEmails: []string{"other@example.com"}},
		{name: "missing bound email", verifiedEmails: []string{"owner@example.com"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := setupGitHubMigrationTestDB(t)
			user := createGitHubMigrationUser(t, db, "email-owner", "legacy-owner", test.email)
			candidate := githubMigrationCandidate(t, user, test.verifiedEmails)

			result, response := beginGitHubMigrationForTest(t, candidate)

			assert.False(t, result.Success)
			assert.Equal(t, githubMigrationDeclinedMessage, result.Message)
			requireGitHubMigrationNoTokens(t, response)
			requireGitHubMigrationState(t, db, user.Id, "legacy-owner", 0)
			requireGitHubMigrationClaimCount(t, db, user.Id, 0)
		})
	}
}

func TestGitHubMigrationTwoFARequiresChallengeDespiteVerifiedEmail(t *testing.T) {
	db := setupGitHubMigrationTestDB(t)
	user := createGitHubMigrationUser(t, db, "twofa-owner", "legacy-owner", "owner@example.com")
	secret := addGitHubMigrationTwoFA(t, db, user)
	candidate := githubMigrationCandidate(t, user, []string{user.Email})

	result, response := beginGitHubMigrationForTest(t, candidate)

	require.True(t, result.Success, response.Body.String())
	assert.True(t, result.Data.RequireTwoFA)
	require.NotEmpty(t, result.Data.FlowToken)
	requireGitHubMigrationNoTokens(t, response)
	requireGitHubMigrationState(t, db, user.Id, "legacy-owner", 0)
	requireGitHubMigrationClaimCount(t, db, user.Id, 0)
	match := model.AuthFlowMatch{Purpose: model.AuthFlowPurposeTwoFALogin, Provider: "github", Intent: model.AuthFlowIntentLogin, UserId: user.Id}
	flow, err := model.GetAuthFlow(result.Data.FlowToken, match)
	require.NoError(t, err)
	var payload twoFALoginFlowPayload
	require.NoError(t, common.UnmarshalJsonStr(flow.Payload, &payload))
	require.NotNil(t, payload.GitHubMigration)
	assert.Equal(t, githubMigration{LegacyID: "legacy-owner", NumericID: "42"}, *payload.GitHubMigration)
	assert.Equal(t, user.AuthVersion, payload.AuthVersion)

	invalidResponse := finishGitHubMigrationTwoFA(t, result.Data.FlowToken, "bad-code")
	assert.False(t, decodeGitHubMigrationResponse(t, invalidResponse).Success)
	requireGitHubMigrationNoTokens(t, invalidResponse)
	requireGitHubMigrationState(t, db, user.Id, "legacy-owner", 0)
	_, err = model.GetAuthFlow(result.Data.FlowToken, match)
	require.NoError(t, err, "a failed factor must not consume the challenge")

	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	validResponse := finishGitHubMigrationTwoFA(t, result.Data.FlowToken, code)
	validResult := decodeGitHubMigrationResponse(t, validResponse)
	require.True(t, validResult.Success, validResponse.Body.String())
	assert.NotEmpty(t, validResult.Data.AccessToken)
	requireGitHubMigrationState(t, db, user.Id, "42", 1)
	requireGitHubMigrationClaimCount(t, db, user.Id, 1)
	_, err = model.GetAuthFlow(result.Data.FlowToken, match)
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)

	replayResponse := finishGitHubMigrationTwoFA(t, result.Data.FlowToken, code)
	assert.False(t, decodeGitHubMigrationResponse(t, replayResponse).Success)
	requireGitHubMigrationNoTokens(t, replayResponse)
	requireGitHubMigrationState(t, db, user.Id, "42", 1)
}

func TestGitHubMigrationSessionInsertFailureRollsBackBindingAndChallenge(t *testing.T) {
	for _, requireTwoFA := range []bool{false, true} {
		name := "verified email"
		if requireTwoFA {
			name = "2FA challenge"
		}
		t.Run(name, func(t *testing.T) {
			db := setupGitHubMigrationTestDB(t)
			user := createGitHubMigrationUser(t, db, "rollback-owner", "legacy-owner", "owner@example.com")
			secret := ""
			if requireTwoFA {
				secret = addGitHubMigrationTwoFA(t, db, user)
			}
			candidate := githubMigrationCandidate(t, user, []string{user.Email})
			require.NoError(t, db.Exec(`CREATE TRIGGER reject_github_login_session BEFORE INSERT ON user_sessions BEGIN SELECT RAISE(ABORT, 'injected session failure'); END`).Error)
			result, response := beginGitHubMigrationForTest(t, candidate)
			if requireTwoFA {
				require.True(t, result.Data.RequireTwoFA, response.Body.String())
				code, err := totp.GenerateCode(secret, time.Now())
				require.NoError(t, err)
				response = finishGitHubMigrationTwoFA(t, result.Data.FlowToken, code)
				_, err = model.GetAuthFlow(result.Data.FlowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeTwoFALogin})
				require.NoError(t, err, "session insert rollback must leave the challenge usable")
			}

			assert.False(t, decodeGitHubMigrationResponse(t, response).Success)
			requireGitHubMigrationNoTokens(t, response)
			requireGitHubMigrationState(t, db, user.Id, "legacy-owner", 0)
			requireGitHubMigrationClaimCount(t, db, user.Id, 0)
			var userAfterFailure model.User
			require.NoError(t, db.First(&userAfterFailure, user.Id).Error)
			assert.Zero(t, userAfterFailure.LastLoginAt)
			var successAudits int64
			require.NoError(t, db.Model(&model.Log{}).Where("content = ?", "GitHub legacy binding migration success").Count(&successAudits).Error)
			assert.Zero(t, successAudits)

			require.NoError(t, db.Exec(`DROP TRIGGER reject_github_login_session`).Error)
			if requireTwoFA {
				code, err := totp.GenerateCode(secret, time.Now())
				require.NoError(t, err)
				response = finishGitHubMigrationTwoFA(t, result.Data.FlowToken, code)
			} else {
				_, response = beginGitHubMigrationForTest(t, candidate)
			}
			require.True(t, decodeGitHubMigrationResponse(t, response).Success, response.Body.String())
			requireGitHubMigrationState(t, db, user.Id, "42", 1)
		})
	}
}

func TestGitHubMigrationBackupCodeConsumptionRollsBackWithSessionFailure(t *testing.T) {
	db := setupGitHubMigrationTestDB(t)
	user := createGitHubMigrationUser(t, db, "backup-owner", "legacy-owner", "owner@example.com")
	addGitHubMigrationTwoFA(t, db, user)
	const backupCode = "ABCD-EFGH"
	codeHash, err := common.HashBackupCode(backupCode)
	require.NoError(t, err)
	backup := model.TwoFABackupCode{UserId: user.Id, CodeHash: codeHash}
	require.NoError(t, db.Create(&backup).Error)
	result, response := beginGitHubMigrationForTest(t, githubMigrationCandidate(t, user, []string{user.Email}))
	require.True(t, result.Data.RequireTwoFA, response.Body.String())

	for attempt := 1; attempt <= 2; attempt++ {
		response = finishGitHubMigrationTwoFA(t, result.Data.FlowToken, "bad-code")
		assert.False(t, decodeGitHubMigrationResponse(t, response).Success)
		requireGitHubMigrationNoTokens(t, response)
		var factor model.TwoFA
		require.NoError(t, db.Where("user_id = ?", user.Id).First(&factor).Error)
		assert.Equal(t, attempt, factor.FailedAttempts, "rejected attempts must remain recorded while the flow stays fresh")
		assert.Nil(t, factor.LastUsedAt)
		_, err = model.GetAuthFlow(result.Data.FlowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeTwoFALogin})
		require.NoError(t, err)
	}
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_github_backup_session BEFORE INSERT ON user_sessions BEGIN SELECT RAISE(ABORT, 'injected session failure'); END`).Error)

	response = finishGitHubMigrationTwoFA(t, result.Data.FlowToken, backupCode)

	assert.False(t, decodeGitHubMigrationResponse(t, response).Success)
	requireGitHubMigrationNoTokens(t, response)
	requireGitHubMigrationState(t, db, user.Id, "legacy-owner", 0)
	requireGitHubMigrationClaimCount(t, db, user.Id, 0)
	_, err = model.GetAuthFlow(result.Data.FlowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeTwoFALogin})
	require.NoError(t, err)
	var backupAfter model.TwoFABackupCode
	require.NoError(t, db.First(&backupAfter, backup.Id).Error)
	assert.False(t, backupAfter.IsUsed)
	assert.Nil(t, backupAfter.UsedAt)
	var factor model.TwoFA
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&factor).Error)
	assert.Equal(t, 2, factor.FailedAttempts)
	assert.Nil(t, factor.LastUsedAt)

	require.NoError(t, db.Exec(`DROP TRIGGER reject_github_backup_session`).Error)
	response = finishGitHubMigrationTwoFA(t, result.Data.FlowToken, backupCode)
	require.True(t, decodeGitHubMigrationResponse(t, response).Success, response.Body.String())
	requireGitHubMigrationState(t, db, user.Id, "42", 1)
	require.NoError(t, db.First(&backupAfter, backup.Id).Error)
	assert.True(t, backupAfter.IsUsed)
	assert.NotNil(t, backupAfter.UsedAt)
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&factor).Error)
	assert.Zero(t, factor.FailedAttempts)
	assert.NotNil(t, factor.LastUsedAt)
}

func addGitHubMigrationPasskey(t *testing.T, db *gorm.DB, user *model.User) (*ecdsa.PrivateKey, *model.PasskeyCredential) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	publicKey, err := webauthncbor.Marshal(map[int]any{
		1: 2, 3: -7, -1: 1, // EC2 key, ES256 signature, P-256 curve.
		-2: key.X.FillBytes(make([]byte, 32)), -3: key.Y.FillBytes(make([]byte, 32)),
	})
	require.NoError(t, err)
	rawID := make([]byte, 32)
	_, err = rand.Read(rawID)
	require.NoError(t, err)
	credential := &model.PasskeyCredential{UserID: user.Id, Name: "Test authenticator",
		CredentialID: base64.StdEncoding.EncodeToString(rawID), PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		AttestationType: "none", UserPresent: true, UserVerified: true}
	require.NoError(t, db.Create(credential).Error)
	return key, credential
}

func signedGitHubMigrationPasskeyBody(t *testing.T, token string, session webauthn.SessionData, credential *model.PasskeyCredential, key *ecdsa.PrivateKey, userVerified bool) string {
	t.Helper()
	rawID, err := base64.StdEncoding.DecodeString(credential.CredentialID)
	require.NoError(t, err)
	clientData, err := common.Marshal(protocol.CollectedClientData{Type: protocol.AssertCeremony, Challenge: session.Challenge, Origin: "https://example.com"})
	require.NoError(t, err)
	authData := make([]byte, 37)
	rpHash := sha256.Sum256([]byte(session.RelyingPartyID))
	copy(authData[:32], rpHash[:])
	authData[32] = 0x01 // User present.
	if userVerified {
		authData[32] |= 0x04
	}
	binary.BigEndian.PutUint32(authData[33:], 1)
	clientHash := sha256.Sum256(clientData)
	signatureHash := sha256.Sum256(append(authData, clientHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, key, signatureHash[:])
	require.NoError(t, err)
	encode := base64.RawURLEncoding.EncodeToString
	assertion, err := common.Marshal(gin.H{
		"id": encode(rawID), "rawId": encode(rawID), "type": "public-key",
		"response": gin.H{"clientDataJSON": encode(clientData), "authenticatorData": encode(authData),
			"signature": encode(signature), "userHandle": encode(session.UserID)},
	})
	require.NoError(t, err)
	body, err := common.Marshal(passkeyFinishRequest{FlowToken: token, Credential: assertion})
	require.NoError(t, err)
	return string(body)
}

func finishGitHubMigrationPasskey(body string) *httptest.ResponseRecorder {
	c, response := newGitHubMigrationContext(http.MethodPost, "/api/user/passkey/login/finish", body)
	PasskeyLoginFinish(c)
	return response
}

func TestGitHubMigrationPasskeyRequiresVerifiedAssertionAndAtomicLogin(t *testing.T) {
	db := setupGitHubMigrationTestDB(t)
	user := createGitHubMigrationUser(t, db, "passkey-owner", "legacy-owner", "owner@example.com")
	key, credential := addGitHubMigrationPasskey(t, db, user)
	candidate := githubMigrationCandidate(t, user, []string{user.Email})
	result, response := beginGitHubMigrationForTest(t, candidate)
	require.True(t, result.Success, response.Body.String())
	require.True(t, result.Data.RequirePasskey)
	requireGitHubMigrationNoTokens(t, response)
	requireGitHubMigrationState(t, db, user.Id, "legacy-owner", 0)
	match := model.AuthFlowMatch{Purpose: model.AuthFlowPurposePasskeyLogin, Provider: "github", Intent: model.AuthFlowIntentLogin, UserId: user.Id}
	flow, err := model.GetAuthFlow(result.Data.FlowToken, match)
	require.NoError(t, err)
	var payload githubPasskeyLoginPayload
	require.NoError(t, common.UnmarshalJsonStr(flow.Payload, &payload))
	require.Equal(t, protocol.VerificationRequired, payload.SessionData.UserVerification,
		"migration must require verified-user evidence even when ordinary passkey settings prefer it")

	// This assertion has a valid signature but lacks the authenticator's UV flag.
	response = finishGitHubMigrationPasskey(signedGitHubMigrationPasskeyBody(t, result.Data.FlowToken, payload.SessionData, credential, key, false))
	assert.False(t, decodeGitHubMigrationResponse(t, response).Success)
	requireGitHubMigrationNoTokens(t, response)
	requireGitHubMigrationState(t, db, user.Id, "legacy-owner", 0)
	invalidToken := result.Data.FlowToken
	_, err = model.GetAuthFlow(result.Data.FlowToken, match)
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed, "an invalid assertion is a single attempt")

	result, response = beginGitHubMigrationForTest(t, candidate)
	require.True(t, result.Data.RequirePasskey, response.Body.String())
	require.NotEqual(t, invalidToken, result.Data.FlowToken)
	flow, err = model.GetAuthFlow(result.Data.FlowToken, match)
	require.NoError(t, err)
	require.NoError(t, common.UnmarshalJsonStr(flow.Payload, &payload))

	// Start the successful ceremony with the stored UV flag unset so the final
	// assertion verifies persistence of the authenticator's new state as well.
	require.NoError(t, db.Model(&model.PasskeyCredential{}).Where("id = ?", credential.ID).Update("user_verified", false).Error)
	validBody := signedGitHubMigrationPasskeyBody(t, result.Data.FlowToken, payload.SessionData, credential, key, true)
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_github_passkey_session BEFORE INSERT ON user_sessions BEGIN SELECT RAISE(ABORT, 'injected session failure'); END`).Error)
	response = finishGitHubMigrationPasskey(validBody)
	assert.False(t, decodeGitHubMigrationResponse(t, response).Success)
	requireGitHubMigrationNoTokens(t, response)
	requireGitHubMigrationState(t, db, user.Id, "legacy-owner", 0)
	requireGitHubMigrationClaimCount(t, db, user.Id, 0)
	_, err = model.GetAuthFlow(result.Data.FlowToken, match)
	require.NoError(t, err)
	var credentialAfter model.PasskeyCredential
	require.NoError(t, db.First(&credentialAfter, credential.ID).Error)
	assert.Zero(t, credentialAfter.SignCount)
	assert.False(t, credentialAfter.UserVerified)
	assert.Nil(t, credentialAfter.LastUsedAt)

	require.NoError(t, db.Exec(`DROP TRIGGER reject_github_passkey_session`).Error)
	response = finishGitHubMigrationPasskey(validBody)
	validResult := decodeGitHubMigrationResponse(t, response)
	require.True(t, validResult.Success, response.Body.String())
	assert.NotEmpty(t, validResult.Data.AccessToken)
	requireGitHubMigrationState(t, db, user.Id, "42", 1)
	requireGitHubMigrationClaimCount(t, db, user.Id, 1)
	_, err = model.GetAuthFlow(result.Data.FlowToken, match)
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	require.NoError(t, db.First(&credentialAfter, credential.ID).Error)
	assert.EqualValues(t, 1, credentialAfter.SignCount)
	assert.True(t, credentialAfter.UserVerified)
	assert.NotNil(t, credentialAfter.LastUsedAt)
	assert.Equal(t, credential.PublicKey, credentialAfter.PublicKey)

	response = finishGitHubMigrationPasskey(validBody)
	assert.False(t, decodeGitHubMigrationResponse(t, response).Success)
	requireGitHubMigrationNoTokens(t, response)
	requireGitHubMigrationState(t, db, user.Id, "42", 1)
}

func TestGitHubMigrationInvalidPasskeySignatureConsumesChallenge(t *testing.T) {
	db := setupGitHubMigrationTestDB(t)
	user := createGitHubMigrationUser(t, db, "invalid-signature-owner", "legacy-owner", "owner@example.com")
	_, credential := addGitHubMigrationPasskey(t, db, user)
	wrongKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	result, response := beginGitHubMigrationForTest(t, githubMigrationCandidate(t, user, []string{user.Email}))
	require.True(t, result.Data.RequirePasskey, response.Body.String())
	match := model.AuthFlowMatch{Purpose: model.AuthFlowPurposePasskeyLogin, Provider: "github", Intent: model.AuthFlowIntentLogin, UserId: user.Id}
	flow, err := model.GetAuthFlow(result.Data.FlowToken, match)
	require.NoError(t, err)
	var payload githubPasskeyLoginPayload
	require.NoError(t, common.UnmarshalJsonStr(flow.Payload, &payload))

	// Every assertion field, including UV, is valid; only the signing key is wrong.
	response = finishGitHubMigrationPasskey(signedGitHubMigrationPasskeyBody(t, result.Data.FlowToken, payload.SessionData, credential, wrongKey, true))

	assert.False(t, decodeGitHubMigrationResponse(t, response).Success)
	requireGitHubMigrationNoTokens(t, response)
	requireGitHubMigrationState(t, db, user.Id, "legacy-owner", 0)
	requireGitHubMigrationClaimCount(t, db, user.Id, 0)
	_, err = model.GetAuthFlow(result.Data.FlowToken, match)
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	var credentialAfter model.PasskeyCredential
	require.NoError(t, db.First(&credentialAfter, credential.ID).Error)
	assert.Zero(t, credentialAfter.SignCount)
	assert.Nil(t, credentialAfter.LastUsedAt)
}

func TestGitHubMigrationUnavailablePasskeyDoesNotFallBackToEmail(t *testing.T) {
	db := setupGitHubMigrationTestDB(t)
	user := createGitHubMigrationUser(t, db, "disabled-passkey-owner", "legacy-owner", "owner@example.com")
	addGitHubMigrationPasskey(t, db, user)
	system_setting.GetPasskeySettings().Enabled = false

	result, response := beginGitHubMigrationForTest(t, githubMigrationCandidate(t, user, []string{user.Email}))

	assert.False(t, result.Success)
	assert.Equal(t, githubMigrationDeclinedMessage, result.Message)
	requireGitHubMigrationNoTokens(t, response)
	requireGitHubMigrationState(t, db, user.Id, "legacy-owner", 0)
	requireGitHubMigrationClaimCount(t, db, user.Id, 0)
}

func TestGitHubMigrationRejectsTwoFAChallengeAfterAccountChanges(t *testing.T) {
	for _, change := range []struct {
		name   string
		values map[string]any
	}{
		{name: "authentication version advanced", values: map[string]any{"auth_version": 2}},
		{name: "GitHub relinked", values: map[string]any{"github_id": "99"}},
		{name: "account disabled", values: map[string]any{"status": common.UserStatusDisabled}},
	} {
		t.Run(change.name, func(t *testing.T) {
			db := setupGitHubMigrationTestDB(t)
			user := createGitHubMigrationUser(t, db, "changed-owner", "legacy-owner", "owner@example.com")
			secret := addGitHubMigrationTwoFA(t, db, user)
			result, response := beginGitHubMigrationForTest(t, githubMigrationCandidate(t, user, nil))
			require.True(t, result.Data.RequireTwoFA, response.Body.String())
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Updates(change.values).Error)
			code, err := totp.GenerateCode(secret, time.Now())
			require.NoError(t, err)

			response = finishGitHubMigrationTwoFA(t, result.Data.FlowToken, code)

			assert.False(t, decodeGitHubMigrationResponse(t, response).Success)
			requireGitHubMigrationNoTokens(t, response)
			subject := "legacy-owner"
			if change.name == "GitHub relinked" {
				subject = "99"
			}
			requireGitHubMigrationState(t, db, user.Id, subject, 0)
			requireGitHubMigrationClaimCount(t, db, user.Id, 0)
		})
	}
}

func TestGitHubNumericIdentityWinsOverLegacyUsernameCandidate(t *testing.T) {
	db := setupGitHubMigrationTestDB(t)
	numeric := createGitHubMigrationUser(t, db, "numeric-owner", "42", "")
	legacy := createGitHubMigrationUser(t, db, "legacy-owner", "recycled-login", "owner@example.com")
	oauthUser := &oauth.OAuthUser{ProviderUserID: "42", VerifiedEmails: []string{legacy.Email}, Extra: map[string]any{"legacy_id": legacy.GitHubId}}

	user, err := findOrCreateOAuthUser(nil, &oauth.GitHubProvider{}, oauthUser, "", false)

	require.NoError(t, err)
	assert.Equal(t, numeric.Id, user.Id)
	c, response := newGitHubMigrationContext(http.MethodGet, "/api/oauth/github", "")
	setupLogin(user, c)
	result := decodeGitHubMigrationResponse(t, response)
	require.True(t, result.Success, response.Body.String())
	assert.NotEmpty(t, result.Data.AccessToken)
	requireGitHubMigrationState(t, db, numeric.Id, "42", 1)
	requireGitHubMigrationState(t, db, legacy.Id, "recycled-login", 0)
}

func TestGitHubDeletedNumericIdentityCannotRegisterAgain(t *testing.T) {
	db := setupGitHubMigrationTestDB(t)
	deleted := createGitHubMigrationUser(t, db, "deleted-numeric-owner", "42", "")
	require.NoError(t, db.Delete(deleted).Error)

	_, err := findOrCreateOAuthUser(nil, &oauth.GitHubProvider{}, &oauth.OAuthUser{ProviderUserID: "42"}, "", true)

	var deletedError *OAuthUserDeletedError
	assert.ErrorAs(t, err, &deletedError)
	var count int64
	require.NoError(t, db.Unscoped().Model(&model.User{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestGitHubAllDigitsLegacyLoginNeverSelectsMigrationCandidate(t *testing.T) {
	for _, legacyID := range []string{"0", "00042", "12345"} {
		t.Run(legacyID, func(t *testing.T) {
			db := setupGitHubMigrationTestDB(t)
			user := createGitHubMigrationUser(t, db, "digit-owner", legacyID, "owner@example.com")

			_, err := findGitHubOAuthUser(&oauth.OAuthUser{ProviderUserID: "42", VerifiedEmails: []string{user.Email}, Extra: map[string]any{"legacy_id": legacyID}})

			assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
			var migration *githubMigrationRequiredError
			assert.False(t, errors.As(err, &migration))
			requireGitHubMigrationState(t, db, user.Id, legacyID, 0)
		})
	}
}

func TestGitHubDeletedLegacyUsernameDoesNotBlockNewRegistration(t *testing.T) {
	db := setupGitHubMigrationTestDB(t)
	deleted := createGitHubMigrationUser(t, db, "recycled-login", "recycled-login", "")
	require.NoError(t, db.Delete(deleted).Error)
	c, _ := newGitHubMigrationContext(http.MethodGet, "/api/oauth/github", "")

	created, err := findOrCreateOAuthUser(c, &oauth.GitHubProvider{}, &oauth.OAuthUser{ProviderUserID: "42", Username: "recycled-login", Extra: map[string]any{"legacy_id": "recycled-login"}}, "", true)

	require.NoError(t, err)
	assert.NotEqual(t, deleted.Id, created.Id)
	assert.Equal(t, "42", created.GitHubId)
	requireGitHubMigrationClaimCount(t, db, created.Id, 1)
	var deletedAfter model.User
	require.NoError(t, db.Unscoped().First(&deletedAfter, deleted.Id).Error)
	assert.True(t, deletedAfter.DeletedAt.Valid)
	assert.Equal(t, "recycled-login", deletedAfter.GitHubId)
}

func TestGitHubRegistrationControlsPreserveExistingLoginsAndMigration(t *testing.T) {
	for _, control := range []string{"registration disabled", "OAuth registration disabled", "GitHub registration disabled"} {
		t.Run(control, func(t *testing.T) {
			db := setupGitHubMigrationTestDB(t)
			numeric := createGitHubMigrationUser(t, db, "numeric-owner", "42", "")
			legacy := createGitHubMigrationUser(t, db, "legacy-owner", "legacy-login", "owner@example.com")
			switch control {
			case "registration disabled":
				common.RegisterEnabled = false
			case "OAuth registration disabled":
				common.OAuthRegisterEnabled = false
			case "GitHub registration disabled":
				common.OptionMapRWMutex.Lock()
				common.OptionMap[common.RegistrationDisabledMethodsOptionKey] = "github"
				common.OptionMapRWMutex.Unlock()
			}
			provider := &oauth.GitHubProvider{}

			found, err := findOrCreateOAuthUser(nil, provider, &oauth.OAuthUser{ProviderUserID: "42"}, "", false)
			require.NoError(t, err)
			assert.Equal(t, numeric.Id, found.Id)
			_, err = findOrCreateOAuthUser(nil, provider, &oauth.OAuthUser{ProviderUserID: "43", VerifiedEmails: []string{legacy.Email}, Extra: map[string]any{"legacy_id": "legacy-login"}}, "", false)
			var migration *githubMigrationRequiredError
			require.ErrorAs(t, err, &migration)
			assert.Equal(t, legacy.Id, migration.User.Id)
			result, response := beginGitHubMigrationForTest(t, migration)
			require.True(t, result.Success, response.Body.String())
			assert.NotEmpty(t, result.Data.AccessToken)
			requireGitHubMigrationState(t, db, legacy.Id, "43", 1)
			_, err = findOrCreateOAuthUser(nil, provider, &oauth.OAuthUser{ProviderUserID: "44"}, "", true)
			var disabled *OAuthRegistrationDisabledError
			assert.ErrorAs(t, err, &disabled)
			var count int64
			require.NoError(t, db.Model(&model.User{}).Count(&count).Error)
			assert.EqualValues(t, 2, count)
		})
	}
}

type githubMigrationTransportFunc func(*http.Request) (*http.Response, error)

func (f githubMigrationTransportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestGitHubAuthenticatedBindIgnoresUnrelatedLegacyUsernameCollision(t *testing.T) {
	db := setupGitHubMigrationTestDB(t)
	target := createGitHubMigrationUser(t, db, "binding-owner", "", "")
	legacy := createGitHubMigrationUser(t, db, "legacy-owner", "recycled-login", "legacy@example.com")
	bundle, err := service.CreateLoginSession(target.Id, "password", "127.0.0.1", "github-bind-test")
	require.NoError(t, err)
	flowToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: model.AuthFlowPurposeOAuth, Provider: "github",
		Intent: model.AuthFlowIntentBind, UserId: target.Id, SessionId: bundle.Session.SID, Payload: `{}`, ExpiresAt: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	previousTransport := http.DefaultTransport
	http.DefaultTransport = githubMigrationTransportFunc(func(request *http.Request) (*http.Response, error) {
		body := ""
		switch request.URL.Host + request.URL.Path {
		case "github.com/login/oauth/access_token":
			body = `{"access_token":"test-token","token_type":"bearer"}`
		case "api.github.com/user":
			body = `{"id":42,"login":"recycled-login"}`
		case "api.github.com/user/emails":
			body = `[]`
		default:
			t.Fatalf("unexpected GitHub request: %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	router := gin.New()
	router.GET("/api/oauth/:provider", HandleOAuth)
	request := httptest.NewRequest(http.MethodGet, "/api/oauth/github?state="+flowToken+"&code=test", nil)
	request.AddCookie(&http.Cookie{Name: oauthStateCookieName("github"), Value: flowToken})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	result := decodeGitHubMigrationResponse(t, response)
	require.True(t, result.Success, response.Body.String())
	assert.Equal(t, "bind", result.Data.Action)
	requireGitHubMigrationNoTokens(t, response)
	requireGitHubMigrationState(t, db, target.Id, "42", 1)
	requireGitHubMigrationState(t, db, legacy.Id, "recycled-login", 0)
	requireGitHubMigrationClaimCount(t, db, target.Id, 1)
	_, err = model.GetAuthFlow(flowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth})
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
}
