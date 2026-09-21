package zhipu_4v

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestRequestOpenAI2ZhipuForwardsReasoningEffort(t *testing.T) {
	converted := requestOpenAI2Zhipu(dto.GeneralOpenAIRequest{
		Model:           "glm-4.5",
		ReasoningEffort: "high",
	})

	require.NotNil(t, converted)
	require.Equal(t, "high", converted.ReasoningEffort)
}
