package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/account"
	"github.com/gin-gonic/gin"
)

func nativeAccountFailure(c *gin.Context, err error) {
	status, code, message := http.StatusInternalServerError, "ACCOUNT_STORAGE_ERROR", "Unable to complete account operation"
	switch {
	case errors.Is(err, model.ErrNativeAccountInput):
		status, code, message = http.StatusBadRequest, "ACCOUNT_INVALID_REQUEST", "Invalid account request"
	case errors.Is(err, model.ErrNativeAccountExists):
		status, code, message = http.StatusConflict, "TEAM_ALREADY_OWNED", "You already own a team"
	case errors.Is(err, model.ErrNativeInvitationUnavailable):
		status, code, message = http.StatusConflict, "TEAM_INVITATION_UNAVAILABLE", "Invitation is no longer available"
	case errors.Is(err, model.ErrNativeAccountDenied), errors.Is(err, account.ErrAccessDenied):
		status, code, message = http.StatusForbidden, "ACCOUNT_ACCESS_DENIED", "Account access denied"
	default:
		common.SysError("native account database operation failed")
	}
	c.JSON(status, gin.H{"success": false, "code": code, "message": message})
}

func nativeAccountRequest(c *gin.Context) (*model.NativeAccountStore, model.NativeAccountActor, bool) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "code": "ACCOUNT_SESSION_REQUIRED", "message": "Sign in with a browser session"})
		return nil, model.NativeAccountActor{}, false
	}
	store, err := model.NewNativeAccountStore(model.DB)
	if err != nil {
		nativeAccountFailure(c, err)
		return nil, model.NativeAccountActor{}, false
	}
	return store, model.NativeAccountActor{UserID: identity.UserID, AuthVersion: identity.UserAuthVersion, SessionID: identity.SessionID, SessionVersion: identity.SessionVersion}, true
}

// Reject unknown authority fields and trailing JSON instead of silently
// ignoring client-supplied owner/role/balance claims. The router bounds bytes.
func nativeAccountBody(c *gin.Context, target interface{}) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		nativeAccountFailure(c, model.ErrNativeAccountInput)
		return false
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		nativeAccountFailure(c, model.ErrNativeAccountInput)
		return false
	}
	return true
}

func nativePositiveParam(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 || id > (1<<53)-1 || (name == "user_id" && int64(int(id)) != id) {
		nativeAccountFailure(c, model.ErrNativeAccountInput)
		return 0, false
	}
	return id, true
}

func nativeCursor(c *gin.Context) (int64, bool) {
	text := c.Query("after")
	if text == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil || id < 0 || id > (1<<53)-1 || int64(int(id)) != id {
		nativeAccountFailure(c, model.ErrNativeAccountInput)
		return 0, false
	}
	return id, true
}

func nativeAccountReply(c *gin.Context, data interface{}, err error) {
	if err != nil {
		nativeAccountFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func ListNativeAccounts(c *gin.Context) {
	s, actor, ok := nativeAccountRequest(c)
	if !ok {
		return
	}
	after, ok := nativeCursor(c)
	if !ok {
		return
	}
	data, err := s.ListAccounts(c.Request.Context(), actor, after)
	nativeAccountReply(c, data, err)
}

func CreateNativeTeam(c *gin.Context) {
	s, actor, ok := nativeAccountRequest(c)
	if !ok {
		return
	}
	var input struct {
		DisplayName string `json:"display_name"`
		RequestKey  string `json:"request_key"`
	}
	if !nativeAccountBody(c, &input) {
		return
	}
	data, err := s.CreateTeam(c.Request.Context(), actor, input.DisplayName, input.RequestKey)
	nativeAccountReply(c, data, err)
}

func GetNativeTeam(c *gin.Context) {
	s, actor, ok := nativeAccountRequest(c)
	if !ok {
		return
	}
	id, ok := nativePositiveParam(c, "team_id")
	if !ok {
		return
	}
	data, err := s.GetTeam(c.Request.Context(), actor, id)
	nativeAccountReply(c, data, err)
}

func ListNativeTeamMembers(c *gin.Context) {
	s, actor, ok := nativeAccountRequest(c)
	if !ok {
		return
	}
	id, ok := nativePositiveParam(c, "team_id")
	if !ok {
		return
	}
	after, ok := nativeCursor(c)
	if !ok {
		return
	}
	data, err := s.ListMembers(c.Request.Context(), actor, id, int(after))
	nativeAccountReply(c, data, err)
}

func InviteNativeTeamMember(c *gin.Context) {
	s, actor, ok := nativeAccountRequest(c)
	if !ok {
		return
	}
	id, ok := nativePositiveParam(c, "team_id")
	if !ok {
		return
	}
	var input struct {
		UserID     int              `json:"user_id"`
		Role       account.TeamRole `json:"role"`
		RequestKey string           `json:"request_key"`
	}
	if !nativeAccountBody(c, &input) {
		return
	}
	data, err := s.Invite(c.Request.Context(), actor, id, input.UserID, input.Role, input.RequestKey)
	nativeAccountReply(c, data, err)
}

func ListNativeTeamInvitations(c *gin.Context) {
	s, actor, ok := nativeAccountRequest(c)
	if !ok {
		return
	}
	data, err := s.ListInvitations(c.Request.Context(), actor, c.Query("after"))
	nativeAccountReply(c, data, err)
}

func AcceptNativeTeamInvitation(c *gin.Context) {
	s, actor, ok := nativeAccountRequest(c)
	if !ok {
		return
	}
	data, err := s.AcceptInvitation(c.Request.Context(), actor, c.Param("invitation_id"))
	nativeAccountReply(c, data, err)
}

func UpdateNativeTeamMember(c *gin.Context) {
	s, actor, ok := nativeAccountRequest(c)
	if !ok {
		return
	}
	id, ok := nativePositiveParam(c, "team_id")
	if !ok {
		return
	}
	userID, ok := nativePositiveParam(c, "user_id")
	if !ok {
		return
	}
	var input struct {
		Role account.TeamRole `json:"role"`
	}
	if !nativeAccountBody(c, &input) {
		return
	}
	err := s.SetMemberRole(c.Request.Context(), actor, id, int(userID), input.Role)
	nativeAccountReply(c, nil, err)
}

func RemoveNativeTeamMember(c *gin.Context) {
	s, actor, ok := nativeAccountRequest(c)
	if !ok {
		return
	}
	id, ok := nativePositiveParam(c, "team_id")
	if !ok {
		return
	}
	userID, ok := nativePositiveParam(c, "user_id")
	if !ok {
		return
	}
	err := s.RemoveMember(c.Request.Context(), actor, id, int(userID))
	nativeAccountReply(c, nil, err)
}

func RevokeNativeTeamInvitation(c *gin.Context) {
	s, actor, ok := nativeAccountRequest(c)
	if !ok {
		return
	}
	id, ok := nativePositiveParam(c, "team_id")
	if !ok {
		return
	}
	err := s.RevokeInvitation(c.Request.Context(), actor, id, c.Param("invitation_id"))
	nativeAccountReply(c, nil, err)
}

func ListNativeTeamSentInvitations(c *gin.Context) {
	s, actor, ok := nativeAccountRequest(c)
	if !ok {
		return
	}
	id, ok := nativePositiveParam(c, "team_id")
	if !ok {
		return
	}
	data, err := s.ListSentInvitations(c.Request.Context(), actor, id, c.Query("after"))
	nativeAccountReply(c, data, err)
}

func DeclineNativeTeamInvitation(c *gin.Context) {
	s, actor, ok := nativeAccountRequest(c)
	if !ok {
		return
	}
	err := s.DeclineInvitation(c.Request.Context(), actor, c.Param("invitation_id"))
	nativeAccountReply(c, nil, err)
}
