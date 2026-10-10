package router

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestToolMarketReviewHTTPAdministratorSelfApprovalStillValidatesRemote(t *testing.T) {
	for name, role := range map[string]int{"admin": common.RoleAdminUser, "root": common.RoleRootUser} {
		t.Run(name, func(t *testing.T) {
			router, db, token, owner := toolMarketTestRouter(t)
			previousLogDB := model.LOG_DB
			model.LOG_DB = db
			t.Cleanup(func() { model.LOG_DB = previousLogDB })
			require.NoError(t, db.AutoMigrate(&model.ToolMarketCredential{}, &model.Log{}))
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", owner.Id).Update("role", role).Error)
			// An administrator publisher reaches remote validation, which must
			// still reject this private destination before publication.
			product, err := model.SaveToolMarketDraft(owner.Id, "", model.ToolMarketDraftInput{Name: "Administrator publisher", ExecutionType: "remote", Visibility: "public", Endpoint: "https://127.0.0.1/mcp", Tools: []model.ToolMarketToolInput{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`), PriceQuota: 100}}})
			require.NoError(t, err)
			require.NoError(t, model.SubmitToolMarketDraft(owner.Id, product.ID, product.DraftVersionID))
			path := "/api/tool-market/services/" + product.ID + "/review"
			body := `{"version_id":"` + product.DraftVersionID + `","approve":true,"note":"Approve my own service"}`
			response := toolMarketHTTPRequest(router, http.MethodPost, path, token, body)
			require.NotEqual(t, http.StatusOK, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "TOOL_MARKET_REMOTE")
			require.NotContains(t, response.Body.String(), "TOOL_MARKET_DENIED")
			var version model.ToolMarketVersion
			require.NoError(t, db.First(&version, "id = ?", product.DraftVersionID).Error)
			require.Equal(t, "pending", version.Status)
			// Rejection stays available to an administrator author and never
			// invokes remote validation or makes this service available to buyers.
			body = `{"version_id":"` + product.DraftVersionID + `","approve":false,"note":"Withdraw my submission"}`
			response = toolMarketHTTPRequest(router, http.MethodPost, path, token, body)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.NoError(t, db.First(&version, "id = ?", product.DraftVersionID).Error)
			require.Equal(t, "rejected", version.Status)
			require.NoError(t, db.First(product, "id = ?", product.ID).Error)
			require.Empty(t, product.LiveVersionID)
		})
	}
}

func TestToolMarketReviewHTTPOrdinaryPublisherCannotReview(t *testing.T) {
	router, db, token, owner := toolMarketTestRouter(t)
	product, err := model.SaveToolMarketDraft(owner.Id, "", model.ToolMarketDraftInput{Name: "Ordinary publisher", ExecutionType: "remote", Visibility: "public", Endpoint: "https://127.0.0.1/mcp", Tools: []model.ToolMarketToolInput{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`), PriceQuota: 100}}})
	require.NoError(t, err)
	require.NoError(t, model.SubmitToolMarketDraft(owner.Id, product.ID, product.DraftVersionID))
	for _, approve := range []bool{true, false} {
		body, err := json.Marshal(map[string]any{"version_id": product.DraftVersionID, "approve": approve, "note": "Review my service"})
		require.NoError(t, err)
		response := toolMarketHTTPRequest(router, http.MethodPost, "/api/tool-market/services/"+product.ID+"/review", token, string(body))
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), "TOOL_MARKET_REMOTE")
	}
	var version model.ToolMarketVersion
	require.NoError(t, db.First(&version, "id = ?", product.DraftVersionID).Error)
	require.Equal(t, "pending", version.Status)
}
