package controller

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

var assistantOperationURLPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s<>"']+`)
var assistantOperationBasicAuthPattern = regexp.MustCompile(`(?i)\bbasic\s+[a-z0-9+/]+={0,2}`)

func assistantOperationNormalizedName(name string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(name))
}

func assistantOperationSecretName(name string) bool {
	name = assistantOperationNormalizedName(name)
	return strings.HasSuffix(name, "key") || name == "auth" || name == "authentication" || name == "authorization" || name == "authvalue" || name == "pwd" || name == "sid" || name == "sig" || strings.Contains(name, "signature") || strings.Contains(name, "cookie") || strings.Contains(name, "sessionid") || strings.Contains(name, "password") || strings.Contains(name, "passwd") || strings.Contains(name, "passphrase") || strings.Contains(name, "secret") || strings.Contains(name, "credential") || strings.Contains(name, "privatekey") || strings.Contains(name, "apikey") || strings.Contains(name, "accesskey") || strings.Contains(name, "accesstoken") || strings.Contains(name, "refreshtoken") || strings.Contains(name, "sessiontoken") || strings.Contains(name, "securityproof") || strings.Contains(name, "backupcode") || strings.HasSuffix(name, "token")
}

// Providers can use arbitrary header/query names for credentials. Filtering
// familiar names alone cannot safely expose these configurable containers.
func assistantOperationOpaqueCredentialField(name string) bool {
	name = assistantOperationNormalizedName(name)
	return name == "header" || strings.HasSuffix(name, "headers") || strings.HasSuffix(name, "headeroverride") || strings.HasSuffix(name, "paramoverride") || strings.HasSuffix(name, "queryoverride") || name == "proxy" || strings.HasSuffix(name, "proxyurl") || name == "webhook" || strings.HasSuffix(name, "webhookurl")
}

func assistantOperationChannelPrivateField(name string) bool {
	switch assistantOperationNormalizedName(name) {
	case "key", "baseurl", "openaiorganization", "headeroverride", "paramoverride", "setting", "other", "settings":
		return true
	default:
		return false
	}
}

func assistantRedactOperationResponse(value any, handler string) any {
	if handler == "GetChannelKey" || handler == "GenerateAccessToken" {
		if response, ok := value.(map[string]any); ok && response["success"] == false {
			// The model needs the original proof-required code to route the user
			// to the secure dashboard. Failed responses contain no issued secret.
			return assistantRedactOperationValue(response, 0)
		}
		return map[string]any{"redacted": true, "message": "Credential disclosure is available only through the secure dashboard."}
	}
	channelResponse := handler == "GetAllChannels" || handler == "SearchChannels" || handler == "GetChannel"
	return assistantRedactOperationValueScoped(value, 0, channelResponse)
}

func assistantRedactOperationValue(value any, depth int) any {
	return assistantRedactOperationValueScoped(value, depth, false)
}

func assistantRedactOperationValueScoped(value any, depth int, channelResponse bool) any {
	if depth > 20 {
		return "[omitted: nested response]"
	}
	switch typed := value.(type) {
	case map[string]any:
		output := make(map[string]any, len(typed))
		optionKey, optionPair := typed["key"].(string)
		_, hasOptionValue := typed["value"]
		optionPair = optionPair && hasOptionValue
		for key, field := range typed {
			switch {
			case optionPair && key == "key":
				output[key] = optionKey
			case optionPair && key == "value" && (assistantOperationSecretName(optionKey) || assistantOperationOpaqueCredentialField(optionKey)):
				output[key] = "[redacted]"
			case assistantOperationSecretName(key) || assistantOperationOpaqueCredentialField(key) || (channelResponse && assistantOperationChannelPrivateField(key)):
				output[key] = "[redacted]"
			default:
				output[key] = assistantRedactOperationValueScoped(field, depth+1, channelResponse)
			}
		}
		return output
	case []any:
		output := make([]any, len(typed))
		for i, field := range typed {
			output[i] = assistantRedactOperationValueScoped(field, depth+1, channelResponse)
		}
		return output
	case string:
		text := strings.TrimSpace(typed)
		lower := strings.ToLower(text)
		if strings.Contains(lower, "-----begin ") || strings.Contains(lower, "bearer ") || strings.HasPrefix(text, "sk-") || (strings.HasPrefix(text, "eyJ") && strings.Count(text, ".") == 2) {
			return "[redacted]"
		}
		if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
			var embedded any
			if json.Unmarshal([]byte(text), &embedded) == nil {
				clean, err := json.Marshal(assistantRedactOperationValueScoped(embedded, depth+1, channelResponse))
				if err == nil {
					return string(clean)
				}
			}
		}
		// Error messages, remarks and JSON-in-string option values can contain
		// credentials independently of their field names. Reuse the transcript
		// filter, after stripping URL userinfo and signed URL parameters.
		clean := assistantOperationURLPattern.ReplaceAllStringFunc(typed, assistantRedactOperationURL)
		clean = assistantOperationBasicAuthPattern.ReplaceAllStringFunc(clean, func(candidate string) string {
			encoded := strings.TrimSpace(candidate[len("basic"):])
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				decoded, err = base64.RawStdEncoding.DecodeString(encoded)
			}
			if err == nil && strings.Contains(string(decoded), ":") {
				return "Basic [redacted]"
			}
			return candidate
		})
		return model.RedactAssistantHistoryContent(clean)
	default:
		return value
	}
}

func assistantRedactOperationURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return "[redacted URL]"
	}
	changed := parsed.User != nil
	parsed.User = nil
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "[redacted URL]"
	}
	for name := range query {
		if assistantOperationSecretName(name) {
			query.Set(name, "[redacted]")
			changed = true
		}
	}
	fragment := parsed.Fragment
	if index := strings.IndexByte(fragment, '?'); index >= 0 {
		fragment = fragment[index+1:]
	}
	if fields, err := url.ParseQuery(fragment); err == nil {
		for name := range fields {
			if assistantOperationSecretName(name) {
				parsed.Fragment = "[redacted]"
				parsed.RawFragment = ""
				changed = true
				break
			}
		}
	}
	if !changed {
		return value
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
