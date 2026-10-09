package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNativeAccountStrictBody(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"display_name":"Team","request_key":"create-team-000001"}`, true},
		{`{"display_name":"Team","owner_user_id":2}`, false},
		{`{"display_name":"Team","quota":999999}`, false},
		{`{"display_name":"Team"} {}`, false},
		{`[]`, false},
		{`{`, false},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/accounts/teams", strings.NewReader(tc.body))
		var input struct {
			DisplayName string `json:"display_name"`
			RequestKey  string `json:"request_key"`
		}
		require.Equal(t, tc.want, nativeAccountBody(c, &input), tc.body)
		if !tc.want {
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		}
	}
}

func TestNativeAccountSessionRequired(t *testing.T) {
	for _, handler := range []gin.HandlerFunc{ListNativeAccounts, CreateNativeTeam, GetNativeTeam, ListNativeTeamMembers, InviteNativeTeamMember, ListNativeTeamInvitations, AcceptNativeTeamInvitation, UpdateNativeTeamMember, RemoveNativeTeamMember, RevokeNativeTeamInvitation, ListNativeTeamSentInvitations, DeclineNativeTeamInvitation} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/accounts/teams", strings.NewReader(`{}`))
		// A user ID / system root role alone is not a browser session.
		c.Set("id", 1)
		c.Set("role", 100)
		handler(c)
		require.Equal(t, http.StatusUnauthorized, recorder.Code)
		require.Contains(t, recorder.Body.String(), "ACCOUNT_SESSION_REQUIRED")
	}
}

func TestNativeAccountIDBounds(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"1", true}, {"4503599627370496", true}, {"9007199254740991", true},
		{"0", false}, {"-1", false}, {"9007199254740992", false}, {"one", false},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Params = gin.Params{{Key: "team_id", Value: tc.value}}
		_, ok := nativePositiveParam(c, "team_id")
		require.Equal(t, tc.valid, ok, tc.value)
	}
}
