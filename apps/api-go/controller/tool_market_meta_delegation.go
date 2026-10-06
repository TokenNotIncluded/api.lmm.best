package controller

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func toolMarketMetaOwnerSubjects(c *gin.Context) ([]model.ToolMarketMetaSubject, error) {
	switch c.Param("kind") {
	case "personal":
		subject, err := model.ToolMarketMetaPersonalSubject(c.GetInt("id"), c.Param("id"))
		if err != nil {
			return nil, err
		}
		return []model.ToolMarketMetaSubject{subject}, nil
	case "oauth":
		integration := service.CurrentOAuthIntegration()
		// Market policy and OAuth family ownership must share the authoritative
		// database. Never resolve family ownership on a separate replica/store.
		if integration == nil || integration.DB != model.DB {
			return nil, model.ErrToolMarketDenied
		}
		return model.ToolMarketMetaOAuthSubjects(c.GetInt("id"), c.Param("id"), integration.Issuer, integration.Resource)
	default:
		return nil, model.ErrToolMarketInput
	}
}

func toolMarketMetaDelegationSummary(views []model.ToolMarketMetaDelegation) model.ToolMarketMetaDelegation {
	if len(views) == 0 {
		return model.ToolMarketMetaDelegation{}
	}
	view := views[0]
	for _, candidate := range views[1:] {
		view.Enabled = view.Enabled && candidate.Enabled
		view.MaxTotalQuota = min(view.MaxTotalQuota, candidate.MaxTotalQuota)
		view.ExpiresAt = min(view.ExpiresAt, candidate.ExpiresAt)
		view.UpdatedAt = max(view.UpdatedAt, candidate.UpdatedAt)
	}
	return view
}

func GetToolMarketMetaDelegation(c *gin.Context) {
	subjects, err := toolMarketMetaOwnerSubjects(c)
	if err != nil {
		toolMarketRespond(c, nil, err)
		return
	}
	views := make([]model.ToolMarketMetaDelegation, 0, len(subjects))
	for _, subject := range subjects {
		view, err := model.GetToolMarketMetaDelegation(subject)
		if err != nil {
			toolMarketRespond(c, nil, err)
			return
		}
		views = append(views, *view)
	}
	toolMarketRespond(c, toolMarketMetaDelegationSummary(views), nil)
}

func decodeToolMarketMetaDelegationInput(raw []byte) (model.ToolMarketMetaDelegation, error) {
	var input model.ToolMarketMetaDelegation
	if len(raw) == 0 || len(raw) > 1024 {
		return input, model.ErrToolMarketInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return input, model.ErrToolMarketInput
	}
	fields := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || fields[key] || (key != "enabled" && key != "max_total_quota" && key != "expires_at") {
			return input, model.ErrToolMarketInput
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return input, model.ErrToolMarketInput
		}
		fields[key] = true
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return input, model.ErrToolMarketInput
	}
	if _, err := decoder.Token(); err != io.EOF || !fields["enabled"] || !fields["max_total_quota"] || json.Unmarshal(raw, &input) != nil {
		return input, model.ErrToolMarketInput
	}
	if input.MaxTotalQuota < 0 || common.ValidateWalletQuota(input.MaxTotalQuota) != nil || input.ExpiresAt < 0 || (!input.Enabled && (input.MaxTotalQuota != 0 || input.ExpiresAt != 0)) {
		return input, model.ErrToolMarketInput
	}
	return input, nil
}

func SetToolMarketMetaDelegation(c *gin.Context) {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1025))
	if err != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	input, err := decodeToolMarketMetaDelegationInput(raw)
	if err != nil {
		toolMarketRespond(c, nil, err)
		return
	}
	subjects, err := toolMarketMetaOwnerSubjects(c)
	if err != nil {
		toolMarketRespond(c, nil, err)
		return
	}
	views, err := model.SetToolMarketMetaDelegations(subjects, input)
	toolMarketRespond(c, toolMarketMetaDelegationSummary(views), err)
}

// The regular OAuth connection picker permits invoke-only clients. AI tool
// management has a distinct, stricter picker and never widens existing scopes.
func ListToolMarketMetaOAuthClients(c *gin.Context) {
	offset, limit, ok := toolMarketPage(c)
	if !ok {
		return
	}
	integration := service.CurrentOAuthIntegration()
	if integration == nil {
		toolMarketRespond(c, []model.ToolMarketOAuthClient{}, nil)
		return
	}
	if integration.DB != model.DB {
		toolMarketRespond(c, nil, model.ErrToolMarketDenied)
		return
	}
	rows, err := model.ListToolMarketOAuthClients(integration.DB.WithContext(c.Request.Context()), c.GetInt("id"), integration.Issuer, integration.Resource,
		[]string{service.OAuthPiClientID, service.OAuthDshClientID},
		[]string{service.OAuthMarketDiscoverScope, service.OAuthMarketInvokeScope, service.OAuthMarketManageScope}, offset, limit)
	toolMarketRespond(c, rows, err)
}
