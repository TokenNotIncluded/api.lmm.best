package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

const maxScriptBytes = 512 << 10

var scriptNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var scriptBranchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
var scriptExtensions = map[string]bool{".sh": true, ".bash": true, ".zsh": true, ".ps1": true, ".cmd": true, ".bat": true}

type scriptInfo struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Updated time.Time `json:"updated"`
	Fetches int64     `json:"fetches"`
}

const (
	scriptsRepoURLOption    = "ScriptsRepoURL"
	scriptsRepoBranchOption = "ScriptsRepoBranch"
	scriptsRepoKeyOption    = "ScriptsRepoGithubKey"
	scriptsRepoPulledOption = "ScriptsRepoPulledAt"
)

var scriptStatsMu sync.Mutex

type scriptWriteRequest struct {
	Content string `json:"content"`
}

func scriptsDirectory() string {
	if configured := strings.TrimSpace(os.Getenv("SCRIPTS_DIR")); configured != "" {
		return filepath.Clean(configured)
	}
	working, err := os.Getwd()
	if err != nil {
		return "hosted-scripts"
	}
	return filepath.Join(working, "hosted-scripts")
}

func validateScriptName(name string) error {
	if !scriptNamePattern.MatchString(name) || filepath.Base(name) != name {
		return errors.New("invalid script name")
	}
	if !scriptExtensions[strings.ToLower(filepath.Ext(name))] {
		return errors.New("unsupported script extension")
	}
	return nil
}

func scriptPath(name string) (string, error) {
	if err := validateScriptName(name); err != nil {
		return "", err
	}
	return filepath.Join(scriptsDirectory(), name), nil
}

func scriptStatsPath() string { return filepath.Join(scriptsDirectory(), ".fetch-stats.json") }

func scriptFetches(name string) int64 {
	data, err := os.ReadFile(scriptStatsPath())
	if err != nil {
		return 0
	}
	var stats map[string]int64
	if json.Unmarshal(data, &stats) != nil {
		return 0
	}
	return stats[name]
}

func recordScriptFetch(name string) {
	scriptStatsMu.Lock()
	defer scriptStatsMu.Unlock()
	path := scriptStatsPath()
	stats := map[string]int64{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &stats)
	}
	stats[name]++
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fetch-stats-*")
	if err != nil {
		return
	}
	if encoded, marshalErr := json.Marshal(stats); marshalErr == nil {
		_, err = tmp.Write(encoded)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		_ = os.Rename(tmp.Name(), path)
	} else {
		_ = os.Remove(tmp.Name())
	}
}

func readScript(name string) ([]byte, os.FileInfo, error) {
	path, err := scriptPath(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("script is not a regular file")
	}
	if info.Size() > maxScriptBytes {
		return nil, nil, errors.New("script is too large")
	}
	body, err := os.ReadFile(path)
	return body, info, err
}

func ListScripts(c *gin.Context) {
	directory := scriptsDirectory()
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		common.ApiSuccess(c, []scriptInfo{})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]scriptInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !scriptExtensions[strings.ToLower(filepath.Ext(entry.Name()))] || validateScriptName(entry.Name()) != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxScriptBytes {
			continue
		}
		items = append(items, scriptInfo{Name: entry.Name(), Size: info.Size(), Updated: info.ModTime().UTC(), Fetches: scriptFetches(entry.Name())})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	common.ApiSuccess(c, items)
}

func GetScriptRaw(c *gin.Context) {
	body, _, err := readScript(c.Param("name"))
	if err != nil {
		if os.IsNotExist(err) {
			c.Status(http.StatusNotFound)
			return
		}
		c.Status(http.StatusBadRequest)
		return
	}
	recordScriptFetch(c.Param("name"))
	c.Data(http.StatusOK, "text/plain; charset=utf-8", body)
}

type scriptRepositoryResponse struct {
	RepositoryURL string `json:"repository_url"`
	Branch        string `json:"branch"`
	GithubKeySet  bool   `json:"github_key_set"`
	LastPulledAt  string `json:"last_pulled_at,omitempty"`
}

func scriptOption(key string) string {
	if value, ok := model.GetOptionsSnapshot()[key]; ok {
		return strings.TrimSpace(common.Interface2String(value))
	}
	return ""
}

func GetScriptRepository(c *gin.Context) {
	last := scriptOption(scriptsRepoPulledOption)
	common.ApiSuccess(c, scriptRepositoryResponse{RepositoryURL: scriptOption(scriptsRepoURLOption), Branch: scriptOption(scriptsRepoBranchOption), GithubKeySet: scriptOption(scriptsRepoKeyOption) != "", LastPulledAt: last})
}

type scriptRepositoryRequest struct {
	RepositoryURL  string `json:"repository_url"`
	Branch         string `json:"branch"`
	GithubKey      string `json:"github_key"`
	ClearGithubKey bool   `json:"clear_github_key"`
}

func PutScriptRepository(c *gin.Context) {
	var input scriptRepositoryRequest
	if err := common.DecodeJson(c.Request.Body, &input); err != nil {
		common.ApiError(c, err)
		return
	}
	input.RepositoryURL = strings.TrimSpace(input.RepositoryURL)
	input.Branch = strings.TrimSpace(input.Branch)
	if input.Branch == "" {
		input.Branch = "main"
	}
	if input.RepositoryURL != "" {
		u, err := url.Parse(input.RepositoryURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			common.ApiError(c, errors.New("repository URL must be an http(s) URL without credentials"))
			return
		}
	}
	if !scriptBranchPattern.MatchString(input.Branch) || strings.Contains(input.Branch, "..") || strings.Contains(input.Branch, "//") || strings.HasSuffix(input.Branch, ".lock") || strings.HasSuffix(input.Branch, "/") {
		common.ApiError(c, errors.New("invalid repository branch"))
		return
	}
	if strings.ContainsAny(input.GithubKey, "\r\n") || len(input.GithubKey) > 512 {
		common.ApiError(c, errors.New("invalid GitHub key"))
		return
	}
	values := map[string]string{scriptsRepoURLOption: input.RepositoryURL, scriptsRepoBranchOption: input.Branch}
	if input.GithubKey != "" {
		values[scriptsRepoKeyOption] = strings.TrimSpace(input.GithubKey)
	}
	if input.ClearGithubKey {
		values[scriptsRepoKeyOption] = ""
	}
	for key, value := range values {
		if err := model.UpdateOption(key, value); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	recordManageAudit(c, "scripts.repository.update", map[string]interface{}{"repository_url": input.RepositoryURL, "branch": input.Branch, "github_key_changed": input.GithubKey != "" || input.ClearGithubKey})
	GetScriptRepository(c)
}

func GetScript(c *gin.Context) {
	body, info, err := readScript(c.Param("name"))
	if err != nil {
		if os.IsNotExist(err) {
			c.Status(http.StatusNotFound)
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"name": c.Param("name"), "content": string(body), "size": info.Size(), "updated": info.ModTime().UTC()})
}

func PutScript(c *gin.Context) {
	name := c.Param("name")
	path, err := scriptPath(name)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	body, err := common.ReadAllLimit(c.Request.Body, maxScriptBytes+1)
	if err != nil || len(body) > maxScriptBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": "script is too large"})
		return
	}
	content := body
	if mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(c.GetHeader("Content-Type"), ";", 2)[0])); mediaType == "application/json" {
		var request scriptWriteRequest
		if err := json.Unmarshal(body, &request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid JSON request"})
			return
		}
		content = []byte(request.Content)
	}
	if len(content) > maxScriptBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": "script is too large"})
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		common.ApiError(c, err)
		return
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".script-*")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err = temporary.Chmod(0640); err == nil {
		_, err = temporary.Write(content)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporaryName, path)
	}
	if err != nil {
		common.ApiError(c, fmt.Errorf("write script: %w", err))
		return
	}
	common.ApiSuccess(c, gin.H{"name": name, "size": len(content)})
}

func DeleteScript(c *gin.Context) {
	path, err := scriptPath(c.Param("name"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			c.Status(http.StatusNotFound)
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"name": c.Param("name")})
}
