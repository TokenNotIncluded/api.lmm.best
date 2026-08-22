package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

const persistentSecretEnvelopeV1 = "v1:"

var ErrPersistentSecretNotConfigured = errors.New("persistent secret is not configured")

func persistentSecretKey(purpose string, primaryEnv string, fallbackEnv string) ([]byte, error) {
	secret := strings.TrimSpace(os.Getenv(primaryEnv))
	if secret == "" && fallbackEnv != "" {
		secret = strings.TrimSpace(os.Getenv(fallbackEnv))
	}
	if secret == "" {
		return nil, ErrPersistentSecretNotConfigured
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(purpose) + ":" + secret))
	key := make([]byte, len(sum))
	copy(key, sum[:])
	return key, nil
}

func EncryptPersistentString(purpose string, primaryEnv string, fallbackEnv string, plaintext string) (string, error) {
	key, err := persistentSecretKey(purpose, primaryEnv, fallbackEnv)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, ciphertext...)
	return persistentSecretEnvelopeV1 + base64.RawURLEncoding.EncodeToString(payload), nil
}

func DecryptPersistentString(purpose string, primaryEnv string, fallbackEnv string, ciphertext string) (string, error) {
	trimmed := strings.TrimSpace(ciphertext)
	if trimmed == "" {
		return "", nil
	}
	if !strings.HasPrefix(trimmed, persistentSecretEnvelopeV1) {
		return "", fmt.Errorf("persistent ciphertext has invalid envelope")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(trimmed, persistentSecretEnvelopeV1))
	if err != nil {
		return "", err
	}
	key, err := persistentSecretKey(purpose, primaryEnv, fallbackEnv)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(encoded) < gcm.NonceSize() {
		return "", errors.New("persistent ciphertext is truncated")
	}
	plaintext, err := gcm.Open(nil, encoded[:gcm.NonceSize()], encoded[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
