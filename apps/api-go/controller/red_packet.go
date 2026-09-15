package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

const maxRedPacketCoverBytes = 3 << 20

type redPacketMutationRequest struct {
	Title        string                     `json:"title"`
	Description  string                     `json:"description"`
	CoverImage   string                     `json:"cover_image"`
	CoverPrompt  string                     `json:"cover_prompt"`
	DrawMode     string                     `json:"draw_mode"`
	PerUserLimit int                        `json:"per_user_limit"`
	StartAt      int64                      `json:"start_at"`
	EndAt        int64                      `json:"end_at"`
	Enabled      *bool                      `json:"enabled"`
	Items        []model.RedPacketItemInput `json:"items"`
}

func validateRedPacketMutation(input *redPacketMutationRequest, requireItems bool) error {
	if input == nil {
		return errors.New("红包配置为空")
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.CoverPrompt = strings.TrimSpace(input.CoverPrompt)
	if utf8.RuneCountInString(input.Title) < 1 || utf8.RuneCountInString(input.Title) > 80 {
		return errors.New("红包标题长度必须为 1-80 个字符")
	}
	if utf8.RuneCountInString(input.Description) > 500 {
		return errors.New("红包说明不能超过 500 个字符")
	}
	if utf8.RuneCountInString(input.CoverPrompt) > 1000 {
		return errors.New("红包封面提示词不能超过 1000 个字符")
	}
	if input.PerUserLimit <= 0 {
		input.PerUserLimit = 1
	}
	if input.PerUserLimit > 100 {
		return errors.New("单用户领取次数不能超过 100")
	}
	if input.StartAt < 0 || input.EndAt < 0 || (input.StartAt > 0 && input.EndAt > 0 && input.EndAt <= input.StartAt) {
		return errors.New("红包有效时间无效")
	}
	if len(input.CoverImage) > maxRedPacketCoverBytes {
		return errors.New("红包封面不能超过 3MB")
	}
	if input.CoverImage != "" &&
		!strings.HasPrefix(input.CoverImage, "data:image/") &&
		!strings.HasPrefix(input.CoverImage, "https://") &&
		!strings.HasPrefix(input.CoverImage, "http://") {
		return errors.New("红包封面必须是图片 data URL 或 http(s) URL")
	}
	if requireItems && len(input.Items) == 0 {
		return errors.New("红包至少需要一个奖励")
	}
	if len(input.Items) > 1000 {
		return errors.New("单个红包最多包含 1000 个奖励")
	}
	input.DrawMode = model.NormalizeRedPacketDrawMode(input.DrawMode)
	return nil
}

func AdminListRedPackets(c *gin.Context) {
	packets, err := model.ListRedPackets()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, packets)
}

func AdminCreateRedPacket(c *gin.Context) {
	var input redPacketMutationRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := validateRedPacketMutation(&input, true); err != nil {
		common.ApiError(c, err)
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	packet := model.RedPacket{
		Title: input.Title, Description: input.Description,
		CoverImage: model.RedPacketCoverImage(input.CoverImage), CoverPrompt: input.CoverPrompt,
		DrawMode: input.DrawMode, PerUserLimit: input.PerUserLimit,
		StartAt: input.StartAt, EndAt: input.EndAt, Enabled: enabled, CreatedBy: c.GetInt("id"),
	}
	if err := model.CreateRedPacket(&packet, input.Items); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "red_packet.create", map[string]interface{}{
		"id": packet.Id, "slug": packet.Slug, "items": len(input.Items),
		"draw_mode": packet.DrawMode, "per_user_limit": packet.PerUserLimit,
	})
	common.ApiSuccess(c, packet)
}

func AdminUpdateRedPacket(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("无效的红包 ID"))
		return
	}
	var input redPacketMutationRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := validateRedPacketMutation(&input, false); err != nil {
		common.ApiError(c, err)
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	packet := model.RedPacket{
		Title: input.Title, Description: input.Description,
		CoverImage: model.RedPacketCoverImage(input.CoverImage), CoverPrompt: input.CoverPrompt,
		DrawMode: input.DrawMode, PerUserLimit: input.PerUserLimit,
		StartAt: input.StartAt, EndAt: input.EndAt, Enabled: enabled,
	}
	if err := model.UpdateRedPacket(id, packet); err != nil {
		common.ApiError(c, err)
		return
	}
	if input.Items != nil {
		if err := model.ReplaceRedPacketItems(id, input.Items); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	recordManageAudit(c, "red_packet.update", map[string]interface{}{"id": id, "items": len(input.Items)})
	common.ApiSuccess(c, nil)
}

func AdminDeleteRedPacket(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("无效的红包 ID"))
		return
	}
	if err := model.DeleteRedPacket(id); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "red_packet.delete", map[string]interface{}{"id": id})
	common.ApiSuccess(c, nil)
}

func GetRedPacket(c *gin.Context) {
	packet, err := model.GetRedPacketPublic(c.Param("slug"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": packet})
}

func ClaimRedPacket(c *gin.Context) {
	reward, err := model.ClaimRedPacket(c.Param("slug"), c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, reward)
}

func GetMyRedPacketClaims(c *gin.Context) {
	claims, err := model.ListUserRedPacketClaims(c.Param("slug"), c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, claims)
}

// RedeemCodeV2 keeps detailed reward metadata for reset-voucher redemption
// while the legacy /api/user/topup endpoint continues returning a numeric quota.
func RedeemCodeV2(c *gin.Context) {
	var req struct {
		Key string `json:"key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	result, err := model.RedeemWithResult(strings.TrimSpace(req.Key), c.GetInt("id"))
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgRedeemFailed)
		logger.LogError(c, redeemFailureLog(c.GetInt("id"), err))
		return
	}
	common.ApiSuccess(c, result)
}
