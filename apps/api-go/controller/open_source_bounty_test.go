package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenSourceBountyTipHTTPIdempotencyReplay(t *testing.T) {
	db, owner, _ := setupOpenSourceBountyMCPControllerTest(t)
	participant := model.User{Username: "http-tip-contributor", Password: "password", AffCode: "http-tip-contributor", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&participant).Error)
	project, err := model.CreateOpenSourceBountyDraft(owner.Id, model.OpenSourceBountyDraftInput{
		RepositoryUrl: "https://github.com/example/http-tip", Title: "Fix reproducible defects",
		Description: "Find and fix a reproducible defect with focused verification.",
		Rules:       "Link the Issue and pull request and include appropriate tests.",
		RewardQuota: 1_000, RewardSlots: 1,
	})
	require.NoError(t, err)
	project, _, err = model.PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := model.AcceptOpenSourceBounty(participant.Id, project.Id, "http-tip-contributor")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/open-source-bounties/challenges/:challenge_id/tip", func(c *gin.Context) {
		c.Set("id", owner.Id)
		TipOpenSourceBountyChallenge(c)
	})
	type tipEnvelope struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
		Data    struct {
			Challenge        model.OpenSourceBountyChallenge `json:"challenge"`
			TransferredQuota int                             `json:"transferred_quota"`
			RemainingQuota   int                             `json:"remaining_quota"`
		} `json:"data"`
	}
	send := func(key string, body string) tipEnvelope {
		request := httptest.NewRequest(http.MethodPost, "/api/open-source-bounties/challenges/"+strconv.Itoa(challenge.Id)+"/tip", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		assert.Equal(t, http.StatusOK, response.Code)
		var envelope tipEnvelope
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		return envelope
	}

	invalid := send("", `{"quota":250,"note":"HTTP replay"}`)
	assert.False(t, invalid.Success)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_INVALID_IDEMPOTENCY_KEY", invalid.Code)
	key := "01988f13-4432-7b02-8d5e-9c82794fc004" // gitleaks:allow
	first := send(key, `{"quota":250,"note":" HTTP replay "}`)
	require.True(t, first.Success)
	replay := send(key, `{"quota":250,"note":"HTTP replay"}`)
	require.True(t, replay.Success)
	assert.Equal(t, first.Data, replay.Data)
	mismatch := send(key, `{"quota":251,"note":"HTTP replay"}`)
	assert.False(t, mismatch.Success)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_IDEMPOTENCY_MISMATCH", mismatch.Code)

	var ledgers int64
	require.NoError(t, db.Model(&model.OpenSourceBountyLedger{}).Where("challenge_id = ? AND kind = ?", challenge.Id, model.OpenSourceBountyLedgerTipTransfer).Count(&ledgers).Error)
	assert.Equal(t, int64(1), ledgers)
}

func TestCancelOpenSourceBountyChallengeHTTP(t *testing.T) {
	db, owner, _ := setupOpenSourceBountyMCPControllerTest(t)
	participant := model.User{Username: "http-cancel-contributor", Password: "password", AffCode: "http-cancel-contributor", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&participant).Error)
	project, err := model.CreateOpenSourceBountyDraft(owner.Id, model.OpenSourceBountyDraftInput{
		RepositoryUrl: "https://github.com/example/http-cancel", Title: "Cancel abandoned challenge",
		Description: "Release a reserved reward slot when no work was submitted.",
		Rules:       "Accepted challenges may be cancelled only before submission.",
		RewardQuota: 1_000, RewardSlots: 1,
	})
	require.NoError(t, err)
	project, _, err = model.PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := model.AcceptOpenSourceBounty(participant.Id, project.Id, "http-cancel-contributor")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/open-source-bounties/challenges/:challenge_id/cancel", func(c *gin.Context) {
		c.Set("id", owner.Id)
		CancelOpenSourceBountyChallenge(c)
	})
	request := httptest.NewRequest(http.MethodPost, "/api/open-source-bounties/challenges/"+strconv.Itoa(challenge.Id)+"/cancel", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusOK, response.Code)
	var envelope struct {
		Success bool                            `json:"success"`
		Data    model.OpenSourceBountyChallenge `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	assert.Equal(t, model.OpenSourceBountyChallengeCancelled, envelope.Data.Status)
}
