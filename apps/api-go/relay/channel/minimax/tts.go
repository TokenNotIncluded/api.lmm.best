package minimax

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

const maxRemoteTTSAudioBytes = 20 << 20

type MiniMaxTTSRequest struct {
	Model             string             `json:"model"`
	Text              string             `json:"text"`
	Stream            bool               `json:"stream,omitempty"`
	StreamOptions     *StreamOptions     `json:"stream_options,omitempty"`
	VoiceSetting      VoiceSetting       `json:"voice_setting"`
	PronunciationDict *PronunciationDict `json:"pronunciation_dict,omitempty"`
	AudioSetting      *AudioSetting      `json:"audio_setting,omitempty"`
	TimbreWeights     []TimbreWeight     `json:"timbre_weights,omitempty"`
	LanguageBoost     string             `json:"language_boost,omitempty"`
	VoiceModify       *VoiceModify       `json:"voice_modify,omitempty"`
	SubtitleEnable    bool               `json:"subtitle_enable,omitempty"`
	OutputFormat      string             `json:"output_format,omitempty"`
	AigcWatermark     bool               `json:"aigc_watermark,omitempty"`
}

type StreamOptions struct {
	ExcludeAggregatedAudio bool `json:"exclude_aggregated_audio,omitempty"`
}

type VoiceSetting struct {
	VoiceID           string  `json:"voice_id"`
	Speed             float64 `json:"speed,omitempty"`
	Vol               float64 `json:"vol,omitempty"`
	Pitch             int     `json:"pitch,omitempty"`
	Emotion           string  `json:"emotion,omitempty"`
	TextNormalization bool    `json:"text_normalization,omitempty"`
	LatexRead         bool    `json:"latex_read,omitempty"`
}

type PronunciationDict struct {
	Tone []string `json:"tone,omitempty"`
}

type AudioSetting struct {
	SampleRate int    `json:"sample_rate,omitempty"`
	Bitrate    int    `json:"bitrate,omitempty"`
	Format     string `json:"format,omitempty"`
	Channel    int    `json:"channel,omitempty"`
	ForceCbr   bool   `json:"force_cbr,omitempty"`
}

type TimbreWeight struct {
	VoiceID string `json:"voice_id"`
	Weight  int    `json:"weight"`
}

type VoiceModify struct {
	Pitch        int    `json:"pitch,omitempty"`
	Intensity    int    `json:"intensity,omitempty"`
	Timbre       int    `json:"timbre,omitempty"`
	SoundEffects string `json:"sound_effects,omitempty"`
}

type MiniMaxTTSResponse struct {
	Data      MiniMaxTTSData   `json:"data"`
	ExtraInfo MiniMaxExtraInfo `json:"extra_info"`
	TraceID   string           `json:"trace_id"`
	BaseResp  MiniMaxBaseResp  `json:"base_resp"`
}

type MiniMaxTTSData struct {
	Audio  string `json:"audio"`
	Status int    `json:"status"`
}

type MiniMaxExtraInfo struct {
	UsageCharacters int64 `json:"usage_characters"`
}

type MiniMaxBaseResp struct {
	StatusCode int64  `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

func isRemoteAudioURL(audio string) bool {
	return strings.HasPrefix(audio, "https://") || strings.HasPrefix(audio, "http://")
}

func writeRemoteTTSAudio(c *gin.Context, audioURL string) *types.NewAPIError {
	resp, fetchErr := service.DoDownloadRequest(audioURL, "minimax-tts")
	if fetchErr != nil {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("failed to fetch minimax audio: %w", fetchErr),
			types.ErrorCodeBadResponse,
			http.StatusBadGateway,
		)
	}
	defer service.CloseResponseBodyGracefully(resp)

	if resp.StatusCode != http.StatusOK {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("minimax audio fetch returned status %d", resp.StatusCode),
			types.ErrorCodeBadResponse,
			http.StatusBadGateway,
		)
	}

	limited := io.LimitReader(resp.Body, maxRemoteTTSAudioBytes+1)
	audioData, readErr := io.ReadAll(limited)
	if readErr != nil {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("failed to read minimax audio: %w", readErr),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusBadGateway,
		)
	}
	if len(audioData) > maxRemoteTTSAudioBytes {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("minimax audio exceeded %d bytes", maxRemoteTTSAudioBytes),
			types.ErrorCodeBadResponse,
			http.StatusBadGateway,
		)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Data(http.StatusOK, contentType, audioData)
	return nil
}

func getContentTypeByFormat(format string) string {
	contentTypeMap := map[string]string{
		"mp3":  "audio/mpeg",
		"wav":  "audio/wav",
		"flac": "audio/flac",
		"aac":  "audio/aac",
		"pcm":  "audio/pcm",
	}
	if ct, ok := contentTypeMap[format]; ok {
		return ct
	}
	return "audio/mpeg" // default to mp3
}

func handleTTSResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	body, readErr := common.ReadResponseBody(resp)
	if readErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to read minimax response: %w", readErr),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusInternalServerError,
		)
	}
	defer resp.Body.Close()

	// Parse response
	var minimaxResp MiniMaxTTSResponse
	if unmarshalErr := json.Unmarshal(body, &minimaxResp); unmarshalErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to unmarshal minimax TTS response: %w", unmarshalErr),
			types.ErrorCodeBadResponseBody,
			http.StatusInternalServerError,
		)
	}

	// Check base_resp status code
	if minimaxResp.BaseResp.StatusCode != 0 {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("minimax TTS error: %d - %s", minimaxResp.BaseResp.StatusCode, minimaxResp.BaseResp.StatusMsg),
			types.ErrorCodeBadResponse,
			http.StatusBadRequest,
		)
	}

	// Check if we have audio data
	if minimaxResp.Data.Audio == "" {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("no audio data in minimax TTS response"),
			types.ErrorCodeBadResponse,
			http.StatusBadRequest,
		)
	}

	if isRemoteAudioURL(minimaxResp.Data.Audio) {
		if fetchErr := writeRemoteTTSAudio(c, minimaxResp.Data.Audio); fetchErr != nil {
			return nil, fetchErr
		}
	} else {
		// Handle hex-encoded audio data
		audioData, decodeErr := hex.DecodeString(minimaxResp.Data.Audio)
		if decodeErr != nil {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("failed to decode hex audio data: %w", decodeErr),
				types.ErrorCodeBadResponse,
				http.StatusInternalServerError,
			)
		}

		// Determine content type - default to mp3
		contentType := "audio/mpeg"

		c.Data(http.StatusOK, contentType, audioData)
	}

	usage = &dto.Usage{
		PromptTokens:     info.GetEstimatePromptTokens(),
		CompletionTokens: 0,
		TotalTokens:      int(minimaxResp.ExtraInfo.UsageCharacters),
	}

	return usage, nil
}

func handleChatCompletionResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	body, readErr := common.ReadResponseBody(resp)
	if readErr != nil {
		return nil, types.NewErrorWithStatusCode(
			errors.New("failed to read minimax response"),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusInternalServerError,
		)
	}
	defer resp.Body.Close()

	// Set response headers
	for key, values := range resp.Header {
		if !service.ShouldCopyUpstreamHeader(c, key, values) {
			continue
		}
		for _, value := range values {
			c.Header(key, value)
		}
	}

	c.Data(resp.StatusCode, "application/json", body)
	return nil, nil
}
