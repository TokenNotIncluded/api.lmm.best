package controller

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantKeyPostgresCommitTimeAuthorizationFenceRejectsCompletedSecurityMutations(t *testing.T) {
	harness := openAssistantKeyPostgresHarness(t)
	cases := []struct {
		name   string
		mutate func(t *testing.T, harness *assistantKeyPostgresHarness)
		want   error
	}{
		{
			name: "disabled account",
			mutate: func(t *testing.T, harness *assistantKeyPostgresHarness) {
				require.NoError(t, harness.db.Model(&model.User{}).Where("id = ?", 7).Update("status", 0).Error)
			},
			want: errAssistantKeyAccountUnavailable,
		},
		{
			name: "bumped session version",
			mutate: func(t *testing.T, harness *assistantKeyPostgresHarness) {
				require.NoError(t, harness.db.Exec("UPDATE user_sessions SET version = version + 1 WHERE sid = 'fence-session' AND user_id = 7").Error)
			},
			want: model.ErrAssistantKeyAuthorizationChanged,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			flow := createAssistantKeyPostgresFlow(t, "fence-session", nil)
			test.mutate(t, harness)
			err := consumeAssistantKeyPostgresFlow(flow, "fence-session")
			require.ErrorIs(t, err, test.want)
			assert.EqualValues(t, 0, countAssistantKeyPostgresRows(t, harness.db, "tokens"))
			assert.EqualValues(t, 0, countAssistantKeyPostgresRows(t, harness.db, "assistant_secure_cards"))
		})
	}

}
