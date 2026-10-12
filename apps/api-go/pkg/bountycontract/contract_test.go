// Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later
package bountycontract

import (
	"strings"
	"testing"
)

func TestKindCompatibility(t *testing.T) {
	for _, c := range []struct {
		kind, repo, want string
		valid            bool
	}{
		{"", "", General, true}, {"", " https://github.com/a/b ", OpenSource, true},
		{General, "", General, true}, {OpenSource, "", OpenSource, true},
		{" general ", "", General, true}, {"unknown", "", "", false},
	} {
		got, err := Kind(c.kind, c.repo)
		if got != c.want || (err == nil) != c.valid {
			t.Errorf("Kind(%q,%q) = %q, %v", c.kind, c.repo, got, err)
		}
	}
}

func TestPublisherType(t *testing.T) {
	for _, c := range []struct {
		input, want string
		valid       bool
	}{
		{"", Individual, true}, {"individual", Individual, true}, {" company ", Company, true}, {"admin", "", false},
	} {
		got, err := PublisherType(c.input)
		if got != c.want || (err == nil) != c.valid {
			t.Errorf("PublisherType(%q) = %q, %v", c.input, got, err)
		}
	}
}

func TestRecruitmentDeadline(t *testing.T) {
	const now int64 = 1800000000
	for _, c := range []struct {
		at   int64
		open bool
	}{
		{0, true}, {-1, false}, {now - 1, false}, {now, false}, {now + 1, true}, {MaxDeadline, true}, {MaxDeadline + 1, false},
	} {
		if RecruitmentOpen(c.at, now) != c.open {
			t.Errorf("RecruitmentOpen(%d)", c.at)
		}
	}
}

func TestUnicodeCharacterBounds(t *testing.T) {
	for _, c := range []struct {
		text     string
		min, max int
		valid    bool
	}{
		{" 中文设计 ", 4, 4, true}, {"🧑💻设计", 4, 4, true}, {"中文", 4, 120, false},
		{strings.Repeat("字", 120), 4, 120, true}, {strings.Repeat("字", 121), 4, 120, false},
		{"", 0, 2000, true}, {"\xff", 0, 2000, false},
	} {
		if ValidText(c.text, c.min, c.max) != c.valid {
			t.Errorf("ValidText(%q,%d,%d)", c.text, c.min, c.max)
		}
	}
}

func TestDeliveryURL(t *testing.T) {
	for _, c := range []struct {
		input string
		valid bool
	}{
		{"", true}, {" https://example.com/work?id=1#review ", true}, {"https://example.com/a b", true},
		{"http://example.com/work", false}, {"//example.com/work", false}, {"javascript:alert(1)", false},
		{"data:text/html,test", false}, {"https:///work", false}, {"https://user:password@example.com/work", false},
		{"https://example.com/\nwork", false}, {"https://example.com\\evil.test/work", false},
		{"https://example.com:65536/work", false}, {"https://example.com:abc/work", false},
		{"https://example.com/" + strings.Repeat("x", 2048), false},
		{"https://example.com/" + strings.Repeat("字", 250), false},
		{"https://[::1]/work", true},
	} {
		t.Run(c.input[:min(len(c.input), 60)], func(t *testing.T) {
			got, err := DeliveryURL(c.input)
			if (err == nil) != c.valid {
				t.Fatalf("got %q, %v", got, err)
			}
			if err == nil && len(got) > 2048 {
				t.Fatal("encoded URL exceeded storage limit")
			}
		})
	}
}

func TestGeneralDeliveryEvidence(t *testing.T) {
	for _, c := range []struct {
		link, note string
		valid      bool
	}{
		{"https://example.com/work", "", true}, {"", strings.Repeat("文", 20), true},
		{"", "", false}, {"", strings.Repeat("文", 19), false},
		{"javascript:alert(1)", strings.Repeat("文", 20), false},
		{"https://example.com/work", strings.Repeat("文", 2001), false},
		{"https://example.com/work", "\xff", false},
	} {
		_, _, err := ValidateDelivery(c.link, c.note)
		if (err == nil) != c.valid {
			t.Errorf("link=%q note length=%d: %v", c.link, len(c.note), err)
		}
	}
	_, note, err := ValidateDelivery("", "  "+strings.Repeat("文", 20)+"  ")
	if err != nil || note != strings.Repeat("文", 20) {
		t.Fatal("note was not normalized")
	}
}

func TestToolNames(t *testing.T) {
	for _, c := range []struct{ input, want string }{
		{"open_source_bounties.publish", "bounties.publish"}, {"open_source_bounties.list", "bounties.list"},
		{"bounties.publish", "bounties.publish"}, {"wallet.transfer", "wallet.transfer"},
		{"open_source_bounties_extra.list", "open_source_bounties_extra.list"},
	} {
		if CanonicalToolName(c.input) != c.want {
			t.Errorf("unexpected alias for %q", c.input)
		}
	}
}
