package controller

import (
	"encoding/json"
	"io"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func decodeToolMarketCredentialInput(c *gin.Context, value any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return model.ErrToolMarketInput
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return model.ErrToolMarketInput
	}
	return nil
}

// Authentication is independent from the publicly visible draft definition.
// Secrets never enter its digest, version JSON or persisted request arguments.
func ConfigureToolMarketCredential(c *gin.Context) {
	var input struct {
		VersionID         string `json:"version_id"`
		Mode              string `json:"mode"`
		Secret            string `json:"secret"`
		CopyFromVersionID string `json:"copy_from_version_id"`
	}
	if err := decodeToolMarketCredentialInput(c, &input); err != nil {
		toolMarketRespond(c, nil, err)
		return
	}
	actor, serviceID := c.GetInt("id"), c.Param("id")
	if err := model.ConfigureToolMarketCredential(actor, serviceID, input.VersionID, input.Mode, input.Secret, input.CopyFromVersionID); err != nil {
		toolMarketRespond(c, nil, err)
		return
	}
	metadata, err := model.GetToolMarketCredentialMetadata(actor, serviceID, input.VersionID)
	toolMarketRespond(c, metadata, err)
}

func GetToolMarketCredential(c *gin.Context) {
	versionID := c.Query("version_id")
	if versionID == "" {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	metadata, err := model.GetToolMarketCredentialMetadata(c.GetInt("id"), c.Param("id"), versionID)
	toolMarketRespond(c, metadata, err)
}

func InspectToolMarketRemoteWithCredentials(c *gin.Context) {
	var input struct {
		Endpoint       string `json:"endpoint"`
		ServiceID      string `json:"service_id"`
		VersionID      string `json:"version_id"`
		Authentication *struct {
			Mode   string `json:"mode"`
			Secret string `json:"secret"`
		} `json:"authentication"`
	}
	if err := decodeToolMarketCredentialInput(c, &input); err != nil {
		toolMarketRespond(c, nil, err)
		return
	}
	if (input.ServiceID == "") != (input.VersionID == "") || (input.Authentication != nil && input.ServiceID != "") {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	var credential *model.ToolMarketResolvedCredential
	if input.Authentication != nil {
		if err := model.ValidateToolMarketCredential(input.Authentication.Mode, input.Authentication.Secret); err != nil {
			toolMarketRespond(c, nil, err)
			return
		}
		credential = &model.ToolMarketResolvedCredential{Mode: input.Authentication.Mode, Secret: input.Authentication.Secret}
	} else if input.ServiceID != "" {
		actor := c.GetInt("id")
		if _, err := model.GetToolMarketCredentialMetadata(actor, input.ServiceID, input.VersionID); err != nil {
			toolMarketRespond(c, nil, err)
			return
		}
		var err error
		credential, err = model.ResolveToolMarketCredential(actor, input.ServiceID, input.VersionID, input.Endpoint)
		if err != nil {
			toolMarketRespond(c, nil, err)
			return
		}
	}
	tools, err := service.InspectToolMarketRemoteAuthenticated(c.Request.Context(), input.Endpoint, credential)
	toolMarketRespond(c, toolMarketDiscoveryRows(tools), err)
}

func ActivateToolMarketPrivate(c *gin.Context) {
	var input struct {
		VersionID string `json:"version_id"`
	}
	if err := decodeToolMarketCredentialInput(c, &input); err != nil {
		toolMarketRespond(c, nil, err)
		return
	}
	actor, serviceID := c.GetInt("id"), c.Param("id")
	// A delivered response may be lost. Retrying the active version checks the
	// model's permission/state invariants without revalidating a published draft.
	if live, err := model.GetToolMarketDetail(actor, serviceID, false); err == nil && live.Version.ID == input.VersionID {
		toolMarketRespond(c, nil, model.ActivateToolMarketPrivate(actor, serviceID, input.VersionID))
		return
	}
	draft, err := model.GetToolMarketDetail(actor, serviceID, true)
	if err != nil {
		toolMarketRespond(c, nil, err)
		return
	}
	if draft.Version.ID != input.VersionID || draft.Version.Status != "draft" || draft.Version.Visibility != "private" || draft.Version.ExecutionType != "remote" {
		toolMarketRespond(c, nil, model.ErrToolMarketConflict)
		return
	}
	if draft.Service.Status == "paused" || draft.Service.Status == "suspended" {
		toolMarketRespond(c, nil, model.ErrToolMarketDenied)
		return
	}
	for _, tool := range draft.Tools {
		if tool.PriceQuota != 0 {
			toolMarketRespond(c, nil, model.ErrToolMarketDenied)
			return
		}
	}
	if err := service.ValidateToolMarketRemote(c.Request.Context(), actor, serviceID, false); err != nil {
		toolMarketRespond(c, nil, err)
		return
	}
	toolMarketRespond(c, nil, model.ActivateToolMarketPrivate(actor, serviceID, input.VersionID))
}

// Keep raw schema strings alongside legacy JSON objects for browser authors:
// JavaScript JSON.parse cannot preserve arbitrary numeric schema constraints.
type toolMarketDiscoveryRow struct {
	model.ToolMarketToolInput
	InputSchemaJSON  string `json:"input_schema_json"`
	OutputSchemaJSON string `json:"output_schema_json,omitempty"`
}

func toolMarketDiscoveryRows(tools []model.ToolMarketToolInput) []toolMarketDiscoveryRow {
	rows := make([]toolMarketDiscoveryRow, 0, len(tools))
	for _, tool := range tools {
		rows = append(rows, toolMarketDiscoveryRow{ToolMarketToolInput: tool, InputSchemaJSON: string(tool.InputSchema), OutputSchemaJSON: string(tool.OutputSchema)})
	}
	return rows
}
