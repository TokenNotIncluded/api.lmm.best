/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

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
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"

	"github.com/gin-gonic/gin"
)

const affiliateInvitationUnavailableMessage = "邀请邮件暂时无法发送，请稍后重试或复制邀请链接。"

type affiliateInvitationRequest struct {
	Email string `json:"email" binding:"required"`
}

type affiliateInvitationEmailData struct {
	SystemName string
	InviteURL  string
}

var affiliateInvitationEmailTemplate = template.Must(template.New("affiliate-invitation").Parse(`<!doctype html>
<html lang="zh-CN">
  <body style="margin:0;background:#f5f4ef;color:#171714;font-family:Arial,'PingFang SC','Microsoft YaHei',sans-serif;">
    <div style="display:none;max-height:0;overflow:hidden;opacity:0;">你的好友邀请你加入 {{.SystemName}}</div>
    <table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="background:#f5f4ef;padding:32px 16px;">
      <tr><td align="center">
        <table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="max-width:560px;background:#ffffff;border:1px solid #deddd6;">
          <tr><td style="padding:36px 36px 20px;">
            <div style="display:inline-block;background:#171714;color:#ffffff;font-size:24px;line-height:48px;text-align:center;width:48px;height:48px;">✦</div>
            <h1 style="margin:24px 0 12px;font-size:26px;line-height:1.3;">好友邀请你加入 {{.SystemName}}</h1>
            <p style="margin:0;color:#66645d;font-size:16px;line-height:1.7;">注册后即可开始使用统一的 AI API 服务。点击下方按钮，邀请关系会自动记录。</p>
          </td></tr>
          <tr><td style="padding:8px 36px 36px;">
            <a href="{{.InviteURL}}" style="display:inline-block;background:#171714;color:#ffffff;text-decoration:none;font-size:15px;font-weight:700;padding:14px 22px;">接受邀请</a>
            <p style="margin:24px 0 8px;color:#66645d;font-size:13px;line-height:1.6;">如果按钮无法打开，请复制以下链接：</p>
            <p style="margin:0;word-break:break-all;color:#3f6254;font-size:13px;line-height:1.6;">{{.InviteURL}}</p>
          </td></tr>
          <tr><td style="border-top:1px solid #deddd6;padding:18px 36px;color:#88867e;font-size:12px;line-height:1.6;">如果你不认识邀请人，可以忽略这封邮件。{{.SystemName}} 不会在邮件中索要密码或密钥。</td></tr>
        </table>
      </td></tr>
    </table>
  </body>
</html>`))

var sendAffiliateInvitationEmail = common.SendEmail

// SendAffiliateInvitation emails the authenticated user's existing affiliate
// URL. SMTP credentials and the configured public origin never leave the
// server; the client submits only the recipient address.
func SendAffiliateInvitation(c *gin.Context) {
	var request affiliateInvitationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	recipient := model.NormalizeEmail(request.Email)
	if err := common.Validate.Var(recipient, "required,email,max=254"); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	user, err := model.GetUserById(c.GetInt("id"), true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if recipient == model.NormalizeEmail(user.Email) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "不能向自己的邮箱发送邀请。",
		})
		return
	}
	if err := ensureAffiliateCode(user); err != nil {
		common.ApiError(c, err)
		return
	}

	systemName := normalizeAffiliateInvitationSystemName(common.SystemName)
	content, inviteURL, err := buildAffiliateInvitationEmailContent(
		system_setting.ServerAddress,
		systemName,
		user.AffCode,
	)
	if err != nil {
		logger.LogError(c.Request.Context(), "failed to build affiliate invitation email: "+err.Error())
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": affiliateInvitationUnavailableMessage,
		})
		return
	}

	subject := fmt.Sprintf("好友邀请你加入 %s", systemName)
	if err := sendAffiliateInvitationEmail(subject, recipient, content); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("failed to send affiliate invitation email to %s: %s", common.MaskEmail(recipient), err.Error()))
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": affiliateInvitationUnavailableMessage,
		})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("user %d sent an affiliate invitation to %s via %s", user.Id, common.MaskEmail(recipient), inviteURL.Hostname()))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "邀请邮件已发送。",
	})
}

func ensureAffiliateCode(user *model.User) error {
	if user.AffCode != "" {
		return nil
	}

	candidate := common.GetRandomString(4)
	result := model.DB.Model(&model.User{}).
		Where("id = ? AND aff_code = ?", user.Id, "").
		Update("aff_code", candidate)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		user.AffCode = candidate
		return nil
	}

	var persisted model.User
	if err := model.DB.Select("aff_code").First(&persisted, user.Id).Error; err != nil {
		return err
	}
	if persisted.AffCode == "" {
		return fmt.Errorf("affiliate code was not persisted for user %d", user.Id)
	}
	user.AffCode = persisted.AffCode
	return nil
}

func normalizeAffiliateInvitationSystemName(systemName string) string {
	name := strings.Join(strings.Fields(systemName), " ")
	if name == "" {
		return "LMM API"
	}
	const maxNameRunes = 80
	nameRunes := []rune(name)
	if len(nameRunes) > maxNameRunes {
		return string(nameRunes[:maxNameRunes])
	}
	return name
}

func buildAffiliateInvitationEmailContent(
	serverAddress string,
	systemName string,
	affiliateCode string,
) (string, *url.URL, error) {
	inviteURL, err := url.Parse(strings.TrimSpace(serverAddress))
	if err != nil {
		return "", nil, fmt.Errorf("invalid server address: %w", err)
	}
	if inviteURL.Host == "" ||
		(inviteURL.Scheme != "http" && inviteURL.Scheme != "https") ||
		inviteURL.User != nil || inviteURL.RawQuery != "" || inviteURL.Fragment != "" {
		return "", nil, fmt.Errorf("server address must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}

	inviteURL.Path = strings.TrimRight(inviteURL.Path, "/") + "/sign-up"
	query := inviteURL.Query()
	query.Set("aff", affiliateCode)
	inviteURL.RawQuery = query.Encode()

	name := normalizeAffiliateInvitationSystemName(systemName)

	var content bytes.Buffer
	if err := affiliateInvitationEmailTemplate.Execute(&content, affiliateInvitationEmailData{
		SystemName: name,
		InviteURL:  inviteURL.String(),
	}); err != nil {
		return "", nil, fmt.Errorf("render affiliate invitation email: %w", err)
	}
	return content.String(), inviteURL, nil
}
