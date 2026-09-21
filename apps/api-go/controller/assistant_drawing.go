/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"

	"github.com/gin-gonic/gin"
)

const (
	assistantDrawingPromptMaxRunes = 2000
	assistantDrawingMaxImages      = 4
	assistantDrawingMaxReferences  = 8
	assistantDrawingMaxImageBytes  = int64(10 << 20)
)

var assistantImageRequestPattern = regexp.MustCompile(`(?i)(?:绘图|画图|生成(?:一张|图片|图像)|帮我画|generate(?: an)? image|create(?: an)? image|draw(?: an)? image)`)

type assistantDrawingDraft struct {
	Prompt  string `json:"prompt"`
	Model   string `json:"model"`
	Group   string `json:"group"`
	Size    string `json:"size,omitempty"`
	Quality string `json:"quality,omitempty"`
	N       uint   `json:"n"`
}

type assistantDrawingGenerateInput struct {
	ConfirmationToken string `json:"confirmation_token"`
}

func assistantDrawingCatalog(userGroup string) (map[string]string, map[string]struct{}) {
	groups := service.GetUserUsableGroups(userGroup)
	usableGroups := make(map[string]string, len(groups))
	for group, description := range groups {
		usableGroups[group] = description
	}

	imageModels := make(map[string]struct{})
	for _, pricing := range model.GetPricing() {
		for _, endpoint := range pricing.SupportedEndpointTypes {
			if endpoint == types.EndpointTypeImageGeneration {
				imageModels[pricing.ModelName] = struct{}{}
				break
			}
		}
	}
	return usableGroups, imageModels
}

func assistantDrawingModelsForGroup(group string, imageModels map[string]struct{}) []string {
	if group == "" {
		return nil
	}
	models := service.GetGroupsEnabledModels([]string{group})
	result := make([]string, 0, len(models))
	for _, name := range models {
		if _, ok := imageModels[name]; ok {
			result = append(result, name)
		}
	}
	slices.Sort(result)
	return result
}

func assistantDrawingModelAllowed(userGroup, group, modelID string) bool {
	groups, imageModels := assistantDrawingCatalog(userGroup)
	if _, ok := groups[group]; !ok {
		return false
	}
	return slices.Contains(assistantDrawingModelsForGroup(group, imageModels), modelID)
}

func assistantExplicitImageRequest(message string) bool {
	return assistantImageRequestPattern.MatchString(strings.TrimSpace(message))
}

func assistantImageGenerationWorkflowRequired(userContext assistantUserContext) bool {
	return common.DrawingEnabled && userContext.DeveloperAccessGranted && assistantExplicitImageRequest(userContext.LatestUserRequest)
}

func assistantImageGenerationWorkflowMinSteps(userContext assistantUserContext) int {
	if !assistantImageGenerationWorkflowRequired(userContext) {
		return 0
	}
	return 2 // prepare the confirmation card, then produce a final answer
}

func executeAssistantImageGenerationTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	if !common.DrawingEnabled {
		return map[string]any{"ok": false, "status": "drawing_disabled", "error": "image generation is currently disabled"}
	}
	if c == nil || c.GetBool("use_access_token") || strings.TrimSpace(c.GetString("session_id")) == "" {
		return map[string]any{"ok": false, "status": "browser_session_required", "error": "image generation confirmation requires a browser session"}
	}
	user, err := model.GetUserCache(userID)
	if err != nil {
		return map[string]any{"ok": false, "error": "account access could not be loaded"}
	}
	access, err := model.GetDeveloperAccessStateForUserBase(user)
	if err != nil || !access.Granted {
		return map[string]any{"ok": false, "status": "l1_required", "error": "L1 access is required for image generation"}
	}
	groups, imageModels := assistantDrawingCatalog(user.Group)
	group := strings.TrimSpace(inputString(input, "group"))
	if group == "" {
		// Keep the requested image-2 preference data-driven: it is only a
		// default when the live group catalog actually exposes that group.
		if _, ok := groups["image-2"]; ok {
			group = "image-2"
		} else {
			groupNames := make([]string, 0, len(groups))
			for name := range groups {
				groupNames = append(groupNames, name)
			}
			slices.Sort(groupNames)
			return map[string]any{
				"ok": true, "status": "selection_required", "groups": groupNames,
				"next_step": "Ask the user to choose one exact routing group before preparing the image.",
			}
		}
	}
	if _, ok := groups[group]; !ok {
		return map[string]any{"ok": false, "status": "invalid_group", "error": "the requested image group is not available to this account"}
	}
	models := assistantDrawingModelsForGroup(group, imageModels)
	if len(models) == 0 {
		return map[string]any{"ok": false, "status": "no_image_models", "error": "the selected group has no image-capable models"}
	}
	modelID := strings.TrimSpace(inputString(input, "model"))
	if modelID == "" {
		if slices.Contains(models, "image-2") {
			modelID = "image-2"
		} else {
			return map[string]any{
				"ok": true, "status": "selection_required", "group": group, "model_ids": models,
				"next_step": "Ask the user to choose one exact image model from this group.",
			}
		}
	}
	if !slices.Contains(models, modelID) {
		return map[string]any{"ok": false, "status": "model_unavailable", "error": "the exact image model is not available in the selected group"}
	}
	prompt := strings.TrimSpace(inputString(input, "prompt"))
	if prompt == "" || len([]rune(prompt)) > assistantDrawingPromptMaxRunes {
		return map[string]any{"ok": false, "status": "prompt_invalid", "error": "image prompt must contain 1 to 2000 characters"}
	}
	n := uint(1)
	if value, ok := inputNumber(input, "n"); ok {
		if value < 1 || value > assistantDrawingMaxImages || value != float64(uint(value)) {
			return map[string]any{"ok": false, "status": "image_count_invalid", "error": "image count must be between 1 and 4"}
		}
		n = uint(value)
	}
	draft := assistantDrawingDraft{
		Prompt:  prompt,
		Model:   modelID,
		Group:   group,
		Size:    strings.TrimSpace(inputString(input, "size")),
		Quality: strings.TrimSpace(inputString(input, "quality")),
		N:       n,
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		return map[string]any{"ok": false, "error": "image request could not be prepared"}
	}
	token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose:   model.AuthFlowPurposeAssistantDrawing,
		UserId:    userID,
		SessionId: strings.TrimSpace(c.GetString("session_id")),
		Payload:   string(payload),
		ExpiresAt: time.Now().Add(10 * time.Minute),
	})
	if err != nil {
		return map[string]any{"ok": false, "error": "image request confirmation could not be created"}
	}
	action := map[string]any{
		"type": "image_generation", "requires_confirmation": true,
		"confirmation_token": token, "expires_in_seconds": 600,
		"prompt": prompt, "model": modelID, "group": group, "n": n,
	}
	if draft.Size != "" {
		action["size"] = draft.Size
	}
	if draft.Quality != "" {
		action["quality"] = draft.Quality
	}
	c.Set(assistantClientActionKey, action)
	return map[string]any{
		"ok": true, "status": "confirmation_required", "action": "image_generation",
		"message": "Ask the user to confirm the exact image prompt, model, routing group, and image count before generating.",
	}
}

func playgroundImageUserAndGroup(c *gin.Context) (*model.UserBase, string, bool) {
	if !common.DrawingEnabled {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "image generation is disabled"}})
		return nil, "", false
	}
	if c.GetBool("use_access_token") {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "browser authentication is required"}})
		return nil, "", false
	}
	user, err := model.GetUserCache(c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "signed-in account is unavailable"}})
		return nil, "", false
	}
	group := strings.TrimSpace(c.Query("group"))
	if group == "" {
		group = model.DrawingTokenGroup
	}
	if _, ok := service.GetUserUsableGroups(user.Group)[group]; !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "the selected image group is not available to this account"}})
		return nil, "", false
	}
	return user, group, true
}

func preparePlaygroundImageRelay(c *gin.Context, user *model.UserBase, group, modelID, prompt string) bool {
	modelID = strings.TrimSpace(modelID)
	prompt = strings.TrimSpace(prompt)
	if modelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "an exact image model is required"}})
		return false
	}
	// Distribute already enforces group/model availability and the real key's
	// model restrictions. Catalog metadata is a UI hint, not an extra relay gate.
	if !common.GetContextKeyBool(c, constant.ContextKeyDrawingRealToken) ||
		common.GetContextKeyInt(c, constant.ContextKeyTokenId) <= 0 ||
		common.GetContextKeyInt(c, constant.ContextKeyUserId) != user.Id ||
		common.GetContextKeyString(c, constant.ContextKeyTokenGroup) != group {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "a validated drawing API key is required"}})
		return false
	}
	if prompt == "" || len([]rune(prompt)) > assistantDrawingPromptMaxRunes {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "image prompt must contain 1 to 2000 characters"}})
		return false
	}
	return true
}

// PlaygroundImage reuses the normal relay billing, routing and safety path,
// but binds it to the authenticated browser user's selected group.
func PlaygroundImage(c *gin.Context) {
	user, group, ok := playgroundImageUserAndGroup(c)
	if !ok {
		return
	}
	if c.Request.Body == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "an image request body is required"}})
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "image request could not be read"}})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	var request struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "an exact image model is required"}})
		return
	}
	if !preparePlaygroundImageRelay(c, user, group, request.Model, request.Prompt) {
		return
	}
	Relay(c, types.RelayFormatOpenAIImage)
}

func validatePlaygroundReferenceImage(header *multipart.FileHeader) error {
	if header == nil || header.Size <= 0 {
		return errors.New("reference images must not be empty")
	}
	if header.Size > assistantDrawingMaxImageBytes {
		return errors.New("each reference image must be 10 MB or smaller")
	}
	file, err := header.Open()
	if err != nil {
		return errors.New("a reference image could not be read")
	}
	defer file.Close()
	sniff := make([]byte, 512)
	n, err := file.Read(sniff)
	if err != nil && !errors.Is(err, io.EOF) {
		return errors.New("a reference image could not be read")
	}
	contentType := http.DetectContentType(sniff[:n])
	if !slices.Contains([]string{"image/jpeg", "image/png", "image/webp"}, contentType) {
		return errors.New("reference images must be PNG, JPEG, or WebP")
	}
	return nil
}

func parsePlaygroundImageEditForm(c *gin.Context) (*multipart.Form, string, string, error) {
	if c.ContentType() != gin.MIMEMultipartPOSTForm {
		return nil, "", "", errors.New("image editing requires multipart form data")
	}
	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return nil, "", "", errors.New("image edit request could not be read")
	}
	values := url.Values(form.Value)
	c.Request.MultipartForm = form
	c.Request.PostForm = values
	files := append([]*multipart.FileHeader{}, form.File["image"]...)
	files = append(files, form.File["image[]"]...)
	if len(files) == 0 || len(files) > assistantDrawingMaxReferences {
		return form, "", "", errors.New("image editing requires between 1 and 8 reference images")
	}
	for _, header := range files {
		if err := validatePlaygroundReferenceImage(header); err != nil {
			return form, "", "", err
		}
	}
	return form, values.Get("model"), values.Get("prompt"), nil
}

// PlaygroundImageEdit adds validated multi-image input to the browser
// workbench while preserving the normal image relay and billing path.
func PlaygroundImageEdit(c *gin.Context) {
	user, group, ok := playgroundImageUserAndGroup(c)
	if !ok {
		return
	}
	form, modelID, prompt, err := parsePlaygroundImageEditForm(c)
	if form != nil {
		defer form.RemoveAll()
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	if !preparePlaygroundImageRelay(c, user, group, modelID, prompt) {
		return
	}
	Relay(c, types.RelayFormatOpenAIImage)
}

// GenerateAssistantDrawing consumes the session-bound confirmation and then
// enters the same image relay used by the drawing workbench. The server never
// trusts model, group or prompt values re-sent by the browser.
func PrepareAssistantDrawing(c *gin.Context) {
	prepared := false
	defer func() {
		if !prepared {
			c.Abort()
		}
	}()
	if !common.DrawingEnabled {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "image generation is disabled"}})
		return
	}
	if c.GetBool("use_access_token") || strings.TrimSpace(c.GetString("session_id")) == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "browser authentication is required"}})
		return
	}
	var input assistantDrawingGenerateInput
	if err := common.DecodeJson(c.Request.Body, &input); err != nil || strings.TrimSpace(input.ConfirmationToken) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "a drawing confirmation token is required"}})
		return
	}
	// Check the authoritative starting balance before the one-shot confirmation
	// is consumed. A rejected request can be retried after funding the wallet.
	if !requireDrawingWebBalance(c, c.GetInt("id")) {
		return
	}
	flow, err := model.ConsumeAuthFlow(strings.TrimSpace(input.ConfirmationToken), model.AuthFlowMatch{
		Purpose:   model.AuthFlowPurposeAssistantDrawing,
		UserId:    c.GetInt("id"),
		SessionId: strings.TrimSpace(c.GetString("session_id")),
	})
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, model.ErrAuthFlowConsumed) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": gin.H{"message": "image confirmation is invalid or expired; ask the assistant to prepare it again"}})
		return
	}
	var draft assistantDrawingDraft
	if err := json.Unmarshal([]byte(flow.Payload), &draft); err != nil || draft.Prompt == "" || draft.Model == "" || draft.Group == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": gin.H{"message": "image confirmation payload is invalid"}})
		return
	}
	user, err := model.GetUserCache(c.GetInt("id"))
	if err != nil || !assistantDrawingModelAllowed(user.Group, draft.Group, draft.Model) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": gin.H{"message": "the confirmed image model or group is no longer available"}})
		return
	}
	if _, _, ok := prepareDrawingTokenContext(c, c.GetInt("id"), draft.Group); !ok {
		return
	}
	body, err := json.Marshal(draft)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "image request could not be encoded"}})
		return
	}
	originalURL := *c.Request.URL
	query := originalURL.Query()
	query.Set("group", draft.Group)
	c.Request.URL = &url.URL{Path: "/pg/images/generations", RawQuery: query.Encode()}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
	c.Request.Header.Set("Content-Type", "application/json")
	defer func() { c.Request.URL = &originalURL }()
	// This is the existing confirmation-to-relay adapter, not a billing bypass:
	// the trusted real-key flag controls accounting even on the /pg image path.
	prepared = true
	c.Next()
}

func GenerateAssistantDrawing(c *gin.Context) {
	PlaygroundImage(c)
}
