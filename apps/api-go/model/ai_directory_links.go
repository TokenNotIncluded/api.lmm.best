/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const AIDirectoryLinksOptionKey = "AIDirectoryLinks"

type AIDirectoryLink struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Category    string `json:"category"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

var directoryLinkIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

var directoryLinkCategories = map[string]bool{
	"chat": true, "research": true, "resources": true,
	"developer": true, "creative": true, "other": true,
}

func ValidateAIDirectoryLinks(value string) error {
	if len(value) > 512*1024 {
		return errors.New("AI directory configuration is too large")
	}
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "[") {
		return errors.New("AI directory must be a JSON array")
	}
	var links []AIDirectoryLink
	if err := json.Unmarshal([]byte(value), &links); err != nil || links == nil {
		return errors.New("AI directory must be a JSON array")
	}
	var fields []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &fields); err != nil {
		return errors.New("AI directory entries must be JSON objects")
	}
	if len(links) > 60 {
		return errors.New("AI directory supports at most 60 websites")
	}
	seen := make(map[string]bool, len(links))
	for index, link := range links {
		for _, key := range []string{"id", "name", "url", "category", "summary", "description", "enabled"} {
			raw, present := fields[index][key]
			if !present || string(raw) == "null" {
				return fmt.Errorf("website %d is missing %s", index+1, key)
			}
		}
		if !directoryLinkIDPattern.MatchString(link.ID) || seen[link.ID] {
			return fmt.Errorf("website %d has an invalid or duplicate ID", index+1)
		}
		seen[link.ID] = true
		if strings.TrimSpace(link.Name) == "" || len([]rune(link.Name)) > 80 ||
			len([]rune(link.Summary)) > 180 || len([]rune(link.Description)) > 1200 {
			return fmt.Errorf("website %d has invalid text lengths", index+1)
		}
		if !directoryLinkCategories[link.Category] {
			return fmt.Errorf("website %d has an invalid category", index+1)
		}
		if len(link.URL) > 2048 || strings.ContainsAny(link.URL, " \t\r\n") {
			return fmt.Errorf("website %d has an invalid URL", index+1)
		}
		parsed, err := url.Parse(link.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
			return fmt.Errorf("website %d must use an absolute HTTP or HTTPS URL without credentials", index+1)
		}
	}
	return nil
}

func PublicAIDirectoryLinks() json.RawMessage {
	raw := GetOptionsSnapshot()[AIDirectoryLinksOptionKey]
	if raw == "" || ValidateAIDirectoryLinks(raw) != nil {
		return nil
	}
	return json.RawMessage(raw)
}
