package relay

import (
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/factory"
	taskali "github.com/LIghtJUNction/api.lmm.best/relay/channel/task/ali"
	taskdoubao "github.com/LIghtJUNction/api.lmm.best/relay/channel/task/doubao"
	taskGemini "github.com/LIghtJUNction/api.lmm.best/relay/channel/task/gemini"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/task/hailuo"
	taskjimeng "github.com/LIghtJUNction/api.lmm.best/relay/channel/task/jimeng"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/task/kling"
	tasksora "github.com/LIghtJUNction/api.lmm.best/relay/channel/task/sora"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/task/suno"
	taskvertex "github.com/LIghtJUNction/api.lmm.best/relay/channel/task/vertex"
	taskVidu "github.com/LIghtJUNction/api.lmm.best/relay/channel/task/vidu"
	"github.com/gin-gonic/gin"
)

func GetAdaptor(apiType int) channel.Adaptor {
	return factory.GetAdaptor(apiType)
}

func GetTaskPlatform(c *gin.Context) constant.TaskPlatform {
	channelType := c.GetInt("channel_type")
	if channelType > 0 {
		return constant.TaskPlatform(strconv.Itoa(channelType))
	}
	return constant.TaskPlatform(c.GetString("platform"))
}

func GetTaskAdaptor(platform constant.TaskPlatform) channel.TaskAdaptor {
	switch platform {
	//case constant.APITypeAIProxyLibrary:
	//	return &aiproxy.Adaptor{}
	case constant.TaskPlatformSuno:
		return &suno.TaskAdaptor{}
	}
	if channelType, err := strconv.ParseInt(string(platform), 10, 64); err == nil {
		switch channelType {
		case constant.ChannelTypeAli:
			return &taskali.TaskAdaptor{}
		case constant.ChannelTypeKling:
			return &kling.TaskAdaptor{}
		case constant.ChannelTypeJimeng:
			return &taskjimeng.TaskAdaptor{}
		case constant.ChannelTypeVertexAi:
			return &taskvertex.TaskAdaptor{}
		case constant.ChannelTypeVidu:
			return &taskVidu.TaskAdaptor{}
		case constant.ChannelTypeDoubaoVideo, constant.ChannelTypeVolcEngine:
			return &taskdoubao.TaskAdaptor{}
		case constant.ChannelTypeSora, constant.ChannelTypeOpenAI, constant.ChannelTypeOpenHuman:
			return &tasksora.TaskAdaptor{}
		case constant.ChannelTypeGemini:
			return &taskGemini.TaskAdaptor{}
		case constant.ChannelTypeMiniMax:
			return &hailuo.TaskAdaptor{}
		}
	}
	return nil
}
