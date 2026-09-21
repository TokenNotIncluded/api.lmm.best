/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package console_setting

import (
	"strings"
	"testing"
)

func TestValidateAnnouncementsCountsUnicodeCharacters(t *testing.T) {
	valid := `[{"content":"` + strings.Repeat("公告", 250) + `","publishDate":"2026-08-14T00:00:00Z","type":"default"}]`
	if err := ValidateConsoleSettings(valid, "Announcements"); err != nil {
		t.Fatalf("500 Unicode characters should be accepted: %v", err)
	}

	tooLong := `[{"content":"` + strings.Repeat("公告", 251) + `","publishDate":"2026-08-14T00:00:00Z","type":"default"}]`
	if err := ValidateConsoleSettings(tooLong, "Announcements"); err == nil {
		t.Fatal("501 Unicode characters should be rejected")
	}
}

func TestValidateAnnouncementsCountsEmojiAsTwoBrowserCharacters(t *testing.T) {
	valid := `[{"content":"` + strings.Repeat("😀", 250) + `","publishDate":"2026-08-14T00:00:00Z","type":"default"}]`
	if err := ValidateConsoleSettings(valid, "Announcements"); err != nil {
		t.Fatalf("250 surrogate-pair emoji should fit the 500 UTF-16-unit limit: %v", err)
	}

	tooLong := `[{"content":"` + strings.Repeat("😀", 251) + `","publishDate":"2026-08-14T00:00:00Z","type":"default"}]`
	if err := ValidateConsoleSettings(tooLong, "Announcements"); err == nil {
		t.Fatal("251 surrogate-pair emoji should exceed the 500 UTF-16-unit limit")
	}
}

func TestMandatoryAnnouncementRequiresStableUniqueID(t *testing.T) {
	valid := `[{"id":1,"mandatory":true,"content":"Read this","publishDate":"2025-01-01T00:00:00Z"}]`
	if err := ValidateConsoleSettings(valid, "Announcements"); err != nil {
		t.Fatal(err)
	}
	invalid := []string{
		`[{"mandatory":true,"content":"Read this","publishDate":"2025-01-01T00:00:00Z"}]`,
		`[{"id":1,"mandatory":"yes","content":"Read this","publishDate":"2025-01-01T00:00:00Z"}]`,
		`[{"id":1.5,"mandatory":true,"content":"Read this","publishDate":"2025-01-01T00:00:00Z"}]`,
		`[{"id":1,"mandatory":true,"content":"One","publishDate":"2025-01-01T00:00:00Z"},{"id":1,"mandatory":true,"content":"Two","publishDate":"2025-01-02T00:00:00Z"}]`,
	}
	for _, input := range invalid {
		if err := ValidateConsoleSettings(input, "Announcements"); err == nil {
			t.Errorf("accepted invalid mandatory announcement: %s", input)
		}
	}
}

func TestAnnouncementAckRevisionMustBeAShortPrintableToken(t *testing.T) {
	valid := []string{
		`[{"id":1,"mandatory":true,"content":"Read this","publishDate":"2025-01-01T00:00:00Z","ackRevision":"2"}]`,
		`[{"id":1,"mandatory":true,"content":"Read this","publishDate":"2025-01-01T00:00:00Z","ackRevision":"v1.2-final"}]`,
		`[{"id":1,"mandatory":true,"content":"Read this","publishDate":"2025-01-01T00:00:00Z","ackRevision":""}]`,
	}
	for _, input := range valid {
		if err := ValidateConsoleSettings(input, "Announcements"); err != nil {
			t.Errorf("rejected a valid acknowledgement generation %s: %v", input, err)
		}
	}
	invalid := []string{
		`[{"id":1,"mandatory":true,"content":"Read this","publishDate":"2025-01-01T00:00:00Z","ackRevision":2}]`,
		`[{"id":1,"mandatory":true,"content":"Read this","publishDate":"2025-01-01T00:00:00Z","ackRevision":"has space"}]`,
		`[{"id":1,"mandatory":true,"content":"Read this","publishDate":"2025-01-01T00:00:00Z","ackRevision":"` + strings.Repeat("9", 33) + `"}]`,
	}
	for _, input := range invalid {
		if err := ValidateConsoleSettings(input, "Announcements"); err == nil {
			t.Errorf("accepted an invalid acknowledgement generation: %s", input)
		}
	}
}
