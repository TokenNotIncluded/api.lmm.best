package middleware

import (
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRecoveryContextDoesNotEnableRelayKeys(t *testing.T) {
	for _, mode := range []constant.MultiKeyMode{constant.MultiKeyModeRandom, constant.MultiKeyModePolling, ""} {
		channel := &model.Channel{Id: 98765, Key: "auto\nmanual", Status: common.ChannelStatusAutoDisabled, ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeyMode: mode, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled, 1: common.ChannelStatusManuallyDisabled}}}
		var wg sync.WaitGroup
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/", nil)
				if err := SetupContextForChannelRecovery(c, channel, "gpt-4o-mini", 0); err != nil {
					t.Error(err)
				}
				if _, _, err := channel.GetNextEnabledKey(); err == nil {
					t.Error("relay selected disabled credential")
				}
			}()
		}
		wg.Wait()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.NotNil(t, SetupContextForChannelRecovery(c, channel, "gpt-4o-mini", 1))
		require.NotNil(t, SetupContextForSelectedChannel(c, channel, "gpt-4o-mini"))
		require.Equal(t, common.ChannelStatusAutoDisabled, channel.ChannelInfo.MultiKeyStatusList[0])
	}
}
