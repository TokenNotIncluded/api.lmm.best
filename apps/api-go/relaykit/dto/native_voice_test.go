package dto

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeVoiceStartMappingPreservesClientConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		kind  NativeVoiceKind
		raw   string
		query string
		model string
	}{
		{
			name:  "Live client delegation",
			kind:  NativeVoiceLive,
			raw:   `{"type":"session.start","event_id":"first","session":{"model":"customer-live","instructions":"保留指令","delegation":{"type":"client"},"audio":{"format":{"type":"audio/pcm","rate":24000},"output":{"voice":"marin"}},"future_setting":{"count":9007199254740993,"values":[true,null,"保留"]}}}`,
			model: "gpt-live-1",
		},
		{
			name:  "streaming transcription",
			kind:  NativeVoiceTranscription,
			raw:   `{"type":"session.update","session":{"type":"transcription","audio":{"input":{"transcription":{"model":"customer-asr","languages":["zh-tw"],"prompt":"billing"},"noise_reduction":null}},"future_setting":{"count":9007199254740993,"values":[true,null,"保留"]}}}`,
			model: "gpt-live-transcribe",
		},
		{
			name:  "Whisper explicit PCM and VAD",
			kind:  NativeVoiceTranscription,
			raw:   `{"type":"session.update","session":{"type":"transcription","audio":{"input":{"transcription":{"model":"customer-whisper","delay":"low"},"format":{"type":"audio/pcm","rate":24000,"future":true},"turn_detection":null}},"future_setting":{"count":9007199254740993,"values":[true,null,"保留"]}}}`,
			model: "gpt-realtime-whisper",
		},
		{
			name:  "translation initial configuration",
			kind:  NativeVoiceTranslation,
			raw:   `{"type":"session.update","session":{"audio":{"input":{"transcription":null},"output":{"language":"es"}},"future_setting":{"count":9007199254740993,"values":[true,null,"保留"]}}}`,
			query: "customer-translate",
			model: "gpt-realtime-translate",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte(test.raw)
			start, err := ParseNativeVoiceStart(test.kind, raw, test.query)
			if err != nil {
				t.Fatal(err)
			}
			if start.AudioRate != 24000 || start.Kind != test.kind || !bytes.Equal(start.Raw, raw) {
				t.Fatalf("unexpected session start: %+v", start)
			}
			raw[0] = '['
			if start.Raw[0] != '{' {
				t.Fatal("session Raw aliases the caller's mutable buffer")
			}
			rewritten, err := RewriteNativeVoiceStart(start, test.model)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(rewritten, []byte(`"count":9007199254740993`)) || !bytes.Contains(rewritten, []byte(`"values":[true,null,"保留"]`)) {
				t.Fatalf("unknown fields or exact number were lost: %s", rewritten)
			}
			event, err := nativeVoiceObject(rewritten)
			if err != nil {
				t.Fatal(err)
			}
			if err = nativeVoiceLockedModels(event, test.model); err != nil {
				t.Fatalf("upstream model was not rewritten: %s", rewritten)
			}
			if test.kind == NativeVoiceTranscription {
				input, err := nativeVoiceTranscriptionInput(event["session"].(map[string]any), true)
				if err != nil {
					t.Fatal(err)
				}
				if detection, exists := input["turn_detection"]; !exists || detection != nil {
					t.Fatal("rewritten ASR session must explicitly disable VAD")
				}
				format := input["format"].(map[string]any)
				if format["type"] != "audio/pcm" || format["rate"] != json.Number("24000") {
					t.Fatalf("unexpected PCM format: %v", format)
				}
			}
		})
	}
}

func TestNativeVoiceStartRejectsAmbiguousOrUnaccountedConfigurations(t *testing.T) {
	tests := []struct {
		name  string
		kind  NativeVoiceKind
		raw   string
		query string
	}{
		{"missing model", NativeVoiceLive, `{"type":"session.start","session":{}}`, ""},
		{"null model", NativeVoiceLive, `{"type":"session.start","session":{"model":null}}`, ""},
		{"whitespace model", NativeVoiceLive, `{"type":"session.start","session":{"model":" gpt-live-1"}}`, ""},
		{"query model on Live", NativeVoiceLive, `{"type":"session.start","session":{"model":"gpt-live-1"}}`, "gpt-live-1"},
		{"query model on ASR", NativeVoiceTranscription, `{"type":"session.update","session":{}}`, "gpt-live-transcribe"},
		{"wrong first event", NativeVoiceLive, `{"type":"session.update","session":{"model":"gpt-live-1"}}`, ""},
		{"wrong ASR type", NativeVoiceTranscription, `{"type":"session.update","session":{"type":"realtime","audio":{"input":{"transcription":{"model":"gpt-live-transcribe"}}}}}`, ""},
		{"Responses delegation", NativeVoiceLive, `{"type":"session.start","session":{"model":"gpt-live-1","delegation":{"type":"responses","responses":{"model":"gpt-live-1"}}}}`, ""},
		{"hidden Responses configuration", NativeVoiceLive, `{"type":"session.start","session":{"model":"gpt-live-1","delegation":{"type":"client","responses":{}}}}`, ""},
		{"extra ASR backend", NativeVoiceLive, `{"type":"session.start","session":{"model":"gpt-live-1","audio":{"input":{"transcription":{"model":"gpt-live-1"}}}}}`, ""},
		{"PCMU Live format", NativeVoiceLive, `{"type":"session.start","session":{"model":"gpt-live-1","audio":{"format":{"type":"audio/pcmu"}}}}`, ""},
		{"different Live rate", NativeVoiceLive, `{"type":"session.start","session":{"model":"gpt-live-1","audio":{"format":{"type":"audio/pcm","rate":16000}}}}`, ""},
		{"string sample rate", NativeVoiceLive, `{"type":"session.start","session":{"model":"gpt-live-1","audio":{"format":{"rate":"24000"}}}}`, ""},
		{"legacy ASR config", NativeVoiceTranscription, `{"type":"session.update","session":{"type":"transcription","input_audio_transcription":{"model":"gpt-live-transcribe"},"audio":{"input":{"transcription":{"model":"gpt-live-transcribe"}}}}}`, ""},
		{"ASR server VAD", NativeVoiceTranscription, `{"type":"session.update","session":{"type":"transcription","audio":{"input":{"transcription":{"model":"gpt-live-transcribe"},"turn_detection":{"type":"server_vad"}}}}}`, ""},
		{"ASR output audio", NativeVoiceTranscription, `{"type":"session.update","session":{"type":"transcription","audio":{"input":{"transcription":{"model":"gpt-live-transcribe"}},"output":{"voice":"marin"}}}}`, ""},
		{"null ASR format", NativeVoiceTranscription, `{"type":"session.update","session":{"type":"transcription","audio":{"input":{"transcription":{"model":"gpt-live-transcribe"},"format":null}}}}`, ""},
		{"nested duplicate", NativeVoiceLive, `{"type":"session.start","session":{"model":"gpt-live-1","future":[{"x":1,"x":2}]}}`, ""},
		{"duplicate escaped model", NativeVoiceLive, `{"type":"session.start","session":{"model":"gpt-live-1","\u006dodel":"gpt-live-1"}}`, ""},
		{"case variant model", NativeVoiceLive, `{"type":"session.start","session":{"model":"gpt-live-1","future":{"Model":"gpt-live-1"}}}`, ""},
		{"conflicting nested model", NativeVoiceLive, `{"type":"session.start","session":{"model":"gpt-live-1","future":{"model":"gpt-6-astra"}}}`, ""},
		{"trailing object", NativeVoiceLive, `{"type":"session.start","session":{"model":"gpt-live-1"}}{}`, ""},
		{"trailing delimiter", NativeVoiceLive, `{"type":"session.start","session":{"model":"gpt-live-1"}}]`, ""},
		{"JSON array", NativeVoiceLive, `[]`, ""},
		{"translation missing query", NativeVoiceTranslation, ``, ""},
		{"translation ASR", NativeVoiceTranslation, `{"type":"session.update","session":{"audio":{"input":{"transcription":{"model":"gpt-realtime-translate"}}}}}`, "gpt-realtime-translate"},
		{"unknown protocol", NativeVoiceKind("other"), `{}`, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseNativeVoiceStart(test.kind, []byte(test.raw), test.query); err == nil {
				t.Fatal("unsafe session configuration was accepted")
			}
		})
	}
}

func TestNativeVoiceRewriteRequiresMappedModelFamilyAndFrozenOriginal(t *testing.T) {
	live, err := ParseNativeVoiceStart(NativeVoiceLive, []byte(`{"type":"session.start","session":{"model":"alias"}}`), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"", "gpt-realtime", "gpt-live-transcribe", "gpt-live-1 "} {
		if _, err := RewriteNativeVoiceStart(live, model); err == nil {
			t.Fatalf("wrong model family %q accepted", model)
		}
	}
	live.Raw = []byte(`{"type":"session.start","session":{"model":"different-alias"}}`)
	if _, err := RewriteNativeVoiceStart(live, "gpt-live-1"); err == nil {
		t.Fatal("mutated session model accepted")
	}
	if _, err := RewriteNativeVoiceStart(nil, "gpt-live-1"); err == nil {
		t.Fatal("nil session accepted")
	}
	translation, err := ParseNativeVoiceStart(NativeVoiceTranslation, nil, "translate-alias")
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := RewriteNativeVoiceStart(translation, "gpt-realtime-translate")
	if err != nil || len(rewritten) != 0 {
		t.Fatalf("query-selected translation should not synthesize a first event: %s, %v", rewritten, err)
	}
	if _, err = RewriteNativeVoiceStart(translation, "gpt-live-1"); err == nil {
		t.Fatal("translation mapped to wrong protocol")
	}
}

func TestNativeVoiceClientEventLocksModelAndBillingScope(t *testing.T) {
	validAudio := base64.StdEncoding.EncodeToString([]byte{0, 0, 1, 0})
	tests := []struct {
		name  string
		kind  NativeVoiceKind
		model string
		raw   string
		valid bool
	}{
		{"Live audio", NativeVoiceLive, "gpt-live-1", `{"type":"session.input_audio.append","audio":"` + validAudio + `"}`, true},
		{"Live client delegation", NativeVoiceLive, "gpt-live-1", `{"type":"session.update","session":{"delegation":{"type":"client"}}}`, true},
		{"Live null delegation", NativeVoiceLive, "gpt-live-1", `{"type":"session.update","session":{"delegation":null}}`, true},
		{"Live close", NativeVoiceLive, "gpt-live-1", `{"type":"session.close"}`, true},
		{"ASR audio", NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"input_audio_buffer.append","audio":"` + validAudio + `"}`, true},
		{"ASR commit", NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"input_audio_buffer.commit"}`, true},
		{"ASR clear", NativeVoiceTranscription, "gpt-realtime-whisper", `{"type":"input_audio_buffer.clear"}`, true},
		{"ASR prompt update", NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"session.update","session":{"audio":{"input":{"transcription":{"model":"gpt-live-transcribe","prompt":"billing"}}}}}`, true},
		{"ASR sparse update", NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"session.update","session":{"include":["item.input_audio_transcription.logprobs"]}}`, true},
		{"translation audio", NativeVoiceTranslation, "gpt-realtime-translate", `{"type":"session.input_audio_buffer.append","audio":"` + validAudio + `"}`, true},
		{"translation language", NativeVoiceTranslation, "gpt-realtime-translate", `{"type":"session.update","session":{"audio":{"input":{"transcription":null,"noise_reduction":null},"output":{"language":"es"}}}}`, true},
		{"translation close", NativeVoiceTranslation, "gpt-realtime-translate", `{"type":"session.close"}`, true},
		{"Live model change", NativeVoiceLive, "gpt-live-1", `{"type":"session.update","session":{"model":"gpt-6-astra"}}`, false},
		{"Live repeat start", NativeVoiceLive, "gpt-live-1", `{"type":"session.start","session":{"model":"gpt-live-1"}}`, false},
		{"Live Responses", NativeVoiceLive, "gpt-live-1", `{"type":"response.create","response":{"model":"gpt-live-1"}}`, false},
		{"Live Responses update", NativeVoiceLive, "gpt-live-1", `{"type":"session.update","session":{"delegation":{"type":"responses"}}}`, false},
		{"Live ASR update", NativeVoiceLive, "gpt-live-1", `{"type":"session.update","session":{"audio":{"input":{"transcription":{"model":"gpt-live-1"}}}}}`, false},
		{"ASR model change", NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"session.update","session":{"audio":{"input":{"transcription":{"model":"gpt-realtime-whisper"}}}}}`, false},
		{"ASR model null", NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"session.update","session":{"audio":{"input":{"transcription":{"model":null}}}}}`, false},
		{"ASR type change", NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"session.update","session":{"type":"realtime"}}`, false},
		{"ASR transcription disable", NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"session.update","session":{"audio":{"input":{"transcription":null}}}}`, false},
		{"ASR VAD update", NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"session.update","session":{"audio":{"input":{"turn_detection":{"type":"server_vad"}}}}}`, false},
		{"ASR rate update", NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"session.update","session":{"audio":{"input":{"format":{"rate":48000}}}}}`, false},
		{"ASR Responses", NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"response.create"}`, false},
		{"translation extra ASR", NativeVoiceTranslation, "gpt-realtime-translate", `{"type":"session.update","session":{"audio":{"input":{"transcription":{"model":"gpt-realtime-translate"}}}}}`, false},
		{"translation invalid audio type", NativeVoiceTranslation, "gpt-realtime-translate", `{"type":"session.update","session":{"audio":[]}}`, false},
		{"translation wrong event", NativeVoiceTranslation, "gpt-realtime-translate", `{"type":"input_audio_buffer.append","audio":"` + validAudio + `"}`, false},
		{"translation changed type", NativeVoiceTranslation, "gpt-realtime-translate", `{"type":"session.update","session":{"type":"realtime"}}`, false},
		{"translation invalid base64", NativeVoiceTranslation, "gpt-realtime-translate", `{"type":"session.input_audio_buffer.append","audio":"not base64"}`, false},
		{"translation odd bytes", NativeVoiceTranslation, "gpt-realtime-translate", `{"type":"session.input_audio_buffer.append","audio":"AA=="}`, false},
		{"translation empty audio", NativeVoiceTranslation, "gpt-realtime-translate", `{"type":"session.input_audio_buffer.append","audio":""}`, false},
		{"audio numeric", NativeVoiceLive, "gpt-live-1", `{"type":"session.input_audio.append","audio":123}`, false},
		{"nested model spoof", NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"input_audio_buffer.commit","future":[{"model":"gpt-live-1"}]}`, false},
		{"model wrong case", NativeVoiceLive, "gpt-live-1", `{"type":"session.close","future":{"MODEL":"gpt-live-1"}}`, false},
		{"duplicate event type", NativeVoiceLive, "gpt-live-1", `{"type":"session.close","type":"session.update"}`, false},
		{"trailing junk", NativeVoiceLive, "gpt-live-1", `{"type":"session.close"}}`, false},
		{"missing type", NativeVoiceLive, "gpt-live-1", `{}`, false},
		{"unknown kind", NativeVoiceKind("other"), "gpt-live-1", `{"type":"session.close"}`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateNativeVoiceClientEvent(test.kind, []byte(test.raw), test.model)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v; validator returned %v", test.valid, err)
			}
		})
	}
}

func TestNativeVoiceJSONRejectsExcessiveNesting(t *testing.T) {
	raw := `{"type":"session.close","future":` + strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65) + `}`
	if err := ValidateNativeVoiceClientEvent(NativeVoiceLive, []byte(raw), "gpt-live-1"); err == nil {
		t.Fatal("excessively nested event accepted")
	}
}

func TestNativeVoiceClientEventRejectsAlternativeAudioInjection(t *testing.T) {
	tests := []struct {
		kind  NativeVoiceKind
		model string
		raw   string
	}{
		{NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"conversation.item.create","item":{"type":"message","role":"user","content":[{"type":"input_audio","audio":"AAAA"}]}}`},
		{NativeVoiceTranscription, "gpt-realtime-whisper", `{"type":"conversation.item.delete","item_id":"item_123"}`},
		{NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"session.input_audio.append","audio":"AAAA"}`},
		{NativeVoiceTranscription, "gpt-live-transcribe", `{"type":"input_audio_buffer.future","audio":"AAAA"}`},
		{NativeVoiceLive, "gpt-live-1", `{"type":"conversation.item.create","item":{"content":[{"type":"input_audio","audio":"AAAA"}]}}`},
		{NativeVoiceLive, "gpt-live-1", `{"type":"input_audio_buffer.append","audio":"AAAA"}`},
		{NativeVoiceLive, "gpt-live-1", `{"type":"session.future","audio":"AAAA"}`},
	}
	for _, test := range tests {
		if err := ValidateNativeVoiceClientEvent(test.kind, []byte(test.raw), test.model); err == nil {
			t.Fatalf("unmetered alternative event accepted for %s: %s", test.kind, test.raw)
		}
	}
	for _, eventType := range []string{"session.input_audio.mute", "session.input_audio.unmute", "session.instructions.append", "session.thinking.append", "session.commentary.append"} {
		raw := `{"type":"` + eventType + `","future":{"keep":true},"content":"应用提供上下文","delegation_id":null}`
		if err := ValidateNativeVoiceClientEvent(NativeVoiceLive, []byte(raw), "gpt-live-1"); err != nil {
			t.Fatalf("supported Live control %s rejected: %v", eventType, err)
		}
	}
}

func TestNativeVoiceJSONValidatesHTTPTransportWithoutLosingUnknownFields(t *testing.T) {
	valid := `{"session":{"model":"live-alias","future":{"value":1}},"transport":{"type":"webrtc","sdp":"v=0\r\n"}}`
	if err := ValidateNativeVoiceJSON([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"session":{"model":"live-alias"},"transport":{"sdp":"first","sdp":"second"}}`,
		`{"session":{"model":"live-alias","Model":"gpt-live-1"},"transport":{"sdp":"v=0"}}`,
		`{"session":{"model":"live-alias"},"transport":{"future":[{"key":1,"key":2}]}}`,
		valid + `{}`,
		`[]`,
	} {
		if err := ValidateNativeVoiceJSON([]byte(raw)); err == nil {
			t.Fatalf("ambiguous transport JSON accepted: %s", raw)
		}
	}
}
