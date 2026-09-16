// Package registrationguard contains only deterministic, non-identifying admission policy.
package registrationguard

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"unicode"
)

const Version = "registration-v1"

// Evidence must be assembled from persisted server observations, never tool arguments.
// Email provider, username shape, language, typing speed and model-written prose
// deliberately do not exist in this type.
type Evidence struct {
	TemplatePeers      int  `json:"template_peers"`
	NetworkPeers       int  `json:"network_peers"`
	IdentityRewardUsed bool `json:"identity_reward_used"`
}

type Decision struct {
	Alert      bool `json:"alert"`
	Hold       bool `json:"hold"`
	CanSuspend bool `json:"can_suspend"`
}

func Evaluate(e Evidence) Decision {
	campaign := e.TemplatePeers >= 4
	linked := e.NetworkPeers >= 3
	return Decision{
		Alert:      campaign || e.IdentityRewardUsed,
		Hold:       e.IdentityRewardUsed || (campaign && linked),
		CanSuspend: campaign && linked && e.IdentityRewardUsed,
	}
}

// Fingerprints are HMACs of sufficiently long character shingles. Shared
// greetings, punctuation and single short answers do not enter the index.
// Eight bottom hashes bound storage; they are similarity signals, not identity.
func Fingerprints(secret, text string) (string, []string) {
	if secret == "" {
		return "", nil
	}
	normalized := make([]rune, 0, min(len(text), 2048))
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized = append(normalized, r)
			if len(normalized) == 2048 {
				break
			}
		}
	}
	if len(normalized) < 40 {
		return "", nil
	}
	digest := hash(secret, "message:", string(normalized))
	seen := make(map[string]struct{})
	for i := 0; i+20 <= len(normalized); i += 4 {
		seen[hash(secret, "shingle:", string(normalized[i:i+20]))] = struct{}{}
	}
	tokens := make([]string, 0, len(seen))
	for token := range seen {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return digest, tokens[:min(8, len(tokens))]
}

func hash(secret, namespace, text string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(Version + ":" + namespace + text))
	return hex.EncodeToString(h.Sum(nil))
}
