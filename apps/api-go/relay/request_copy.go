package relay

import "github.com/LIghtJUNction/api.lmm.best/common"

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
	return common.DeepCopy(src)
}
