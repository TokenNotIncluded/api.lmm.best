package channel

import (
	"errors"
	"fmt"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
)

// Endpoint identifies a relay protocol that an adaptor may implement.
type Endpoint string

const (
	EndpointClaudeMessages Endpoint = "claude_messages"
	EndpointRerank         Endpoint = "rerank"
)

// UnsupportedEndpointError is returned by an adaptor when it cannot safely
// convert or pass through a requested relay endpoint.
type UnsupportedEndpointError struct {
	Channel  string
	Endpoint Endpoint
}

func (e *UnsupportedEndpointError) Error() string {
	if e == nil {
		return "channel does not support requested endpoint"
	}
	if e.Channel == "" {
		return fmt.Sprintf("channel does not support %s endpoint", e.Endpoint)
	}
	return fmt.Sprintf("%s channel does not support %s endpoint", e.Channel, e.Endpoint)
}

func NewUnsupportedEndpointError(channel string, endpoint Endpoint) *UnsupportedEndpointError {
	return &UnsupportedEndpointError{Channel: channel, Endpoint: endpoint}
}

func IsUnsupportedEndpointError(err error) bool {
	var unsupported *UnsupportedEndpointError
	return errors.As(err, &unsupported)
}

// EndpointSupporter lets adaptors reject unsupported protocols before request
// conversion or any upstream work begins.
type EndpointSupporter interface {
	SupportsEndpoint(endpoint Endpoint) bool
}

// SupportsEndpoint preserves legacy adaptors as supported by default. Adaptors
// that cannot implement an endpoint must opt out through EndpointSupporter and
// return UnsupportedEndpointError from the corresponding converter.
func SupportsEndpoint(adaptor Adaptor, endpoint Endpoint) bool {
	if adaptor == nil {
		return false
	}
	supporter, ok := adaptor.(EndpointSupporter)
	return !ok || supporter.SupportsEndpoint(endpoint)
}

func EndpointForRequestPath(path string) (Endpoint, bool) {
	switch {
	case strings.HasSuffix(path, "/v1/messages"):
		return EndpointClaudeMessages, true
	case strings.HasSuffix(path, "/v1/rerank"):
		return EndpointRerank, true
	default:
		return "", false
	}
}

func EndpointForRelayFormat(format types.RelayFormat) (Endpoint, bool) {
	switch format {
	case types.RelayFormatClaude:
		return EndpointClaudeMessages, true
	case types.RelayFormatRerank:
		return EndpointRerank, true
	default:
		return "", false
	}
}
