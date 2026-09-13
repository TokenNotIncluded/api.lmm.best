package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

const (
	defaultUpdateRepository = "TokenNotIncluded/api.lmm.best"
	defaultAURBackend       = "lmm-api-go-bin"
	defaultAURFrontend      = "lmm-api-web-bin"
	updateCheckTimeout      = 5 * time.Second
	maxUpdateResponseBody   = 2 << 20
)

var (
	updateRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	updatePackagePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._+:-]{0,127}$`)
	reportedVersionPattern  = regexp.MustCompile(`^[A-Za-z0-9._+-]*$`)
	githubUpdateAPIBase     = "https://api.github.com"
	aurUpdateAPIBase        = "https://aur.archlinux.org/rpc/v5"
)

type updateSource struct {
	Type       string `json:"type"`
	Repository string `json:"repository,omitempty"`
	Package    string `json:"package,omitempty"`
	Component  string `json:"component,omitempty"`
	TagPrefix  string `json:"tag_prefix,omitempty"`
	URL        string `json:"url,omitempty"`
	Enabled    bool   `json:"enabled"`
	EnabledSet bool   `json:"-"`
}

type updateCandidate struct {
	Version     string `json:"version"`
	Tag         string `json:"tag,omitempty"`
	URL         string `json:"url,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	Source      string `json:"source"`
}

type updateComponent struct {
	Current    string             `json:"current"`
	Latest     string             `json:"latest,omitempty"`
	Updated    string             `json:"updated,omitempty"`
	Sources    []updateSourceInfo `json:"sources"`
	candidates []updateCandidate
}

type updateSourceInfo struct {
	Type    string `json:"type"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

type githubUpdateRelease struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
}

type aurUpdateInfo struct {
	Results []struct {
		Name         string `json:"Name"`
		Version      string `json:"Version"`
		URL          string `json:"URL"`
		LastModified int64  `json:"LastModified"`
		OutOfDate    *int64 `json:"OutOfDate"`
	} `json:"results"`
}

func defaultUpdateSources() []updateSource {
	return []updateSource{{Type: "github_release", Repository: defaultUpdateRepository, Component: "backend", TagPrefix: "go-v", URL: "https://github.com/" + defaultUpdateRepository + "/releases", Enabled: true},
		{Type: "github_release", Repository: defaultUpdateRepository, Component: "frontend", TagPrefix: "web-v", URL: "https://github.com/" + defaultUpdateRepository + "/releases", Enabled: true},
		{Type: "aur", Package: defaultAURBackend, Component: "backend", URL: "https://aur.archlinux.org/packages/" + defaultAURBackend, Enabled: true},
		{Type: "aur", Package: defaultAURFrontend, Component: "frontend", URL: "https://aur.archlinux.org/packages/" + defaultAURFrontend, Enabled: true}}
}

// parseUpdateSources accepts either an object keyed by source type or a list of
// source objects. Invalid entries are ignored and reported by the caller.
func parseUpdateSources(raw string) ([]updateSource, []string) {
	if strings.TrimSpace(raw) == "" {
		return defaultUpdateSources(), nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return defaultUpdateSources(), []string{"UpdateSources is invalid JSON"}
	}
	var sources []updateSource
	var parseErrs []string
	add := func(source updateSource) {
		source.Type = strings.ToLower(strings.TrimSpace(source.Type))
		source.Component = strings.ToLower(strings.TrimSpace(source.Component))
		source.Repository = strings.TrimSpace(source.Repository)
		source.Package = strings.TrimSpace(source.Package)
		source.URL = strings.TrimSpace(source.URL)
		if source.URL != "" && !source.EnabledSet {
			source.Enabled = true
		}
		if source.Type == "github" || source.Type == "release" {
			source.Type = "github_release"
		}
		if source.URL != "" {
			if strings.HasPrefix(source.URL, "https://github.com/") {
				parts := strings.Split(strings.TrimPrefix(strings.TrimSuffix(source.URL, "/"), "https://github.com/"), "/")
				if len(parts) >= 2 && source.Repository == "" {
					source.Repository = parts[0] + "/" + parts[1]
				}
			} else if strings.HasPrefix(source.URL, "https://aur.archlinux.org/packages/") && source.Package == "" {
				source.Package = strings.Trim(strings.TrimPrefix(source.URL, "https://aur.archlinux.org/packages/"), "/")
			}
		}
		if source.Component == "" {
			if source.Type == "github_release" {
				source.Component = "auto"
			} else {
				if source.Type == "aur" && source.Package == defaultAURFrontend {
					source.Component = "frontend"
				} else {
					source.Component = "backend"
				}
			}
		}
		if source.Type == "github_release" && source.TagPrefix == "" {
			if source.Component == "backend" {
				source.TagPrefix = "go-v"
			} else if source.Component == "frontend" {
				source.TagPrefix = "web-v"
			}
		}
		switch source.Type {
		case "github_release", "github_commit":
			if source.Repository == "" {
				source.Repository = defaultUpdateRepository
			}
			if !updateRepositoryPattern.MatchString(source.Repository) {
				parseErrs = append(parseErrs, "invalid GitHub repository")
				return
			}
			if source.URL == "" {
				source.URL = "https://github.com/" + source.Repository + "/releases"
			}
		case "aur":
			if !updatePackagePattern.MatchString(source.Package) {
				parseErrs = append(parseErrs, "invalid AUR package")
				return
			}
			if source.URL == "" {
				source.URL = "https://aur.archlinux.org/packages/" + source.Package
			}
		default:
			parseErrs = append(parseErrs, "unsupported update source: "+source.Type)
			return
		}
		sources = append(sources, source)
	}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			encoded, _ := json.Marshal(item)
			var source updateSource
			if json.Unmarshal(encoded, &source) == nil {
				add(source)
			}
		}
	case map[string]any:
		for sourceType, item := range typed {
			if sourceType == "backend" || sourceType == "frontend" {
				entries, ok := item.([]any)
				if !ok {
					parseErrs = append(parseErrs, sourceType+" sources must be an array")
					continue
				}
				for _, entry := range entries {
					encoded, _ := json.Marshal(entry)
					var source updateSource
					if json.Unmarshal(encoded, &source) != nil {
						continue
					}
					if object, ok := entry.(map[string]any); ok {
						_, source.EnabledSet = object["enabled"]
					}
					source.Component = sourceType
					add(source)
				}
				continue
			}
			switch entries := item.(type) {
			case []any:
				for _, entry := range entries {
					if sourceType == "aur" {
						if pkg, ok := entry.(string); ok {
							add(updateSource{Type: sourceType, Package: pkg})
							continue
						}
					}
					encoded, _ := json.Marshal(entry)
					var source updateSource
					_ = json.Unmarshal(encoded, &source)
					source.Type = sourceType
					add(source)
				}
			case string:
				add(updateSource{Type: sourceType, Repository: entries, Package: entries})
			default:
				encoded, _ := json.Marshal(item)
				var source updateSource
				_ = json.Unmarshal(encoded, &source)
				source.Type = sourceType
				add(source)
			}
		}
	default:
		parseErrs = append(parseErrs, "UpdateSources must be an object or array")
	}
	if len(sources) == 0 {
		sources = defaultUpdateSources()
	}
	return sources, parseErrs
}

func updateHTTPClient() *http.Client {
	client := service.GetHttpClient()
	if client == nil {
		client = http.DefaultClient
	}
	copy := *client
	copy.Timeout = updateCheckTimeout
	return &copy
}

func fetchUpdatesJSON(ctx context.Context, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "LMM-API-update-checker")
	response, err := updateHTTPClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("upstream status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxUpdateResponseBody+1))
	if len(body) > maxUpdateResponseBody {
		return errors.New("upstream response too large")
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func fetchGitHubSource(ctx context.Context, source updateSource) ([]updateCandidate, error) {
	if !updateRepositoryPattern.MatchString(source.Repository) {
		return nil, errors.New("invalid GitHub repository")
	}
	endpoint := strings.TrimRight(githubUpdateAPIBase, "/") + "/repos/" + source.Repository
	if source.Type == "github_commit" {
		endpoint += "/commits/main"
		var commit struct {
			SHA string `json:"sha"`
		}
		if err := fetchUpdatesJSON(ctx, endpoint, &commit); err != nil {
			return nil, err
		}
		if len(commit.SHA) < 7 {
			return nil, errors.New("invalid GitHub commit response")
		}
		return []updateCandidate{{Version: commit.SHA[:7], Tag: commit.SHA, URL: "https://github.com/" + source.Repository + "/commit/" + commit.SHA, Source: source.Type}}, nil
	}
	endpoint += "/releases?per_page=30"
	var releases []githubUpdateRelease
	if err := fetchUpdatesJSON(ctx, endpoint, &releases); err != nil {
		return nil, err
	}
	candidates := make([]updateCandidate, 0, len(releases))
	for _, release := range releases {
		if release.Draft || release.Prerelease || strings.TrimSpace(release.TagName) == "" || (source.TagPrefix != "" && !strings.HasPrefix(release.TagName, source.TagPrefix)) {
			continue
		}
		version := strings.TrimPrefix(release.TagName, source.TagPrefix)
		version = strings.TrimPrefix(version, "v")
		candidates = append(candidates, updateCandidate{Version: version, Tag: release.TagName, URL: release.HTMLURL, PublishedAt: release.PublishedAt, Source: source.Type})
	}
	return candidates, nil
}

func fetchAURSource(ctx context.Context, source updateSource) ([]updateCandidate, error) {
	if !updatePackagePattern.MatchString(source.Package) {
		return nil, errors.New("invalid AUR package")
	}
	endpoint := strings.TrimRight(aurUpdateAPIBase, "/") + "/info?arg[]=" + source.Package
	var payload aurUpdateInfo
	if err := fetchUpdatesJSON(ctx, endpoint, &payload); err != nil {
		return nil, err
	}
	if len(payload.Results) == 0 || strings.TrimSpace(payload.Results[0].Version) == "" {
		return nil, errors.New("AUR package not found")
	}
	item := payload.Results[0]
	published := ""
	if item.LastModified > 0 {
		published = time.Unix(item.LastModified, 0).UTC().Format(time.RFC3339)
	}
	return []updateCandidate{{Version: item.Version, URL: item.URL, PublishedAt: published, Source: source.Type}}, nil
}

func GetUpdates(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	rawSources := common.OptionMap["UpdateSources"]
	common.OptionMapRWMutex.RUnlock()
	sources, errorsList := parseUpdateSources(rawSources)
	result := struct {
		Backend  updateComponent `json:"backend"`
		Frontend updateComponent `json:"frontend"`
		Errors   []string        `json:"errors"`
	}{Backend: updateComponent{Current: strings.TrimPrefix(common.Version, "v"), candidates: []updateCandidate{}, Sources: []updateSourceInfo{}}, Frontend: updateComponent{Current: normalizeReportedVersion(c.Query("frontend_version")), candidates: []updateCandidate{}, Sources: []updateSourceInfo{}}, Errors: errorsList}
	ctx, cancel := context.WithTimeout(c.Request.Context(), updateCheckTimeout)
	defer cancel()
	type fetched struct {
		source     updateSource
		candidates []updateCandidate
		err        error
	}
	results := make(chan fetched, len(sources))
	for _, source := range sources {
		go func(source updateSource) {
			if !source.Enabled {
				results <- fetched{source: source}
				return
			}
			var candidates []updateCandidate
			var err error
			if source.Type == "aur" {
				candidates, err = fetchAURSource(ctx, source)
			} else {
				candidates, err = fetchGitHubSource(ctx, source)
			}
			results <- fetched{source: source, candidates: candidates, err: err}
		}(source)
	}
	for range sources {
		item := <-results
		if !item.source.Enabled {
			info := updateSourceInfo{Type: item.source.Type, URL: item.source.URL, Enabled: false}
			if item.source.Component == "frontend" {
				result.Frontend.Sources = append(result.Frontend.Sources, info)
			} else {
				result.Backend.Sources = append(result.Backend.Sources, info)
			}
			continue
		}
		if item.err != nil {
			label := item.source.Type
			if item.source.Package != "" {
				label += ":" + item.source.Package
			} else {
				label += ":" + item.source.Repository
			}
			result.Errors = append(result.Errors, label+": "+item.err.Error())
			continue
		}
		for _, candidate := range item.candidates {
			component := item.source.Component
			if component == "auto" {
				switch {
				case strings.HasPrefix(candidate.Tag, "web-v"), strings.Contains(candidate.URL, "web-"):
					component = "frontend"
				default:
					component = "backend"
				}
			}
			if component == "frontend" {
				result.Frontend.candidates = append(result.Frontend.candidates, candidate)
			} else {
				result.Backend.candidates = append(result.Backend.candidates, candidate)
			}
		}
		info := updateSourceInfo{Type: item.source.Type, URL: item.source.URL, Enabled: item.source.Enabled}
		if item.source.Component == "frontend" {
			result.Frontend.Sources = append(result.Frontend.Sources, info)
		} else if item.source.Component == "backend" {
			result.Backend.Sources = append(result.Backend.Sources, info)
		}
	}
	sort.SliceStable(result.Backend.candidates, func(i, j int) bool {
		return isUpdateVersionNewer(result.Backend.candidates[i].Version, result.Backend.candidates[j].Version)
	})
	sort.SliceStable(result.Frontend.candidates, func(i, j int) bool {
		return isUpdateVersionNewer(result.Frontend.candidates[i].Version, result.Frontend.candidates[j].Version)
	})
	if len(result.Backend.candidates) > 0 {
		result.Backend.Latest = result.Backend.candidates[0].Version
	}
	if len(result.Frontend.candidates) > 0 {
		result.Frontend.Latest = result.Frontend.candidates[0].Version
	}
	if result.Backend.Latest != "" && isUpdateVersionNewer(result.Backend.Current, result.Backend.Latest) {
		result.Backend.Updated = result.Backend.candidates[0].PublishedAt
	}
	if result.Frontend.Latest != "" && isUpdateVersionNewer(result.Frontend.Current, result.Frontend.Latest) {
		result.Frontend.Updated = result.Frontend.candidates[0].PublishedAt
	}
	common.ApiSuccess(c, result)
}

func normalizeReportedVersion(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 64 || !reportedVersionPattern.MatchString(value) {
		return "unknown"
	}
	if value == "" {
		return "unknown"
	}
	return strings.TrimPrefix(value, "v")
}

func isUpdateVersionNewer(current, candidate string) bool {
	current = strings.TrimPrefix(strings.TrimSpace(current), "v")
	candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "v")
	if current == "" || candidate == "" || current == candidate {
		return false
	}
	parse := func(value string) []int {
		value = strings.SplitN(value, "-", 2)[0]
		parts := strings.Split(value, ".")
		result := make([]int, len(parts))
		for i, part := range parts {
			var n int
			if _, err := fmt.Sscan(part, &n); err != nil {
				return nil
			}
			result[i] = n
		}
		return result
	}
	a, b := parse(current), parse(candidate)
	if a == nil || b == nil {
		return candidate > current
	}
	for i := 0; i < len(a) || i < len(b); i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			return bv > av
		}
	}
	return false
}
