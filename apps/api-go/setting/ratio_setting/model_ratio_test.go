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

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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

func TestJevDefaultRatiosAndAdministratorOverrides(t *testing.T) {
	previousModels := ModelRatio2JSONString()
	previousCompletions := CompletionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateModelRatioByJSONString(previousModels))
		require.NoError(t, UpdateCompletionRatioByJSONString(previousCompletions))
	})
	for _, name := range []string{"jev-1.13.0", "jev-latest", "jev-preview"} {
		require.Equal(t, 0.021, GetDefaultModelRatioMap()[name])
		ratio, exists := defaultCompletionRatio[name]
		require.True(t, exists)
		require.Zero(t, ratio)
	}
	require.NoError(t, UpdateModelRatioByJSONString(DefaultModelRatio2JSONString()))
	completionRatioMap.AddAll(defaultCompletionRatio)
	for _, name := range []string{"jev-1.13.0", "jev-latest", "jev-preview"} {
		ratio, found, _ := GetModelRatio(name)
		require.True(t, found)
		require.Equal(t, 0.021, ratio)
		require.Equal(t, CompletionRatioInfo{Ratio: 0, Locked: false}, GetCompletionRatioInfo(name))
	}

	// New built-in defaults must not force an administrator's existing prices.
	require.NoError(t, UpdateModelRatioByJSONString(`{"jev-latest":0.05}`))
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"jev-latest":1.5}`))
	ratio, found, _ := GetModelRatio("jev-latest")
	require.True(t, found)
	require.Equal(t, 0.05, ratio)
	require.Equal(t, CompletionRatioInfo{Ratio: 1.5, Locked: false}, GetCompletionRatioInfo("jev-latest"))
}
