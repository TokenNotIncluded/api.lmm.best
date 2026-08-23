/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package ratio_setting

import "testing"

func TestDefaultSonarLargeRatiosUseFloatingDivision(t *testing.T) {
	defaults := GetDefaultModelRatioMap()
	want := 1.0 / 1000 * USD
	for _, name := range []string{
		"llama-3-sonar-large-32k-chat",
		"llama-3-sonar-large-32k-online",
	} {
		ratio, ok := defaults[name]
		if !ok {
			t.Fatalf("missing default ratio for %s", name)
		}
		if ratio == 0 {
			t.Fatalf("default ratio for %s is 0 because of integer division", name)
		}
		if ratio != want {
			t.Fatalf("default ratio for %s = %v, want %v", name, ratio, want)
		}
	}
}
