package doubao

import (
	"fmt"
	"strings"
)

var ModelList = []string{
	"doubao-seedance-1-0-pro-250528",
	"doubao-seedance-1-0-lite-t2v",
	"doubao-seedance-1-0-lite-i2v",
	"doubao-seedance-1-5-pro-251215",
	"doubao-seedance-2-0-260128",
	"doubao-seedance-2-0-fast-260128",
}

var ChannelName = "doubao-video"

type videoTier struct {
	price      float64
	videoPrice float64
}

// Profiles describe only the models this native adaptor advertises. Prices are
// relative to the configured model rate, never replacements for that rate.
// Capabilities: https://github.com/QuantumNous/new-api/commit/65d3a2171fd11c65ad2e2e1f1d65723926659c81
// Audio ratio: https://docs.volcengine.com/docs/Coze/Modelcost?lang=zh
// Seedance 2.0 rates retain the existing native adaptor's relative prices.
type videoModelProfile struct {
	resolutions           []string
	tiers                 map[string]videoTier
	basePrice             float64
	reservationResolution string
	referenceVideo        bool
	silentRatio           float64 // zero means audio is not a billing dimension
}

func uniformVideoProfile(silentRatio float64) videoModelProfile {
	return videoModelProfile{
		resolutions: []string{"480p", "720p", "1080p"},
		tiers: map[string]videoTier{
			"480p": {price: 1}, "720p": {price: 1}, "1080p": {price: 1},
		},
		basePrice: 1, reservationResolution: "1080p", silentRatio: silentRatio,
	}
}

var videoModelProfiles = map[string]videoModelProfile{
	"doubao-seedance-1-0-pro-250528": uniformVideoProfile(0),
	"doubao-seedance-1-0-lite-t2v":   uniformVideoProfile(0),
	"doubao-seedance-1-0-lite-i2v":   uniformVideoProfile(0),
	"doubao-seedance-1-5-pro-251215": uniformVideoProfile(8.0 / 16.0),
	"doubao-seedance-2-0-260128": {
		resolutions: []string{"480p", "720p", "1080p", "4k"},
		tiers: map[string]videoTier{
			"480p": {price: 46, videoPrice: 28}, "720p": {price: 46, videoPrice: 28},
			"1080p": {price: 51, videoPrice: 31}, "4k": {price: 26, videoPrice: 16},
		},
		basePrice: 46, reservationResolution: "1080p", referenceVideo: true,
	},
	"doubao-seedance-2-0-fast-260128": {
		resolutions: []string{"480p", "720p"},
		tiers: map[string]videoTier{
			"480p": {price: 37, videoPrice: 22}, "720p": {price: 37, videoPrice: 22},
		},
		basePrice: 37, reservationResolution: "720p", referenceVideo: true,
	},
}

// Endpoint IDs do not identify a model family. In that case a known public
// model still supplies its profile; a mapping to another known model uses that
// executing model's profile instead.
func videoProfileForModels(names ...string) (videoModelProfile, string, bool) {
	for _, name := range names {
		if profile, ok := videoModelProfiles[name]; ok {
			return profile, name, true
		}
	}
	return videoModelProfile{}, "", false
}

func (p videoModelProfile) validateResolution(modelName, resolution string) error {
	if resolution == "" {
		return nil // Ark chooses it; billing reserves a supported tier.
	}
	if _, ok := p.tiers[resolution]; !ok {
		return fmt.Errorf("%s resolution must be one of %s", modelName, strings.Join(p.resolutions, ", "))
	}
	return nil
}

func (p videoModelProfile) billingRatios(resolution string, hasVideo, generateAudio bool) map[string]float64 {
	if resolution == "" {
		resolution = p.reservationResolution
	}
	tier, ok := p.tiers[resolution]
	if !ok || p.basePrice <= 0 {
		return nil
	}
	// Store each supported dimension even when its multiplier is one. This
	// preserves reference/audio state for completion without conflating a 4k
	// resolution discount with reference-video input.
	ratios := map[string]float64{"resolution": tier.price / p.basePrice}
	if p.referenceVideo {
		ratios["video_input"] = 1
		if hasVideo {
			ratios["video_input"] = tier.videoPrice / tier.price
		}
	}
	if p.silentRatio > 0 {
		ratios["generate_audio"] = 1
		if !generateAudio {
			ratios["generate_audio"] = p.silentRatio
		}
	}
	return ratios
}

// GetVideoInputRatio returns the combined capability multiplier. Unsupported
// tiers are never treated as the base tier; callers must validate them locally.
func GetVideoInputRatio(modelName, resolution string, hasVideo bool) (float64, bool) {
	profile, _, ok := videoProfileForModels(modelName)
	if !ok {
		return 0, false
	}
	ratios := profile.billingRatios(strings.ToLower(strings.TrimSpace(resolution)), hasVideo, true)
	if ratios == nil {
		return 0, false
	}
	ratio := 1.0
	for _, multiplier := range ratios {
		ratio *= multiplier
	}
	return ratio, true
}
