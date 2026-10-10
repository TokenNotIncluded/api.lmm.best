package router

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type rt17AuthObservation struct {
	URI                   string   `json:"uri"`
	Path                  string   `json:"decoded_path"`
	RawPath               string   `json:"raw_path"`
	OriginalAuthorization []string `json:"original_authorization"`
	FinalAuthorization    string   `json:"final_authorization"`
	UserID                int      `json:"user_id"`
	Status                int      `json:"status"`
}

// Actual SetRelayRouter + TokenAuth + local database; no handler replacement.
// The catalog is deliberately read-only. This does not test billable relay,
// channel selection, external providers, the edge proxy, or wallet settlement.
func TestRT17RelayCatalogRawIdentityBoundaries(t *testing.T) {
	oldDB, oldLogDB := model.DB, model.LOG_DB
	t.Cleanup(func() { model.DB, model.LOG_DB = oldDB, oldLogDB })
	setupRelayRouterTestDB(t)
	level := model.TrustLevelMinUser + 1
	users := []model.User{
		{Username: "rt17-user-a", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", Quota: 100000, TrustLevelOverride: &level, ConsoleActivatedAt: 1},
		{Username: "rt17-user-b", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", Quota: 100000, TrustLevelOverride: &level, ConsoleActivatedAt: 1},
	}
	keys := []string{"rt17localtestkeya", "rt17localtestkeyb"}
	tokens := make([]model.Token, 2)
	for i := range users {
		require.NoError(t, model.DB.Create(&users[i]).Error)
		tokens[i] = model.Token{UserId: users[i].Id, Key: keys[i], Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, RemainQuota: 100000}
		require.NoError(t, model.DB.Create(&tokens[i]).Error)
	}
	tests := []struct {
		name, path, headers string
		user, code          int
	}{
		{"bearer-control", "/v1/models", "Authorization: Bearer " + keys[0] + "\r\n", 0, 200},
		{"mixed-scheme-case-control", "/v1/models", "Authorization: bEaReR " + keys[0] + "\r\n", 0, 200},
		{"duplicate-authorization", "/v1/models", "Authorization: Bearer " + keys[0] + "\r\naUtHoRiZaTiOn: Bearer " + keys[1] + "\r\n", 0, 200},
		{"conflicting-provider-header", "/v1/models", "Authorization: Bearer " + keys[0] + "\r\nx-goog-api-key: " + keys[1] + "\r\n", 1, 200},
		{"duplicate-key-query", "/v1/models?key=" + keys[0] + "&key=" + keys[1], "", 0, 200},
		{"encoded-path-control", "/v1/%6dodels", "Authorization: Bearer " + keys[0] + "\r\n", 0, 200},
		{"trace-is-not-identity", "/v1/models", fmt.Sprintf("Authorization: Bearer %s\r\nX-Request-ID: RT17_TRACE\r\nX-Forwarded-For: 127.0.0.9\r\nX-Forwarded-User: %d\r\nNew-Api-User: %d\r\n", keys[0], users[1].Id, users[1].Id), 0, 200},
		{"headers-without-key", "/v1/models", fmt.Sprintf("X-Forwarded-User: %d\r\nNew-Api-User: %d\r\nX-Request-ID: RT17_TRACE\r\n", users[0].Id, users[0].Id), -1, 404},
		{"ordinary-key-channel-override", "/v1/models", "Authorization: Bearer " + keys[0] + "-1\r\n", -1, 403},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			observations := []rt17AuthObservation{}
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				before := append([]string(nil), c.Request.Header.Values("Authorization")...)
				c.Next()
				record := rt17AuthObservation{URI: c.Request.RequestURI, Path: c.Request.URL.Path, RawPath: c.Request.URL.RawPath, OriginalAuthorization: before, FinalAuthorization: c.GetHeader("Authorization"), UserID: c.GetInt("id"), Status: c.Writer.Status()}
				mu.Lock()
				observations = append(observations, record)
				mu.Unlock()
			})
			SetRelayRouter(engine)
			server := httptest.NewServer(engine)
			defer server.Close()
			addr := server.Listener.Addr().(*net.TCPAddr)
			require.True(t, addr.IP.IsLoopback(), "raw request target must be loopback")
			conn, err := net.DialTimeout("tcp", addr.String(), time.Second)
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
			next := 1
			if tc.user == 1 {
				next = 0
			}
			separator := "?"
			if strings.Contains(tc.path, "?") {
				separator = "&"
			}
			first := fmt.Sprintf("GET %s%smarker=RT17_A HTTP/1.1\r\nHost: rt17.invalid\r\n%s\r\n", tc.path, separator, tc.headers)
			second := fmt.Sprintf("GET /v1/models?marker=RT17_B HTTP/1.1\r\nHost: rt17.invalid\r\nAuthorization: Bearer %s\r\nConnection: close\r\n\r\n", keys[next])
			_, err = io.WriteString(conn, first+second)
			require.NoError(t, err)
			reader := bufio.NewReader(conn)
			codes := []int{}
			for i := 0; i < 2; i++ {
				response, err := http.ReadResponse(reader, nil)
				require.NoError(t, err, "second benign request must remain a separate request")
				_, readErr := io.Copy(io.Discard, response.Body)
				response.Body.Close()
				require.NoError(t, readErr)
				codes = append(codes, response.StatusCode)
			}
			conn.Close()
			server.Close()
			mu.Lock()
			captured := append([]rt17AuthObservation(nil), observations...)
			mu.Unlock()
			evidence, err := json.Marshal(struct {
				Raw    string                `json:"raw"`
				Codes  []int                 `json:"codes"`
				Stages []rt17AuthObservation `json:"stages"`
			}{first + second, codes, captured})
			require.NoError(t, err)
			t.Log(string(evidence))
			require.Len(t, captured, 2, "each HTTP message must have its own Gin context")
			// Rejecting a conflicting credential bundle is also an acceptable policy.
			ambiguity := tc.name == "duplicate-authorization" || tc.name == "conflicting-provider-header" || tc.name == "duplicate-key-query"
			if ambiguity && codes[0] >= 400 && codes[0] < 500 {
				t.Log("ambiguous credentials were rejected")
			} else {
				require.Equal(t, tc.code, codes[0])
				if tc.user >= 0 {
					require.Equal(t, users[tc.user].Id, captured[0].UserID)
					// Provider-specific credentials may normalize Authorization. It must then
					// name the same stored key as the authenticated context, not another user.
					if tc.name != "mixed-scheme-case-control" {
						require.Equal(t, "Bearer "+keys[tc.user], captured[0].FinalAuthorization)
					}
				}
			}
			require.Equal(t, http.StatusOK, codes[1])
			require.Equal(t, users[next].Id, captured[1].UserID)
			require.Equal(t, "Bearer "+keys[next], captured[1].FinalAuthorization)
			require.Contains(t, captured[1].URI, "marker=RT17_B")
		})
	}
	// Direct persistence checks, not just HTTP 200 or cached context values.
	for i := range users {
		var after model.User
		var tokenAfter model.Token
		require.NoError(t, model.DB.First(&after, users[i].Id).Error)
		require.NoError(t, model.DB.First(&tokenAfter, tokens[i].Id).Error)
		require.Equal(t, users[i].Quota, after.Quota, "catalog reads changed user quota")
		require.Equal(t, tokens[i].RemainQuota, tokenAfter.RemainQuota, "catalog reads changed token quota")
		require.Equal(t, common.RoleCommonUser, after.Role, "request headers changed a stored role")
	}
}
