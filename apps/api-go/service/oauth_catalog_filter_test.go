package service

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/stretchr/testify/assert"
)

func TestOAuthCatalogDoesNotExposeImageModelsAsChat(t *testing.T) {
	for _, name := range []string{"gpt-image-2", "gpt-image-2.5-flare", "dall-e-3"} {
		assert.Contains(t, common.GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, name), constant.EndpointTypeOpenAI)
		assert.Empty(t, oauthAPIs(constant.ChannelTypeOpenAI, name), name)
	}
	assert.Contains(t, oauthAPIs(constant.ChannelTypeOpenAI, "gpt-5.6-sol"), "openai-completions")
}
