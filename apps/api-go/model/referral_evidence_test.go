package model

import (
	"strings"
	"testing"
)

func TestReferralEvidenceUnicodeBoundaries(t *testing.T) {
	for _, text := range []string{"a", "证", "é", "🙂", "\U00020000"} {
		for _, count := range []int{1, 250, 334, 999, 1000, 1001} {
			value := strings.Repeat(text, count)
			got, valid := normalizeReferralEvidence(value)
			if got != value || valid != (count <= 1000) {
				t.Fatalf("rune=%q count=%d bytes=%d valid=%v", text, count, len(value), valid)
			}
		}
	}
}

func TestReferralEvidenceNormalizesWithoutChangingAuditContent(t *testing.T) {
	for _, value := range []string{"  已核实\n保留原始记录  ", "\u0085\u00a0证据\u3000", "e\u0301", "\ufeffevidence"} {
		got, valid := normalizeReferralEvidence(value)
		if !valid || got != strings.TrimSpace(value) {
			t.Fatalf("normalization changed content: %q => %q", value, got)
		}
	}
	// A combining sequence is two code points, as it is in PostgreSQL.
	_, valid := normalizeReferralEvidence(strings.Repeat("e\u0301", 500))
	if !valid {
		t.Fatal("1000 code points rejected")
	}
	_, valid = normalizeReferralEvidence(strings.Repeat("e\u0301", 501))
	if valid {
		t.Fatal("1002 code points accepted")
	}
}

func TestReferralEvidenceRejectsEmptyNULAndInvalidUTF8(t *testing.T) {
	for _, value := range []string{"", " \t\r\n", "\u0085\u00a0\u3000", "before\x00after", string([]byte{0xff}), "bad\xc0\xaf"} {
		if _, valid := normalizeReferralEvidence(value); valid {
			t.Fatalf("invalid evidence accepted: %q", value)
		}
	}
}
