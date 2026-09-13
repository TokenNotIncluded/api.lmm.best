package controller

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func piRemoteTestCiphertext(value string) piRemoteCiphertext {
	return piRemoteCiphertext{
		Nonce:      base64.RawURLEncoding.EncodeToString([]byte("123456789012")),
		Ciphertext: base64.RawURLEncoding.EncodeToString([]byte(value + "-authentication-tag")),
	}
}

func TestPiRemoteStoreIsolatesUsersAndExpiresSessions(t *testing.T) {
	store := newPiRemoteStore()
	now := time.Unix(1000, 0)
	store.now = func() time.Time { return now }
	request := piRemoteUpsertRequest{DeviceID: "device_123456", Metadata: piRemoteTestCiphertext("metadata")}
	_, err := store.upsert(1, "session_123456", request)
	require.NoError(t, err)
	require.Len(t, store.list(1), 1)
	require.Empty(t, store.list(2))
	_, err = store.messages(2, "session_123456", 0)
	require.Error(t, err)
	now = now.Add(piRemoteSessionTTL + time.Second)
	require.Empty(t, store.list(1))
}

func TestPiRemoteMessagesAreOpaqueSequencedSnapshots(t *testing.T) {
	store := newPiRemoteStore()
	request := piRemoteUpsertRequest{DeviceID: "device_123456", Metadata: piRemoteTestCiphertext("metadata")}
	_, err := store.upsert(1, "session_123456", request)
	require.NoError(t, err)
	for _, sender := range []string{"plugin", "controller"} {
		payload := piRemoteTestCiphertext("opaque-" + sender)
		_, err = store.append(1, "session_123456", piRemoteAppendRequest{Sender: sender, Nonce: payload.Nonce, Ciphertext: payload.Ciphertext})
		require.NoError(t, err)
	}
	messages, err := store.messages(1, "session_123456", 1)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, uint64(2), messages[0].Sequence)
	require.Equal(t, "controller", messages[0].Sender)
}

func TestPiRemoteRejectsUnsafeIdentifiersAndCiphertext(t *testing.T) {
	store := newPiRemoteStore()
	_, err := store.upsert(1, "../session", piRemoteUpsertRequest{DeviceID: "device_123456", Metadata: piRemoteTestCiphertext("metadata")})
	require.Error(t, err)
	_, err = store.upsert(1, "session_123456", piRemoteUpsertRequest{DeviceID: "device_123456", Metadata: piRemoteCiphertext{Nonce: "not base64!", Ciphertext: "bad"}})
	require.Error(t, err)
}
