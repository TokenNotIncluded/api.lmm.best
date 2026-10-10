package claudemessages

import (
	"fmt"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	kitutil "github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/kitutil"
)

func claudeSourceURL(source *dto.ClaudeMessageSource) string {
	if source == nil {
		return ""
	}
	if strings.TrimSpace(source.Url) != "" {
		return source.Url
	}
	data := kitutil.Interface2String(source.Data)
	if data == "" {
		return ""
	}
	if strings.HasPrefix(data, "data:") {
		return data
	}
	return fmt.Sprintf("data:%s;base64,%s", source.MediaType, data)
}
