package dto

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemOneResponseRequiresEveryMatchingAnswer(t *testing.T) {
	questions := map[string]SystemOneQuestion{"safe": {Type: "noul"}, "rank": {Type: "score"}}
	for _, body := range []string{
		`null`,
		`{"answers":{"safe":{"type":"noul"},"rank":{"type":"score"}}}`,
		`{"model":"","answers":{}}`,
		`{"model":"jev-latest","answers":[]}`,
		`{"model":"jev-latest","answers":{"safe":{"type":"noul"}}}`,
		`{"model":"jev-latest","answers":{"safe":{"type":"choice"},"rank":{"type":"score"}}}`,
		`{"model":"jev-latest","answers":{"safe":{"type":"noul"},"rank":null}}`,
		`{"model":"jev-latest","answers":{"safe":{"type":"noul"},"rank":{"type":"score"}}}]`,
		`{"model":"jev-latest","answers":{"safe":{"type":"noul"},"rank":{"type":"score"}}}}`,
		`{"model":"jev-latest","answers":{"safe":{"type":"noul"},"rank":{"type":"score"}}}{}`,
	} {
		_, err := ValidateSystemOneResponse([]byte(body), questions)
		require.Error(t, err, body)
	}
	response, err := ValidateSystemOneResponse([]byte(`{"model":"jev-1.13.0","answers":{"safe":{"type":"noul","probability":0.9},"rank":{"type":"score","distribution":[0.2,0.8]}}}`), questions)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"score","distribution":[0.2,0.8]}`, string(response.Answers["rank"]))
}

func TestSystemOneInputUsageStrictContextBudget(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
		want  int
	}{
		{"0", true, 0}, {"1", true, 1}, {"1000", true, 1000}, {"65536", true, 65536},
		{"1.0", true, 1}, {"1e3", true, 1000}, {"-1", false, 0}, {"65537", false, 0},
		{"1.5", false, 0}, {`"100"`, false, 0}, {"null", false, 0}, {"true", false, 0},
		{"1e300", false, 0},
	} {
		t.Run(tc.value, func(t *testing.T) {
			response := &SystemOneResponse{Usage: json.RawMessage(fmt.Sprintf(`{"input_tokens":%s,"output_tokens":9999999}`, tc.value))}
			got, valid := response.InputTokenUsage()
			require.Equal(t, tc.valid, valid)
			require.Equal(t, tc.want, got)
		})
	}
	for _, usage := range []json.RawMessage{nil, []byte(`{}`), []byte(`null`), []byte(`[]`), []byte(`{"input_tokens":0,"input_tokens":100}`)} {
		_, valid := (&SystemOneResponse{Usage: usage}).InputTokenUsage()
		require.False(t, valid)
	}
}
