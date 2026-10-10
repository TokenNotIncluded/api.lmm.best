package common

import (
	"regexp"
	"strings"
)

// ResponseModel contains provider-originated diagnostic names only. No billing,
// routing or response-conversion decision may depend on this observation.
// Mismatch is derived on read rather than persisted with the names.
type ResponseModel struct {
	RequestedModel string `json:"requested_model"`
	UpstreamModel  string `json:"upstream_model"`
	ReturnedModel  string `json:"returned_model"`
}

var responseModelCompatibleSuffix = regexp.MustCompile(`^-(?:[0-9]{8}|[0-9]{4}-[0-9]{2}-[0-9]{2}|latest|preview(?:-(?:[0-9]{2}-[0-9]{2}|[0-9]{2}-[0-9]{4}|[0-9]{8}|[0-9]{4}-[0-9]{2}-[0-9]{2}))?)$`)

func canonicalResponseModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if index := strings.LastIndexByte(model, '/'); index >= 0 {
		model = model[index+1:]
	}
	return model
}

func (r *ResponseModel) matches(model string) bool {
	returned := canonicalResponseModel(model)
	for _, candidate := range []string{r.RequestedModel, r.UpstreamModel} {
		expected := canonicalResponseModel(candidate)
		if expected == "" {
			continue
		}
		if returned == expected || (strings.HasPrefix(returned, expected) && responseModelCompatibleSuffix.MatchString(strings.TrimPrefix(returned, expected))) {
			return true
		}
	}
	return false
}

func (r *ResponseModel) Mismatch() bool {
	return r != nil && strings.TrimSpace(r.ReturnedModel) != "" && !r.matches(r.ReturnedModel)
}

// Useful omits an exact ordinary response, but retains mapping, casing, provider
// paths and snapshot aliases so operators can distinguish all three names.
func (r *ResponseModel) Useful() bool {
	return r != nil && strings.TrimSpace(r.ReturnedModel) != "" &&
		!(r.ReturnedModel == r.RequestedModel && (r.UpstreamModel == "" || r.UpstreamModel == r.RequestedModel))
}

func (info *RelayInfo) resetResponseModel() {
	if info.responseModelRequestedModel == "" {
		info.responseModelRequestedModel = info.OriginModelName
	}
	info.ResponseModel = nil
	info.responseModelSelectedModel = ""
	info.responseModelSelectionSeen = false
}

// ObserveResponseModel must run before conversion or legacy handlers mutate
// UpstreamModelName. The selected model snapshot then remains independent of
// those existing behavior/billing fields throughout this relay attempt.
func (info *RelayInfo) ObserveResponseModel(model string) {
	if info == nil {
		return
	}
	// Even an empty declaration can precede legacy mutation of the selected
	// field (native Claude message_start). Freeze selection before that mutation.
	if !info.responseModelSelectionSeen {
		info.responseModelSelectedModel = info.GetUpstreamModelName()
		info.responseModelSelectionSeen = true
	}
	if strings.TrimSpace(model) == "" {
		return
	}
	if info.ResponseModel == nil {
		requested := info.responseModelRequestedModel
		if requested == "" {
			requested = info.OriginModelName
		}
		info.ResponseModel = &ResponseModel{RequestedModel: requested, UpstreamModel: info.responseModelSelectedModel}
	}
	observation := info.ResponseModel
	if observation.Mismatch() {
		return
	}
	if observation.matches(model) && observation.ReturnedModel != "" &&
		observation.ReturnedModel != observation.RequestedModel && observation.ReturnedModel != observation.UpstreamModel {
		return
	}
	observation.ReturnedModel = model
}
