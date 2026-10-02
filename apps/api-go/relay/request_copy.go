package relay

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert"
)

// copyRequestForRelay isolates top-level metadata (model, stream options, etc.)
// while avoiding a deep copy of payloads that will be sent verbatim from their
// original BodyStorage. Passthrough callers MUST NOT mutate nested values or
// pass this shallow copy to a provider converter. Converted requests retain the
// existing deep-copy path, so channel retries cannot mutate the original DTO.
func copyRequestForRelay[T any](src *T, passthrough bool) (*T, error) {
	if passthrough && src != nil {
		copy := *src
		return &copy, nil
	}
	return copyMutableRequestForRelay(src, passthrough)
}

// Claude and Gemini handlers can change nested provider settings even when the
// raw body is forwarded. Keep those copies isolated across channel retries.
func copyMutableRequestForRelay[T any](src *T, passthrough bool) (*T, error) {
	copy, err := common.DeepCopy(src)
	if err != nil {
		return nil, err
	}
	if !passthrough {
		if err := relayconvert.SanitizeToolSchemas(copy); err != nil {
			return nil, err
		}
	}
	return copy, nil
}
