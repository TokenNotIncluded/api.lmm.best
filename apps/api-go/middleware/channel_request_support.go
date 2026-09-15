package middleware

import "github.com/LIghtJUNction/api.lmm.best/model"

// ChannelSupportsRequestPath exposes the distributor's path/capability check to
// retry selectors. Keeping retry admission on the same predicate prevents a
// later attempt from routing onto a channel that the initial distributor would
// have rejected for the requested protocol.
func ChannelSupportsRequestPath(selected *model.Channel, requestPath string, requestModel string) bool {
	return channelSupportsRequestPath(selected, requestPath, requestModel)
}
