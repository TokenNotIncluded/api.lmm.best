package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

type openSourceBountyAcceptRequest struct {
	GithubHandle string `json:"github_handle"`
}

type openSourceBountySubmitRequest struct {
	IssueUrl       string `json:"issue_url"`
	PullRequestUrl string `json:"pull_request_url"`
	SubmissionNote string `json:"submission_note"`
}

type openSourceBountyReviewRequest struct {
	ReviewNote    string `json:"review_note"`
	RatingScore   int    `json:"rating_score"`
	RatingComment string `json:"rating_comment"`
}

type openSourceBountyTipRequest struct {
	Quota int    `json:"quota"`
	Note  string `json:"note"`
}

type openSourceBountyRatingRequest struct {
	Score   int    `json:"score"`
	Comment string `json:"comment"`
}

type openSourceBountyDisputeRequest struct {
	Reason    string `json:"reason"`
	Statement string `json:"statement"`
}

type openSourceBountyDisputeResolutionRequest struct {
	Action     string `json:"action"`
	Resolution string `json:"resolution"`
}

func openSourceBountyId(c *gin.Context, key string) (int, bool) {
	id, err := strconv.Atoi(c.Param(key))
	if err != nil || id <= 0 {
		openSourceBountyApiError(c, &model.OpenSourceBountyError{
			Code: "OPEN_SOURCE_BOUNTY_INVALID_ID", Message: "invalid open-source bounty identifier",
		})
		return 0, false
	}
	return id, true
}

func openSourceBountyApiError(c *gin.Context, err error) {
	code := model.OpenSourceBountyErrorCode(err)
	message := err.Error()
	if code == "OPEN_SOURCE_BOUNTY_INTERNAL_ERROR" {
		common.SysLog("open-source bounty API error: " + err.Error())
		message = "open-source bounty operation failed"
	}
	c.JSON(http.StatusOK, gin.H{"success": false, "code": code, "message": message})
}

func openSourceBountyRemainingQuota(userId int) int {
	var quota int
	if err := model.DB.Model(&model.User{}).Where("id = ?", userId).Select("quota").Scan(&quota).Error; err != nil {
		return 0
	}
	return quota
}

func ListOpenSourceBounties(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	items, total, err := model.ListOpenSourceBounties(c.GetInt("id"), page, pageSize)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func GetOpenSourceBountyConfig(c *gin.Context) {
	common.ApiSuccess(c, model.GetOpenSourceBountyFeeConfig())
}

func ListOwnedOpenSourceBounties(c *gin.Context) {
	archived, _ := strconv.ParseBool(c.DefaultQuery("archived", "false"))
	items, err := model.ListOwnedOpenSourceBountiesFiltered(c.GetInt("id"), archived)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, items)
}

func ArchiveOpenSourceBounty(c *gin.Context) {
	projectId, ok := openSourceBountyId(c, "id")
	if !ok {
		return
	}
	project, err := model.ArchiveOpenSourceBounty(c.GetInt("id"), projectId)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, project)
}

func UnarchiveOpenSourceBounty(c *gin.Context) {
	projectId, ok := openSourceBountyId(c, "id")
	if !ok {
		return
	}
	project, err := model.UnarchiveOpenSourceBounty(c.GetInt("id"), projectId)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, project)
}

func ListAcceptedOpenSourceBounties(c *gin.Context) {
	items, err := model.ListAcceptedOpenSourceBounties(c.GetInt("id"))
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, items)
}

func ListOpenSourceBountyNotifications(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := model.ListOpenSourceBountyNotifications(c.GetInt("id"), limit)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, items)
}

func MarkOpenSourceBountyNotificationsRead(c *gin.Context) {
	if err := model.MarkOpenSourceBountyNotificationsRead(c.GetInt("id")); err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func ListOpenSourceBountyTipNotifications(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := model.ListOpenSourceBountyTipNotifications(c.GetInt("id"), limit)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, items)
}

func MarkOpenSourceBountyTipNotificationsRead(c *gin.Context) {
	if err := model.MarkOpenSourceBountyTipNotificationsRead(c.GetInt("id")); err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func ThankOpenSourceBountyTip(c *gin.Context) {
	tipId, ok := openSourceBountyId(c, "tip_id")
	if !ok {
		return
	}
	notification, err := model.ThankOpenSourceBountyTip(c.GetInt("id"), tipId)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, notification)
}

func GetOpenSourceBounty(c *gin.Context) {
	projectId, ok := openSourceBountyId(c, "id")
	if !ok {
		return
	}
	detail, err := model.GetOpenSourceBountyDetail(c.GetInt("id"), projectId)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, detail)
}

func CreateOpenSourceBounty(c *gin.Context) {
	var request model.OpenSourceBountyDraftInput
	if err := c.ShouldBindJSON(&request); err != nil {
		openSourceBountyApiError(c, &model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_INVALID_REQUEST", Message: "invalid bounty request"})
		return
	}
	project, err := model.CreateOpenSourceBountyDraft(c.GetInt("id"), request)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, project)
}

func UpdateOpenSourceBounty(c *gin.Context) {
	projectId, ok := openSourceBountyId(c, "id")
	if !ok {
		return
	}
	var request model.OpenSourceBountyDraftInput
	if err := c.ShouldBindJSON(&request); err != nil {
		openSourceBountyApiError(c, &model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_INVALID_REQUEST", Message: "invalid bounty request"})
		return
	}
	var project *model.OpenSourceBountyProject
	var err error
	if request.RewardQuota == 0 && request.RewardSlots == 0 {
		project, err = model.UpdateOpenSourceBountyContent(c.GetInt("id"), projectId, request)
	} else {
		project, err = model.UpdateOpenSourceBountyDraft(c.GetInt("id"), projectId, request)
	}
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, project)
}

func DeleteOpenSourceBounty(c *gin.Context) {
	projectId, ok := openSourceBountyId(c, "id")
	if !ok {
		return
	}
	if err := model.DeleteOpenSourceBountyDraft(c.GetInt("id"), projectId); err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func PublishOpenSourceBounty(c *gin.Context) {
	projectId, ok := openSourceBountyId(c, "id")
	if !ok {
		return
	}
	userId := c.GetInt("id")
	project, chargedQuota, err := model.PublishOpenSourceBounty(userId, projectId)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"project": project, "charged_quota": chargedQuota, "remaining_quota": openSourceBountyRemainingQuota(userId)})
}

func PauseOpenSourceBounty(c *gin.Context) {
	setOpenSourceBountyPaused(c, true)
}

func ResumeOpenSourceBounty(c *gin.Context) {
	setOpenSourceBountyPaused(c, false)
}

func setOpenSourceBountyPaused(c *gin.Context, paused bool) {
	projectId, ok := openSourceBountyId(c, "id")
	if !ok {
		return
	}
	project, err := model.SetOpenSourceBountyPaused(c.GetInt("id"), projectId, paused)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, project)
}

func CloseOpenSourceBounty(c *gin.Context) {
	projectId, ok := openSourceBountyId(c, "id")
	if !ok {
		return
	}
	userId := c.GetInt("id")
	project, refundedQuota, err := model.CloseOpenSourceBounty(userId, projectId)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"project": project, "refunded_quota": refundedQuota, "remaining_quota": openSourceBountyRemainingQuota(userId)})
}

func AcceptOpenSourceBounty(c *gin.Context) {
	projectId, ok := openSourceBountyId(c, "id")
	if !ok {
		return
	}
	var request openSourceBountyAcceptRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		openSourceBountyApiError(c, &model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_INVALID_REQUEST", Message: "invalid bounty request"})
		return
	}
	challenge, err := model.AcceptOpenSourceBounty(c.GetInt("id"), projectId, request.GithubHandle)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, challenge)
}

func SubmitOpenSourceBountyChallenge(c *gin.Context) {
	projectId, ok := openSourceBountyId(c, "id")
	if !ok {
		return
	}
	var request openSourceBountySubmitRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		openSourceBountyApiError(c, &model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_INVALID_REQUEST", Message: "invalid bounty submission"})
		return
	}
	challenge, err := model.SubmitOpenSourceBountyChallenge(
		c.GetInt("id"), projectId, request.IssueUrl, request.PullRequestUrl,
		request.SubmissionNote,
	)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, challenge)
}

func WithdrawOpenSourceBountyChallenge(c *gin.Context) {
	challengeId, ok := openSourceBountyId(c, "challenge_id")
	if !ok {
		return
	}
	challenge, err := model.WithdrawOpenSourceBountyChallenge(c.GetInt("id"), challengeId)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, challenge)
}

func CancelOpenSourceBountyChallenge(c *gin.Context) {
	challengeId, ok := openSourceBountyId(c, "challenge_id")
	if !ok {
		return
	}
	challenge, err := model.CancelOpenSourceBountyChallenge(c.GetInt("id"), challengeId)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, challenge)
}

func ApproveOpenSourceBountyChallenge(c *gin.Context) {
	reviewOpenSourceBountyChallenge(c, true)
}

func RejectOpenSourceBountyChallenge(c *gin.Context) {
	reviewOpenSourceBountyChallenge(c, false)
}

func reviewOpenSourceBountyChallenge(c *gin.Context, approve bool) {
	challengeId, ok := openSourceBountyId(c, "challenge_id")
	if !ok {
		return
	}
	var request openSourceBountyReviewRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		openSourceBountyApiError(c, &model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_INVALID_REQUEST", Message: "invalid bounty review"})
		return
	}
	challenge, transferredQuota, err := model.ReviewOpenSourceBountyChallenge(
		c.GetInt("id"), challengeId, approve, strings.TrimSpace(request.ReviewNote),
		request.RatingScore, request.RatingComment,
	)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"challenge": challenge, "transferred_quota": transferredQuota})
}

func RateOpenSourceBountyOwner(c *gin.Context) {
	challengeId, ok := openSourceBountyId(c, "challenge_id")
	if !ok {
		return
	}
	var request openSourceBountyRatingRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		openSourceBountyApiError(c, &model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_INVALID_REQUEST", Message: "invalid bounty rating"})
		return
	}
	challenge, err := model.RateOpenSourceBountyOwner(c.GetInt("id"), challengeId, request.Score, request.Comment)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, challenge)
}

func TipOpenSourceBountyChallenge(c *gin.Context) {
	challengeId, ok := openSourceBountyId(c, "challenge_id")
	if !ok {
		return
	}
	var request openSourceBountyTipRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		openSourceBountyApiError(c, &model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_INVALID_REQUEST", Message: "invalid bounty tip"})
		return
	}
	userId := c.GetInt("id")
	result, err := model.TipOpenSourceBountyChallengeIdempotent(userId, challengeId, request.Quota, request.Note, c.GetHeader("Idempotency-Key"))
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"challenge": result.Challenge, "transferred_quota": result.TransferredQuota,
		"remaining_quota": result.RemainingQuota,
	})
}

func OpenOpenSourceBountyDispute(c *gin.Context) {
	challengeId, ok := openSourceBountyId(c, "challenge_id")
	if !ok {
		return
	}
	var request openSourceBountyDisputeRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		openSourceBountyApiError(c, &model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_INVALID_REQUEST", Message: "invalid bounty dispute"})
		return
	}
	dispute, err := model.OpenOpenSourceBountyDispute(c.GetInt("id"), challengeId, request.Reason, request.Statement)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, dispute)
}

func ListMyOpenSourceBountyDisputes(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := model.ListOpenSourceBountyDisputesFiltered(c.GetInt("id"), false, c.Query("status"), limit)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, items)
}

func ListAdminOpenSourceBountyDisputes(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := model.ListOpenSourceBountyDisputesFiltered(c.GetInt("id"), true, c.Query("status"), limit)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, items)
}

func ResolveOpenSourceBountyDispute(c *gin.Context) {
	disputeId, ok := openSourceBountyId(c, "dispute_id")
	if !ok {
		return
	}
	var request openSourceBountyDisputeResolutionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		openSourceBountyApiError(c, &model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_INVALID_REQUEST", Message: "invalid bounty dispute resolution"})
		return
	}
	dispute, transferredQuota, err := model.ResolveOpenSourceBountyDispute(c.GetInt("id"), disputeId, request.Action, request.Resolution)
	if err != nil {
		openSourceBountyApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"dispute": dispute, "transferred_quota": transferredQuota})
}
