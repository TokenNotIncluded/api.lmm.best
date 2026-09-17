package service

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSessionMetadataUTF8ByteBudget(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		limit       int
		want        string
	}{
		{"ascii", "  Mozilla/5.0  ", 512, "Mozilla/5.0"},
		{"ascii-boundary", strings.Repeat("a", 513), 512, strings.Repeat("a", 512)},
		{"chinese-boundary", strings.Repeat("中", 171), 512, strings.Repeat("中", 170)},
		{"mixed-boundary", strings.Repeat("a", 511) + "中", 512, strings.Repeat("a", 511)},
		{"emoji-boundary", strings.Repeat("a", 510) + "😀", 512, strings.Repeat("a", 510)},
		{"exact-emoji", strings.Repeat("😀", 128), 512, strings.Repeat("😀", 128)},
		{"combining-boundary", "e\u0301", 2, "e"},
		{"unicode-space", "\u2003中\u2003", 64, "中"},
		{"invalid-byte", "a\xffb", 64, "a\ufffdb"},
		{"incomplete-rune", "a\xe4\xb8", 64, "a\ufffd\ufffd"},
		{"nul", "a\x00b", 64, "a\ufffdb"},
		{"replacement-budget", "a\x00b", 3, "a"},
		{"no-room-for-rune", "中", 2, ""},
		{"ip-budget", strings.Repeat("a", 63) + "中", 64, strings.Repeat("a", 63)},
		{"ipv6", " 2001:db8::1 ", 64, "2001:db8::1"},
		{"empty", "", 512, ""},
		{"zero", "x", 0, ""},
		{"negative", "x", -1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateAuthMetadata(tc.input, tc.limit)
			if got != tc.want || !utf8.ValidString(got) || strings.ContainsRune(got, 0) {
				t.Fatalf("got %q (%d bytes), want %q", got, len(got), tc.want)
			}
		})
	}
}

func FuzzSessionMetadataUTF8Budget(f *testing.F) {
	for _, input := range []string{"", "Mozilla/5.0", "中😀", "a\x00b", "\xff\xfe", strings.Repeat("中", 171)} {
		f.Add(input, uint16(512))
	}
	f.Fuzz(func(t *testing.T, input string, rawLimit uint16) {
		limit := int(rawLimit % 1025)
		got := truncateAuthMetadata(input, limit)
		if len(got) > limit || !utf8.ValidString(got) || strings.ContainsRune(got, 0) {
			t.Fatalf("invalid metadata: limit=%d bytes=%d valid=%t", limit, len(got), utf8.ValidString(got))
		}
	})
}
