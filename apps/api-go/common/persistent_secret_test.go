package common

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func testPersistentStringEncryptionRequiresStrongExplicitSecret(t *testing.T) {
	const primary = "TEST_PERSISTENT_SECRET_PRIMARY"
	const fallback = "TEST_PERSISTENT_SECRET_FALLBACK"
	oldPrimary, hadPrimary := os.LookupEnv(primary)
	oldFallback, hadFallback := os.LookupEnv(fallback)
	t.Cleanup(func() {
		if hadPrimary {
			require.NoError(t, os.Setenv(primary, oldPrimary))
		} else {
			require.NoError(t, os.Unsetenv(primary))
		}
		if hadFallback {
			require.NoError(t, os.Setenv(fallback, oldFallback))
		} else {
			require.NoError(t, os.Unsetenv(fallback))
		}
	})

	require.NoError(t, os.Unsetenv(primary))
	require.NoError(t, os.Unsetenv(fallback))
	ciphertext, err := EncryptPersistentString("hero_sms.api_key", primary, fallback, "secret-value")
	require.ErrorIs(t, err, ErrPersistentKeyUnavailable)
	require.Empty(t, ciphertext)

	require.NoError(t, os.Setenv(primary, "too-short"))
	ciphertext, err = EncryptPersistentString("hero_sms.api_key", primary, fallback, "secret-value")
	require.ErrorIs(t, err, ErrPersistentKeyUnavailable)
	require.Empty(t, ciphertext)

	require.NoError(t, os.Setenv(primary, "REPLACE_WITH_AT_LEAST_32_RANDOM_BYTES"))
	ciphertext, err = EncryptPersistentString("hero_sms.api_key", primary, fallback, "secret-value")
	require.ErrorIs(t, err, ErrPersistentKeyUnavailable)
	require.Empty(t, ciphertext)

	require.NoError(t, os.Setenv(primary, "0123456789abcdef0123456789abcdef"))
	ciphertext, err = EncryptPersistentString("hero_sms.api_key", primary, fallback, "secret-value")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(ciphertext, persistentCipherEnvelopeV1))
	require.NotContains(t, ciphertext, "secret-value")

	plaintext, err := DecryptPersistentString("hero_sms.api_key", primary, fallback, ciphertext)
	require.NoError(t, err)
	require.Equal(t, "secret-value", plaintext)

	wrongPurposePlaintext, err := DecryptPersistentString("hero_sms.payload", primary, fallback, ciphertext)
	require.Error(t, err)
	require.Empty(t, wrongPurposePlaintext)
}

func testPersistentStringEncryptionUsesExplicitFallback(t *testing.T) {
	const primary = "TEST_PERSISTENT_SECRET_PRIMARY_FALLBACK"
	const fallback = "TEST_PERSISTENT_SECRET_FALLBACK_ONLY"
	t.Setenv(primary, "")
	t.Setenv(fallback, "fedcba9876543210fedcba9876543210")

	ciphertext, err := EncryptPersistentString("hero_sms.payload", primary, fallback, "email@example.test")
	require.NoError(t, err)
	plaintext, err := DecryptPersistentString("hero_sms.payload", primary, fallback, ciphertext)
	require.NoError(t, err)
	require.Equal(t, "email@example.test", plaintext)
}

// pi-lens-ignore: ast-grep:go-test-functions
func TestHeroSMSPersistentEncryption(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "PersistentStringEncryptionRequiresStrongExplicitSecret", run: testPersistentStringEncryptionRequiresStrongExplicitSecret},
		{name: "PersistentStringEncryptionUsesExplicitFallback", run: testPersistentStringEncryptionUsesExplicitFallback},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, testCase.run)
	}
}
