/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func PullScriptRepository(c *gin.Context) {
	repositoryURL := scriptOption(scriptsRepoURLOption)
	branch := scriptOption(scriptsRepoBranchOption)
	if repositoryURL == "" {
		common.ApiError(c, errors.New("configure a script repository first"))
		return
	}
	if branch == "" {
		branch = "main"
	}

	tmp, err := os.MkdirTemp("", "lmm-script-repo-")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	defer os.RemoveAll(tmp)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	args := []string{"clone", "--depth=1", "--branch", branch, repositoryURL, tmp}
	command := exec.CommandContext(ctx, "git", args...)
	if key := scriptOption(scriptsRepoKeyOption); key != "" {
		command.Env = append(os.Environ(), "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraheader", "GIT_CONFIG_VALUE_0=Authorization: Bearer "+key)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		common.ApiError(c, fmt.Errorf("git pull failed: %s", message))
		return
	}

	if err := syncRepositoryScripts(tmp); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOption(scriptsRepoPulledOption, time.Now().UTC().Format(time.RFC3339)); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "scripts.repository.pull", map[string]interface{}{"repository_url": repositoryURL, "branch": branch})
	common.ApiSuccess(c, gin.H{"updated_at": scriptOption(scriptsRepoPulledOption), "scripts": listScriptNames()})
}

func listScriptNames() []string {
	entries, err := os.ReadDir(scriptsDirectory())
	if err != nil {
		return []string{}
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && validateScriptName(entry.Name()) == nil {
			result = append(result, entry.Name())
		}
	}
	return result
}

func syncRepositoryScripts(repository string) error {
	entries, err := os.ReadDir(repository)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("script repository is empty")
	}
	allowed := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() || validateScriptName(entry.Name()) != nil {
			continue
		}
		allowed[entry.Name()] = struct{}{}
	}
	if len(allowed) == 0 {
		return errors.New("script repository contains no supported scripts")
	}
	destination := scriptsDirectory()
	if err := os.MkdirAll(destination, 0750); err != nil {
		return err
	}
	for name := range allowed {
		body, err := os.ReadFile(filepath.Join(repository, name))
		if err != nil {
			return err
		}
		if len(body) > maxScriptBytes {
			return fmt.Errorf("script %s is too large", name)
		}
		tmp, err := os.CreateTemp(destination, ".script-pull-*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		if err = tmp.Chmod(0640); err == nil {
			_, err = tmp.Write(body)
		}
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(tmpName, filepath.Join(destination, name))
		}
		if err != nil {
			_ = os.Remove(tmpName)
			return err
		}
	}
	return nil
}
