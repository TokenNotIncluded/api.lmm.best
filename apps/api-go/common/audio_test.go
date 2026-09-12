package common

import (
	"bytes"
	"context"
	"testing"
)

func TestGetAudioDurationNormalizesExtensionCase(t *testing.T) {
	tests := []struct {
		name  string
		lower string
		mixed string
	}{
		{name: "wav upper", lower: ".wav", mixed: ".WAV"},
		{name: "wav mixed", lower: ".wav", mixed: ".WaV"},
		{name: "mp3 upper", lower: ".mp3", mixed: ".MP3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lowerDuration, lowerErr := GetAudioDuration(context.Background(), bytes.NewReader(nil), tt.lower)
			mixedDuration, mixedErr := GetAudioDuration(context.Background(), bytes.NewReader(nil), tt.mixed)

			if lowerDuration != mixedDuration {
				t.Fatalf("duration mismatch for %q and %q: %v != %v", tt.lower, tt.mixed, lowerDuration, mixedDuration)
			}
			if (lowerErr == nil) != (mixedErr == nil) {
				t.Fatalf("error presence mismatch for %q and %q: %v != %v", tt.lower, tt.mixed, lowerErr, mixedErr)
			}
			if lowerErr != nil && lowerErr.Error() != mixedErr.Error() {
				t.Fatalf("error mismatch for %q and %q: %q != %q", tt.lower, tt.mixed, lowerErr, mixedErr)
			}
		})
	}
}

func TestGetAudioDurationRejectsUnknownExtension(t *testing.T) {
	_, err := GetAudioDuration(context.Background(), bytes.NewReader(nil), ".UNKNOWN")
	if err == nil || err.Error() != "unsupported audio format: .unknown" {
		t.Fatalf("unexpected error: %v", err)
	}
}
