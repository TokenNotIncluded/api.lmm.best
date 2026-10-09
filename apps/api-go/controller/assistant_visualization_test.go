// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAssistantVisualizationValidatedDataOnly(t *testing.T) {
	cases := []struct{ name, raw string }{
		{"show_chart", `{"title":"Recorded requests","chart_type":"line","labels":["Mon","Tue"],"series":[{"name":"Calls","values":[0,12]}]}`},
		{"show_statistics", `{"title":"Summary","items":[{"label":"Requests","value":"12","icon":"chart"}]}`},
		{"show_choices", `{"title":"Choose a period","items":[{"label":"Seven days","value":"Show the last seven days"}]}`},
		{"show_flowchart", `{"title":"Process","nodes":[{"id":"a","label":"Start"},{"id":"b","label":"Finish"}],"edges":[{"from":"a","to":"b"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var input map[string]any
			require.NoError(t, json.Unmarshal([]byte(tc.raw), &input))
			visual, err := parseAssistantVisualization(tc.name, input)
			require.NoError(t, err)
			require.NotEmpty(t, visual.Kind)
			input["script"] = "alert(1)"
			_, err = parseAssistantVisualization(tc.name, input)
			require.Error(t, err)
		})
	}
	for _, raw := range []string{
		`{"title":"Bad","chart_type":"donut","labels":["a"],"series":[{"name":"x","values":[-1]}]}`,
		`{"title":"Bad","chart_type":"bar","labels":["a"],"series":[{"name":"x","values":[1,2]}]}`,
		`{"title":"Bad","chart_type":"line","labels":["a"],"series":[{"name":"x","values":[1e20]}]}`,
	} {
		var input map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &input))
		_, err := parseAssistantVisualization("show_chart", input)
		require.Error(t, err)
	}
	var broken map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"title":"Invalid edge","nodes":[{"id":"a","label":"Start"}],"edges":[{"from":"a","to":"missing"}]}`), &broken))
	_, err := parseAssistantVisualization("show_flowchart", broken)
	require.Error(t, err)
}
