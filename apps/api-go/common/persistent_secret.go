package common

import (
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

const persistentCipherEnvelopeV1 = "v1:"

var ErrPersistentKeyUnavailable = errors.New("persistent encryption key is unavailable")

func persistentCipherKey(purpose string, primaryEnv string, fallbackEnv string) ([]byte, error) {
	secret := strings.TrimSpace(os.Getenv(primaryEnv))
	if secret == "" && fallbackEnv != "" {
		secret = strings.TrimSpace(os.Getenv(fallbackEnv))
	}
	if len(secret) < 32 || weakPersistentKeyMaterial(secret) {
		return nil, ErrPersistentKeyUnavailable
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(purpose) + ":" + secret))
	key := make([]byte, len(sum))
	copy(key, sum[:])
	return key, nil
}

func weakPersistentKeyMaterial(secret string) bool {
	lower := strings.ToLower(strings.TrimSpace(secret))
	for _, marker := range []string{"replace_with", "random_string", "your_secret", "example-secret", "change-me", "changeme"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	unique := make(map[rune]struct{})
	for _, character := range secret {
		unique[character] = struct{}{}
	}
	return len(unique) < 4
}

func EncryptPersistentString(purpose string, primaryEnv string, fallbackEnv string, plaintext string) (string, error) {
	key, err := persistentCipherKey(purpose, primaryEnv, fallbackEnv)
	if err != nil {
		return "", fmt.Errorf("derive persistent encryption key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create persistent cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create persistent GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	bytesRead, err := cryptorand.Read(nonce)
	if err != nil {
		return "", fmt.Errorf("generate persistent nonce: %w", err)
	}
	if bytesRead != len(nonce) {
		return "", errors.New("generate persistent nonce: incomplete read")
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, ciphertext...)
	return persistentCipherEnvelopeV1 + base64.RawURLEncoding.EncodeToString(payload), nil
}

// pi-lens-ignore: go-bare-error
func DecryptPersistentString(purpose string, primaryEnv string, fallbackEnv string, ciphertext string) (string, error) {
	trimmed := strings.TrimSpace(ciphertext)
	if trimmed == "" {
		return "", nil
	}
	if !strings.HasPrefix(trimmed, persistentCipherEnvelopeV1) {
		return "", fmt.Errorf("persistent ciphertext has invalid envelope")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(trimmed, persistentCipherEnvelopeV1))
	if err != nil {
		return "", fmt.Errorf("decode persistent envelope: %w", err)
	}
	key, err := persistentCipherKey(purpose, primaryEnv, fallbackEnv)
	if err != nil {
		return "", fmt.Errorf("derive persistent decryption key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create persistent decrypt cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create persistent decrypt GCM: %w", err)
	}
	if len(encoded) < gcm.NonceSize() {
		return "", errors.New("persistent ciphertext is truncated")
	}
	plaintext, err := gcm.Open(nil, encoded[:gcm.NonceSize()], encoded[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt persistent ciphertext: %w", err)
	}
	return string(plaintext), nil
}
