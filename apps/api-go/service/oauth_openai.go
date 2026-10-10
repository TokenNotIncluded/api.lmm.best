package service

import (
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"
)

const OAuthOpenAIBasePath = "/api/oauth2/openai/v1"

// OAuthModelFromID accepts only the exact group-bound IDs issued by Catalog.
// Routing never picks a default group or treats an upstream name as an ID.
func OAuthModelFromID(id string) (groupID, model string, err error) {
	parts := strings.Split(id, ":")
	invalid := errors.New("invalid OAuth catalog model ID")
	if len(parts) != 3 || parts[0] != "lmm" || len(id) > 2048 {
		return "", "", invalid
	}
	if _, err := OAuthGroupFromID(parts[1]); err != nil {
		return "", "", invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(raw) == 0 || len(raw) > 512 || !utf8.Valid(raw) || base64.RawURLEncoding.EncodeToString(raw) != parts[2] {
		return "", "", invalid
	}
	return parts[1], string(raw), nil
}
