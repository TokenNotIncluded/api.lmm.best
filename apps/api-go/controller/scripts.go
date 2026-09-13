package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
)

const maxScriptBytes = 512 << 10

var scriptNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var scriptExtensions = map[string]bool{".sh": true, ".bash": true, ".zsh": true, ".ps1": true, ".cmd": true, ".bat": true}

type scriptInfo struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Updated time.Time `json:"updated"`
}

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
		items = append(items, scriptInfo{Name: entry.Name(), Size: info.Size(), Updated: info.ModTime().UTC()})
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
	c.Data(http.StatusOK, "text/plain; charset=utf-8", body)
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
