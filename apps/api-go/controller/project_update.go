package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/service"

	"github.com/gin-gonic/gin"
)

const (
	projectCommitAPIURL  = "https://api.github.com/repos/TokenNotIncluded/api.lmm.best/commits/main"
	projectCommitURLBase = "https://github.com/TokenNotIncluded/api.lmm.best/commit/"
	maxProjectCommitBody = 1 << 20
)

type githubProjectCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Date string `json:"date"`
		} `json:"author"`
		Committer struct {
			Date string `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

type projectReleaseInfo struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
}

func normalizeProjectCommit(commit githubProjectCommit) (projectReleaseInfo, error) {
	sha := strings.ToLower(strings.TrimSpace(commit.SHA))
	if len(sha) < 7 || len(sha) > 64 {
		return projectReleaseInfo{}, errors.New("invalid commit sha length")
	}
	for _, char := range sha {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return projectReleaseInfo{}, errors.New("invalid commit sha")
		}
	}

	body := commit.Commit.Message
	message := strings.TrimSpace(body)
	if message == "" {
		return projectReleaseInfo{}, errors.New("missing commit message")
	}
	name := strings.TrimSpace(strings.SplitN(message, "\n", 2)[0])
	nameRunes := []rune(name)
	if len(nameRunes) > 120 {
		name = string(nameRunes[:120])
	}
	if name == "" {
		name = sha[:7]
	}

	publishedAt := strings.TrimSpace(commit.Commit.Author.Date)
	if publishedAt == "" {
		publishedAt = strings.TrimSpace(commit.Commit.Committer.Date)
	}
	parsedDate, err := time.Parse(time.RFC3339, publishedAt)
	if err != nil {
		return projectReleaseInfo{}, errors.New("invalid commit date")
	}

	return projectReleaseInfo{
		TagName:     sha[:7],
		Name:        name,
		Body:        body,
		HTMLURL:     projectCommitURLBase + sha,
		PublishedAt: parsedDate.Format(time.RFC3339),
	}, nil
}

func fetchProjectUpdate(request *http.Request) (projectReleaseInfo, error) {
	upstreamRequest, err := http.NewRequestWithContext(
		request.Context(),
		http.MethodGet,
		projectCommitAPIURL,
		nil,
	)
	if err != nil {
		return projectReleaseInfo{}, err
	}
	upstreamRequest.Header.Set("Accept", "application/vnd.github+json")
	upstreamRequest.Header.Set("User-Agent", "LMM-API-update-checker")
	upstreamRequest.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := service.GetHttpClient()
	if client == nil {
		return projectReleaseInfo{}, errors.New("http client is not initialized")
	}
	response, err := client.Do(upstreamRequest)
	if err != nil {
		return projectReleaseInfo{}, err
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return projectReleaseInfo{}, fmt.Errorf("unexpected upstream status: %d", response.StatusCode)
	}

	body, err := common.ReadAllLimit(response.Body, maxProjectCommitBody)
	if errors.Is(err, common.ErrLimitExceeded) {
		return projectReleaseInfo{}, errors.New("upstream response too large")
	}
	if err != nil {
		return projectReleaseInfo{}, err
	}

	var commit githubProjectCommit
	if err = json.Unmarshal(body, &commit); err != nil {
		return projectReleaseInfo{}, err
	}
	return normalizeProjectCommit(commit)
}

func GetProjectUpdate(c *gin.Context) {
	release, err := fetchProjectUpdate(c.Request)
	if err != nil {
		logger.LogError(c.Request.Context(), "project update check failed: "+err.Error())
		common.ApiErrorMsg(c, "Failed to check for updates")
		return
	}
	common.ApiSuccess(c, release)
}
