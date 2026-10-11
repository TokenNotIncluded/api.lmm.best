package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

const (
	codexLatestReleaseURL      = "https://api.github.com/repos/openai/codex/releases/latest"
	codexClientVersionCacheTTL = time.Hour
)

// Codex model discovery is an operator-facing integration with the ChatGPT
// backend. Keep the request target drawn from a fixed set of official origins;
// accepting an arbitrary channel URL here would turn the model-refresh action
// into a server-side request forgery primitive.
var codexModelOrigins = map[string]string{
	"chatgpt.com":     "https://chatgpt.com",
	"chat.openai.com": "https://chat.openai.com",
}

type codexClientVersionCache struct {
	sync.Mutex
	version   string
	expiresAt time.Time
}

var latestCodexClientVersion codexClientVersionCache

func GetLatestCodexClientVersion(ctx context.Context, client *http.Client) (string, error) {
	return latestCodexClientVersion.get(ctx, client, codexLatestReleaseURL, time.Now())
}

func (cache *codexClientVersionCache) get(ctx context.Context, client *http.Client, releaseURL string, now time.Time) (string, error) {
	cache.Lock()
	defer cache.Unlock()

	if cache.version != "" && now.Before(cache.expiresAt) {
		return cache.version, nil
	}

	version, err := fetchLatestCodexClientVersion(ctx, client, releaseURL)
	if err != nil {
		if cache.version != "" {
			cache.expiresAt = now.Add(codexClientVersionCacheTTL)
			return cache.version, nil
		}
		return "", err
	}

	cache.version = version
	cache.expiresAt = now.Add(codexClientVersionCacheTTL)
	return version, nil
}

func fetchLatestCodexClientVersion(ctx context.Context, client *http.Client, releaseURL string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("nil http client")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "LMM_API")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("codex release lookup failed: status=%d", resp.StatusCode)
	}

	var release struct {
		Name       string `json:"name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := common.DecodeJson(resp.Body, &release); err != nil {
		return "", err
	}
	if release.Draft || release.Prerelease {
		return "", fmt.Errorf("latest codex release is not stable")
	}
	version := strings.TrimSpace(release.Name)
	if version == "" {
		return "", fmt.Errorf("latest codex release has no version name")
	}
	return version, nil
}

func FetchCodexModels(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	oauthKey *CodexOAuthKey,
	clientVersion string,
) (statusCode int, models []string, err error) {
	if client == nil {
		return 0, nil, fmt.Errorf("nil http client")
	}
	if oauthKey == nil {
		return 0, nil, fmt.Errorf("nil oauth key")
	}

	accessToken := strings.TrimSpace(oauthKey.AccessToken)
	accountID := strings.TrimSpace(oauthKey.AccountID)
	clientVersion = strings.TrimSpace(clientVersion)
	if accessToken == "" {
		return 0, nil, fmt.Errorf("codex channel: access_token is required")
	}
	if accountID == "" {
		return 0, nil, fmt.Errorf("codex channel: account_id is required")
	}
	if clientVersion == "" {
		return 0, nil, fmt.Errorf("codex channel: client_version is required")
	}

	modelsURL, err := codexModelsURL(baseURL, clientVersion)
	if err != nil {
		return 0, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL.String(), nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("ChatGPT-Account-Id", accountID)
	req.Header.Set("User-Agent", "codex-cli/"+clientVersion)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := common.ReadResponseBody(resp)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp.StatusCode, nil, nil
	}

	var result struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err := common.Unmarshal(body, &result); err != nil {
		return resp.StatusCode, nil, err
	}

	seen := make(map[string]struct{}, len(result.Models))
	models = make([]string, 0, len(result.Models))
	for _, item := range result.Models {
		slug := strings.TrimSpace(item.Slug)
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		models = append(models, slug)
	}
	return resp.StatusCode, models, nil
}

func codexModelsURL(rawBaseURL, clientVersion string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return nil, fmt.Errorf("invalid Codex baseURL")
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.Port() != "" {
		return nil, fmt.Errorf("Codex baseURL must use an official HTTPS origin")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, fmt.Errorf("Codex baseURL must be an origin")
	}
	origin, ok := codexModelOrigins[strings.ToLower(parsed.Hostname())]
	if !ok {
		return nil, fmt.Errorf("Codex baseURL is not an official origin")
	}
	modelsURL, err := url.Parse(origin + "/backend-api/codex/models")
	if err != nil {
		return nil, err
	}
	query := modelsURL.Query()
	query.Set("client_version", strings.TrimSpace(clientVersion))
	modelsURL.RawQuery = query.Encode()
	return modelsURL, nil
}
