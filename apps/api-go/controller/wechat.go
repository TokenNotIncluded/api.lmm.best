package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/model"

	"github.com/gin-gonic/gin"
)

type wechatLoginResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

// The WeChat identity response is a small fixed-shape JSON document. Keep
// provider-controlled error text bounded before json.Decoder can buffer it.
const wechatProviderResponseMaxBytes int64 = 64 << 10

const wechatProviderPath = "/api/wechat/user"

func newWeChatProviderSSRFProtection() *common.SSRFProtection {
	return &common.SSRFProtection{
		DomainFilterMode:       false,
		IpFilterMode:           false,
		ApplyIPFilterForDomain: true,
	}
}

// validateWeChatProviderURL applies an independent outbound request policy.
// WeChatServerAddress is editable through the admin option API, so it must
// not be allowed to target loopback, link-local, private, or reserved hosts.
// DNS is resolved as part of validation to prevent a hostname from bypassing
// the address policy.
var validateWeChatProviderURL = func(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return errors.New("微信服务器地址无效")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("微信服务器地址仅支持 HTTP 或 HTTPS")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return errors.New("微信服务器地址不得包含用户信息或片段")
	}
	protection := newWeChatProviderSSRFProtection()
	if err := protection.ValidateURL(rawURL); err != nil {
		return fmt.Errorf("微信服务器地址被出站安全策略拒绝: %w", err)
	}
	return nil
}

// dialWeChatProvider resolves a hostname and dials the exact validated IP.
// The request URL keeps the original hostname for TLS SNI, but the connection
// never asks the default transport to resolve it a second time. This closes
// the DNS-rebinding window between validation and connect.
func dialWeChatProvider(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("微信服务器地址无效: %w", err)
	}
	protection := newWeChatProviderSSRFProtection()
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	ips := []net.IP{}
	if ip := net.ParseIP(host); ip != nil {
		ips = append(ips, ip)
	} else {
		ips, err = net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("微信服务器 DNS 解析失败: %w", err)
		}
	}
	var lastErr error
	for _, ip := range ips {
		if err := protection.ValidateResolvedIP(host, ip); err != nil {
			lastErr = err
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = errors.New("微信服务器没有可用地址")
	}
	return nil, lastErr
}

var newWeChatHTTPClient = func() *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialWeChatProvider,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

type wechatLoginStartRequest struct {
	AcceptedLegal bool   `json:"accepted_legal"`
	Aff           string `json:"aff,omitempty"`
}

// WeChatAuthStart creates browser-bound state before the code is submitted.
// The WeChat verification code itself is not signed by this application, so
// the state cookie and one-time AuthFlow are the only local CSRF boundary.
func WeChatAuthStart(c *gin.Context) {
	if !common.WeChatAuthEnabled {
		c.JSON(http.StatusOK, gin.H{
			"message": "管理员未开启通过微信登录以及注册",
			"success": false,
		})
		return
	}
	var request wechatLoginStartRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	request.Aff = strings.TrimSpace(request.Aff)
	if len(request.Aff) > 32 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	payload, err := common.Marshal(oauthFlowPayload{
		AcceptedLegal: request.AcceptedLegal,
		AffiliateCode: request.Aff,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	expiresAt := time.Now().Add(oauthAuthFlowTTL)
	state, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose:   model.AuthFlowPurposeWeChatLogin,
		Provider:  "wechat",
		Intent:    model.AuthFlowIntentLogin,
		Payload:   string(payload),
		ExpiresAt: expiresAt,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	setOAuthStateCookie(c, "wechat", state)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"flow_token": state,
			"expires_at": expiresAt.Unix(),
		},
	})
}

func getWeChatIdByCode(code string) (string, error) {
	if code == "" {
		return "", errors.New("无效的参数")
	}
	baseURL, err := url.Parse(strings.TrimSpace(common.WeChatServerAddress))
	if err != nil || baseURL.Scheme == "" || baseURL.Hostname() == "" {
		return "", errors.New("微信服务器地址无效")
	}
	if baseURL.RawQuery != "" || baseURL.Fragment != "" || baseURL.User != nil {
		return "", errors.New("微信服务器地址不得包含用户信息、查询参数或片段")
	}
	if err := validateWeChatProviderURL(baseURL.String()); err != nil {
		return "", err
	}
	query := baseURL.Query()
	query.Set("code", code)
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + wechatProviderPath
	baseURL.RawPath = ""
	baseURL.RawQuery = query.Encode()
	providerURL := baseURL.String()
	if err := validateWeChatProviderURL(providerURL); err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodGet, providerURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", common.WeChatServerToken)
	client := newWeChatHTTPClient()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 {
			return errors.New("微信服务器不允许重定向")
		}
		return validateWeChatProviderURL(req.URL.String())
	}
	// Strict scheme, host, DNS, and private-address validation precedes this sink.
	// lgtm [go/request-forgery]
	httpResponse, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer httpResponse.Body.Close()
	if err := common.LimitResponseBody(httpResponse, wechatProviderResponseMaxBytes); err != nil {
		return "", err
	}
	var res wechatLoginResponse
	err = common.DecodeJson(httpResponse.Body, &res)
	if err != nil {
		return "", err
	}
	if !res.Success {
		return "", errors.New(res.Message)
	}
	if res.Data == "" {
		return "", errors.New("验证码错误或已过期")
	}
	return res.Data, nil
}

// writeWeChatProviderError keeps upstream diagnostics out of the public
// response. The WeChat helper is an external service boundary and its message
// may contain implementation details or provider-controlled text; retain it
// only in server logs while returning the stable, localized OAuth error.
func writeWeChatProviderError(c *gin.Context, err error) {
	if err != nil {
		common.SysLog("WeChat OAuth provider request failed: " + err.Error())
	}
	common.ApiErrorI18n(c, i18n.MsgOAuthGetUserErr)
}

func WeChatAuth(c *gin.Context) {
	if !common.WeChatAuthEnabled {
		c.JSON(http.StatusOK, gin.H{
			"message": "管理员未开启通过微信登录以及注册",
			"success": false,
		})
		return
	}
	state := c.Query("state")
	if !oauthStateCookieMatches(c, "wechat", state) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgOAuthStateInvalid),
		})
		return
	}
	code := c.Query("code")
	wechatId, err := getWeChatIdByCode(code)
	if err != nil {
		writeWeChatProviderError(c, err)
		return
	}
	flow, err := model.ConsumeAuthFlow(state, model.AuthFlowMatch{
		Purpose:  model.AuthFlowPurposeWeChatLogin,
		Provider: "wechat",
		Intent:   model.AuthFlowIntentLogin,
	})
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgOAuthStateInvalid),
		})
		return
	}
	clearOAuthStateCookie(c, "wechat")
	var payload oauthFlowPayload
	if err := common.UnmarshalJsonStr(flow.Payload, &payload); err != nil {
		common.ApiError(c, err)
		return
	}
	user, ok := findOrCreateWeChatUser(c, wechatId, payload.AffiliateCode, payload.AcceptedLegal)
	if !ok {
		return
	}

	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusOK, gin.H{
			"message": "用户已被封禁",
			"success": false,
		})
		return
	}
	setupLogin(user, c)
}

func findOrCreateWeChatUser(c *gin.Context, wechatId, affiliateCode string, acceptedLegal bool) (*model.User, bool) {
	user := &model.User{WeChatId: wechatId}
	if model.IsWeChatIdAlreadyTaken(wechatId) {
		err := user.FillUserByWeChatId()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return nil, false
		}
		if user.Id == 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "用户已注销",
			})
			return nil, false
		}
	} else {
		if common.RegisterEnabled {
			if !common.OAuthRegisterEnabled {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "管理员已关闭通过 OAuth 注册",
				})
				return nil, false
			}
			if common.IsRegistrationMethodDisabled("wechat") {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "管理员已关闭通过微信注册",
				})
				return nil, false
			}
			if !requirePublicRegistrationLegal(c, acceptedLegal) {
				return nil, false
			}
			// WeChat's login assertion does not provide an email address. When
			// strict verification is enabled, it cannot create a new account;
			// existing identities were handled above and can still sign in.
			if common.EmailVerificationEnabled {
				common.ApiErrorI18n(c, i18n.MsgOAuthEmailVerificationRequired)
				return nil, false
			}
			user.Username = "wechat_" + strconv.Itoa(model.GetMaxUserId()+1)
			user.DisplayName = "WeChat User"
			user.Role = common.RoleCommonUser
			user.Status = common.UserStatusEnabled

			inviterId, _ := model.GetUserIdByAffCode(affiliateCode)
			if err := user.Insert(inviterId); err != nil {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": err.Error(),
				})
				return nil, false
			}
		} else {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "管理员关闭了新用户注册",
			})
			return nil, false
		}
	}
	return user, true
}

type wechatBindRequest struct {
	Code string `json:"code"`
}

func WeChatBind(c *gin.Context) {
	if !common.WeChatAuthEnabled {
		c.JSON(http.StatusOK, gin.H{
			"message": "管理员未开启通过微信登录以及注册",
			"success": false,
		})
		return
	}
	var req wechatBindRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的请求",
		})
		return
	}
	code := req.Code
	wechatId, err := getWeChatIdByCode(code)
	if err != nil {
		writeWeChatProviderError(c, err)
		return
	}
	if model.IsWeChatIdAlreadyTaken(wechatId) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "该微信账号已被绑定",
		})
		return
	}
	userID := c.GetInt("id")
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "未登录"})
		return
	}
	// Update only the binding column. A full user snapshot can overwrite a
	// concurrent role, status, group, or quota change made while the OAuth
	// provider request was in flight.
	if err := model.UpdateUserBindColumn(userID, "wechat_id", wechatId); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}
