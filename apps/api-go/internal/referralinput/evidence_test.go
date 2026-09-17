package referralinput

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestEvidenceSharedFrontendContract(t *testing.T) {
	data, err := os.ReadFile("../../../../contracts/referral-evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name   string `json:"name"`
		Text   string `json:"text"`
		Repeat int    `json:"repeat"`
		Bytes  int    `json:"bytes"`
		Valid  bool   `json:"valid"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) < 14 {
		t.Fatal("incomplete frontend/backend evidence contract")
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			input := strings.Repeat(f.Text, f.Repeat)
			expected := strings.TrimSpace(input)
			if len(expected) != f.Bytes {
				t.Fatalf("bytes=%d want=%d", len(expected), f.Bytes)
			}
			got, err := NormalizeEvidence(input)
			if f.Valid {
				if err != nil || got != expected {
					t.Fatalf("normalization mismatch: %v", err)
				}
			} else if !errors.Is(err, ErrInvalidEvidence) || got != "" {
				t.Fatal("invalid evidence was accepted or partially returned")
			}
		})
	}
}

func TestEvidenceRejectsInvalidUTF8AndDoesNotTruncate(t *testing.T) {
	for _, input := range []string{string([]byte{0xff}), "ok" + string([]byte{0xed, 0xa0, 0x80}), strings.Repeat("证", 334)} {
		got, err := NormalizeEvidence(input)
		if got != "" || !errors.Is(err, ErrInvalidEvidence) {
			t.Fatal("invalid evidence accepted")
		}
	}
	exact := strings.Repeat("证", 333) + "a"
	got, err := NormalizeEvidence("\\t" + exact)
	if err == nil || got != "" {
		t.Fatal("literal backslash is not whitespace")
	}
	got, err = NormalizeEvidence("\t" + exact + "\n")
	if err != nil || got != exact {
		t.Fatal("trimmed exact byte boundary rejected")
	}
}
