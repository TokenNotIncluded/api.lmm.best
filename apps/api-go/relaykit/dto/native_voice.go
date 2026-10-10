package dto

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type NativeVoiceKind string

const (
	NativeVoiceLive          NativeVoiceKind = "live"
	NativeVoiceTranscription NativeVoiceKind = "transcription"
	NativeVoiceTranslation   NativeVoiceKind = "translation"
)

// NativeVoiceStart keeps the client event intact until channel model mapping has
// completed. Model is the public name; RewriteNativeVoiceStart validates and
// inserts the actual upstream model without discarding unknown configuration.
type NativeVoiceStart struct {
	Kind      NativeVoiceKind
	Model     string
	Raw       []byte
	AudioRate int
}

// ValidateNativeVoiceJSON rejects ambiguous JSON before HTTP transport fields
// and session configuration are decoded separately by the WebRTC entry point.
func ValidateNativeVoiceJSON(raw []byte) error {
	_, err := nativeVoiceObject(raw)
	return err
}

func ParseNativeVoiceStart(kind NativeVoiceKind, raw []byte, queryModel string) (*NativeVoiceStart, error) {
	start := &NativeVoiceStart{Kind: kind, Raw: bytes.Clone(raw), AudioRate: 24000}
	if kind == NativeVoiceTranslation {
		if err := nativeVoiceModelName(queryModel); err != nil {
			return nil, err
		}
		start.Model = queryModel
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := ValidateNativeVoiceClientEvent(kind, raw, queryModel); err != nil {
				return nil, err
			}
		}
		return start, nil
	}
	if kind != NativeVoiceLive && kind != NativeVoiceTranscription {
		return nil, errors.New("unsupported native voice protocol")
	}
	if queryModel != "" {
		return nil, errors.New("this voice protocol selects its model in the first session event")
	}
	event, err := nativeVoiceObject(raw)
	if err != nil {
		return nil, err
	}
	session, err := nativeVoiceChild(event, "session", true)
	if err != nil {
		return nil, err
	}
	if kind == NativeVoiceLive {
		if event["type"] != "session.start" {
			return nil, errors.New("Live requires session.start as its first event")
		}
		start.Model, _ = session["model"].(string)
		if err = nativeVoiceLiveConfig(session); err != nil {
			return nil, err
		}
	} else {
		if event["type"] != "session.update" || session["type"] != "transcription" {
			return nil, errors.New("transcription requires session.update with session.type transcription")
		}
		input, inputErr := nativeVoiceTranscriptionInput(session, true)
		if inputErr != nil {
			return nil, inputErr
		}
		transcription, transcriptionErr := nativeVoiceChild(input, "transcription", true)
		if transcriptionErr != nil {
			return nil, transcriptionErr
		}
		start.Model, _ = transcription["model"].(string)
	}
	if err = nativeVoiceModelName(start.Model); err != nil {
		return nil, err
	}
	if err = nativeVoiceLockedModels(event, start.Model); err != nil {
		return nil, err
	}
	return start, nil
}

func RewriteNativeVoiceStart(start *NativeVoiceStart, mappedModel string) ([]byte, error) {
	if start == nil {
		return nil, errors.New("voice session configuration is required")
	}
	if !nativeVoiceOfficialModel(start.Kind, mappedModel) {
		return nil, errors.New("mapped model does not support this native voice protocol")
	}
	queryModel := ""
	if start.Kind == NativeVoiceTranslation {
		queryModel = start.Model
	}
	parsed, err := ParseNativeVoiceStart(start.Kind, start.Raw, queryModel)
	if err != nil {
		return nil, err
	}
	if parsed.Model != start.Model {
		return nil, errors.New("voice session model changed after authorization")
	}
	if len(bytes.TrimSpace(start.Raw)) == 0 {
		return bytes.Clone(start.Raw), nil
	}
	event, err := nativeVoiceObject(start.Raw)
	if err != nil {
		return nil, err
	}
	nativeVoiceReplaceModels(event, mappedModel)
	if start.Kind == NativeVoiceTranscription {
		session := event["session"].(map[string]any)
		input, _ := nativeVoiceTranscriptionInput(session, true)
		// Both supported streaming ASR models require manual commits. Explicitly
		// disable VAD so an omitted field cannot inherit an incompatible default.
		input["turn_detection"] = nil
		format, _ := input["format"].(map[string]any)
		if format == nil {
			format = make(map[string]any)
			input["format"] = format
		}
		format["type"] = "audio/pcm"
		format["rate"] = 24000
	}
	return json.Marshal(event)
}

// ValidateNativeVoiceClientEvent prevents a persistent session from switching
// models or enabling a separately billed backend after its price is frozen.
func ValidateNativeVoiceClientEvent(kind NativeVoiceKind, raw []byte, lockedUpstreamModel string) error {
	if err := nativeVoiceModelName(lockedUpstreamModel); err != nil {
		return err
	}
	event, err := nativeVoiceObject(raw)
	if err != nil {
		return err
	}
	eventType, ok := event["type"].(string)
	if !ok || eventType == "" {
		return errors.New("voice event type is required")
	}
	if err = nativeVoiceLockedModels(event, lockedUpstreamModel); err != nil {
		return err
	}
	if eventType == "session.start" {
		return errors.New("a voice session may only be started once")
	}
	if strings.HasPrefix(eventType, "response.") {
		return errors.New("native duration voice sessions do not support Responses delegation")
	}
	switch kind {
	case NativeVoiceLive:
		switch eventType {
		case "session.update", "session.input_audio.append", "session.input_audio.mute", "session.input_audio.unmute",
			"session.instructions.append", "session.thinking.append", "session.commentary.append", "session.close":
		default:
			return errors.New("unsupported Live client event")
		}
		if eventType == "session.update" {
			session, childErr := nativeVoiceChild(event, "session", true)
			if childErr != nil {
				return childErr
			}
			if err = nativeVoiceLiveConfig(session); err != nil {
				return err
			}
		}
		if eventType == "session.input_audio.append" {
			return nativeVoicePCM(event["audio"])
		}
	case NativeVoiceTranscription:
		switch eventType {
		case "session.update", "input_audio_buffer.append", "input_audio_buffer.commit", "input_audio_buffer.clear":
		default:
			// Item creation can carry audio outside the metered append path.
			return errors.New("unsupported transcription client event")
		}
		if eventType == "session.update" {
			session, childErr := nativeVoiceChild(event, "session", true)
			if childErr != nil {
				return childErr
			}
			if sessionType, exists := session["type"]; exists && sessionType != "transcription" {
				return errors.New("transcription session type cannot change")
			}
			if _, err = nativeVoiceTranscriptionInput(session, false); err != nil {
				return err
			}
		}
		if eventType == "input_audio_buffer.append" {
			return nativeVoicePCM(event["audio"])
		}
	case NativeVoiceTranslation:
		switch eventType {
		case "session.update":
			session, childErr := nativeVoiceChild(event, "session", true)
			if childErr != nil {
				return childErr
			}
			if sessionType, exists := session["type"]; exists && sessionType != "translation" {
				return errors.New("translation session type cannot change")
			}
			if err = nativeVoiceNoTranscription(session); err != nil {
				return err
			}
			if audio, childErr := nativeVoiceChild(session, "audio", false); childErr != nil {
				return childErr
			} else if input, childErr := nativeVoiceChild(audio, "input", false); childErr != nil {
				return childErr
			} else if err = nativeVoicePCMFormat(input); err != nil {
				return err
			}
		case "session.input_audio_buffer.append":
			return nativeVoicePCM(event["audio"])
		case "session.close":
		default:
			return errors.New("unsupported translation client event")
		}
	default:
		return errors.New("unsupported native voice protocol")
	}
	return nil
}

func nativeVoiceModelName(model string) error {
	if model == "" || strings.TrimSpace(model) != model {
		return errors.New("voice model must be a nonempty string without surrounding whitespace")
	}
	return nil
}

func nativeVoiceOfficialModel(kind NativeVoiceKind, model string) bool {
	switch kind {
	case NativeVoiceLive:
		return model == "gpt-live-1"
	case NativeVoiceTranscription:
		return model == "gpt-live-transcribe" || model == "gpt-realtime-whisper"
	case NativeVoiceTranslation:
		return model == "gpt-realtime-translate"
	}
	return false
}

func nativeVoiceLiveConfig(session map[string]any) error {
	if delegation, exists := session["delegation"]; exists && delegation != nil {
		config, ok := delegation.(map[string]any)
		if !ok || len(config) != 1 || config["type"] != "client" {
			return errors.New("Live supports only null or client delegation; Responses delegation has separate billing")
		}
	}
	if err := nativeVoiceNoTranscription(session); err != nil {
		return err
	}
	audio, err := nativeVoiceChild(session, "audio", false)
	if err != nil {
		return err
	}
	return nativeVoicePCMFormat(audio)
}

func nativeVoiceNoTranscription(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if (key == "transcription" || key == "input_audio_transcription") && item != nil {
				return errors.New("an additional transcription backend is not supported by this session's billing")
			}
			if err := nativeVoiceNoTranscription(item); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range typed {
			if err := nativeVoiceNoTranscription(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func nativeVoiceTranscriptionInput(session map[string]any, required bool) (map[string]any, error) {
	for _, legacy := range []string{"input_audio_format", "input_audio_transcription", "turn_detection"} {
		if _, exists := session[legacy]; exists {
			return nil, errors.New("transcription requires the GA session.audio.input configuration")
		}
	}
	audio, err := nativeVoiceChild(session, "audio", required)
	if err != nil {
		return nil, err
	}
	if output, exists := audio["output"]; exists && output != nil {
		return nil, errors.New("a transcription session cannot enable output audio")
	}
	input, err := nativeVoiceChild(audio, "input", required)
	if err != nil {
		return nil, err
	}
	if err = nativeVoicePCMFormat(input); err != nil {
		return nil, err
	}
	if detection, exists := input["turn_detection"]; exists && detection != nil {
		return nil, errors.New("streaming transcription requires null turn_detection and manual audio commits")
	}
	if transcription, exists := input["transcription"]; exists {
		if _, ok := transcription.(map[string]any); !ok {
			return nil, errors.New("transcription configuration must remain an object")
		}
	}
	return input, nil
}

func nativeVoicePCMFormat(config map[string]any) error {
	format, err := nativeVoiceChild(config, "format", false)
	if err != nil {
		return err
	}
	if audioType, exists := format["type"]; exists && audioType != "audio/pcm" {
		return errors.New("native voice supports only 24 kHz PCM16 mono input")
	}
	if rate, exists := format["rate"]; exists {
		number, ok := rate.(json.Number)
		if !ok {
			return errors.New("native voice audio sample rate must be 24000")
		}
		value, err := number.Float64()
		if err != nil || value != 24000 {
			return errors.New("native voice audio sample rate must be 24000")
		}
	}
	return nil
}

func nativeVoicePCM(value any) error {
	audio, ok := value.(string)
	if !ok || audio == "" {
		return errors.New("audio must be nonempty base64 PCM16 data")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(audio)
	if err != nil || len(decoded) == 0 || len(decoded)%2 != 0 {
		return errors.New("audio must contain complete base64 PCM16 samples")
	}
	return nil
}

func nativeVoiceChild(object map[string]any, key string, required bool) (map[string]any, error) {
	value, exists := object[key]
	if !exists && !required {
		return nil, nil
	}
	child, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", key)
	}
	return child, nil
}

func nativeVoiceLockedModels(value any, locked string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if key == "model" && item != locked {
				return errors.New("voice session model cannot change after authorization")
			}
			if err := nativeVoiceLockedModels(item, locked); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range typed {
			if err := nativeVoiceLockedModels(item, locked); err != nil {
				return err
			}
		}
	}
	return nil
}

func nativeVoiceReplaceModels(value any, model string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if key == "model" {
				typed[key] = model
			} else {
				nativeVoiceReplaceModels(item, model)
			}
		}
	case []any:
		for _, item := range typed {
			nativeVoiceReplaceModels(item, model)
		}
	}
}

func nativeVoiceObject(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := nativeVoiceJSONValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, errors.New("unexpected trailing JSON data in voice event")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("voice event must be a JSON object")
	}
	return object, nil
}

func nativeVoiceJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("voice event JSON nesting is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := make(map[string]any)
		for decoder.More() {
			field, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := field.(string)
			if !ok {
				return nil, errors.New("invalid voice event JSON field")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("duplicate voice event JSON field %q", key)
			}
			if key != "model" && strings.EqualFold(key, "model") {
				return nil, errors.New("voice model field must use exact lowercase spelling")
			}
			object[key], err = nativeVoiceJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
			return nil, errors.New("invalid voice event JSON object")
		}
		return object, nil
	case json.Delim('['):
		array := make([]any, 0)
		for decoder.More() {
			item, err := nativeVoiceJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, item)
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim(']') {
			return nil, errors.New("invalid voice event JSON array")
		}
		return array, nil
	default:
		if _, delimiter := token.(json.Delim); delimiter {
			return nil, errors.New("invalid voice event JSON value")
		}
		return token, nil
	}
}
