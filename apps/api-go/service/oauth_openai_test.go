package service

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOAuthModelFromIDCanonicalRoundTrip(t *testing.T) {
	for _, model := range []string{"model", "名称:版本", "vendor/model-1"} {
		id := "lmm:" + OAuthGroupID("vip") + ":" + base64.RawURLEncoding.EncodeToString([]byte(model))
		group, got, err := OAuthModelFromID(id)
		require.NoError(t, err)
		require.Equal(t, OAuthGroupID("vip"), group)
		require.Equal(t, model, got)
		for _, invalid := range []string{id + "=", id + ":extra", model} {
			_, _, err = OAuthModelFromID(invalid)
			require.Error(t, err)
		}
	}
	for _, invalid := range []string{"", "lmm::bW9kZWw", "lmm:../:bW9kZWw", "lmm:dmlw:_w", "lmm:dmlw:"} {
		_, _, err := OAuthModelFromID(invalid)
		require.Error(t, err)
	}
}
