package relay

import (
	"fmt"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/wsmanager"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func WssHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	var socketMu sync.Mutex
	var closeOnce sync.Once
	closed := false
	closeCode := websocket.CloseServiceRestart
	closeReason := wsmanager.ServiceRestartReason
	closeSocket := func(conn *websocket.Conn, code int, reason string) {
		if conn == nil {
			return
		}
		deadline := time.Now().Add(time.Second)
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), deadline)
		_ = conn.Close()
	}
	unregister, accepted := wsmanager.Register(info.ChannelId, wsmanager.KindRealtime, func(code int, reason string) {
		closeOnce.Do(func() {
			socketMu.Lock()
			closed = true
			closeCode = code
			closeReason = reason
			target := info.TargetWs
			socketMu.Unlock()
			closeSocket(info.ClientWs, code, reason)
			closeSocket(target, code, reason)
		})
	})
	if !accepted {
		return nil
	}
	defer unregister()

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	//var requestBody io.Reader
	//firstWssRequest, _ := c.Get("first_wss_request")
	//requestBody = bytes.NewBuffer(firstWssRequest.([]byte))

	statusCodeMappingStr := c.GetString("status_code_mapping")
	resp, err := adaptor.DoRequest(c, info, nil)
	if err != nil {
		socketMu.Lock()
		wasClosed := closed
		socketMu.Unlock()
		if wasClosed {
			return nil
		}
		return types.NewError(err, types.ErrorCodeDoRequestFailed)
	}

	if resp != nil {
		target := resp.(*websocket.Conn)
		common.SetWebSocketReadLimit(target)
		socketMu.Lock()
		if closed {
			code, reason := closeCode, closeReason
			socketMu.Unlock()
			closeSocket(target, code, reason)
			return nil
		}
		info.TargetWs = target
		socketMu.Unlock()
		defer target.Close()
	}

	usage, newAPIError := adaptor.DoResponse(c, nil, info)
	if newAPIError != nil {
		socketMu.Lock()
		wasClosed := closed
		socketMu.Unlock()
		if wasClosed {
			return nil
		}
		// reset status code 重置状态码
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}
	service.PostWssConsumeQuota(c, info, info.UpstreamModelName, usage.(*dto.RealtimeUsage), "")
	return nil
}
