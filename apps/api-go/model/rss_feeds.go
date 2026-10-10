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

const RSSFeedsOptionKey = "RSSFeeds"

const maxRSSFeeds = 24

type RSSFeedConfig struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

var rssFeedIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func parseRSSFeeds(value string) ([]RSSFeedConfig, error) {
	if len(value) > 128*1024 {
		return nil, errors.New("RSS configuration is too large")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return []RSSFeedConfig{}, nil
	}
	if !strings.HasPrefix(value, "[") {
		return nil, errors.New("RSS feeds must be a JSON array")
	}

	var feeds []RSSFeedConfig
	if err := json.Unmarshal([]byte(value), &feeds); err != nil || feeds == nil {
		return nil, errors.New("RSS feeds must be a JSON array")
	}
	var fields []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &fields); err != nil {
		return nil, errors.New("RSS feed entries must be JSON objects")
	}
	if len(feeds) > maxRSSFeeds {
		return nil, fmt.Errorf("RSS supports at most %d feeds", maxRSSFeeds)
	}

	seenIDs := make(map[string]bool, len(feeds))
	seenURLs := make(map[string]bool, len(feeds))
	for index, feed := range feeds {
		for _, key := range []string{"id", "name", "url", "enabled"} {
			raw, present := fields[index][key]
			if !present || string(raw) == "null" {
				return nil, fmt.Errorf("RSS feed %d is missing %s", index+1, key)
			}
		}

		feed.ID = strings.TrimSpace(feed.ID)
		feed.Name = strings.TrimSpace(feed.Name)
		feed.URL = strings.TrimSpace(feed.URL)
		if !rssFeedIDPattern.MatchString(feed.ID) || seenIDs[feed.ID] {
			return nil, fmt.Errorf("RSS feed %d has an invalid or duplicate ID", index+1)
		}
		seenIDs[feed.ID] = true
		if feed.Name == "" || len([]rune(feed.Name)) > 80 {
			return nil, fmt.Errorf("RSS feed %d has an invalid name", index+1)
		}
		if len(feed.URL) > 2048 || strings.ContainsAny(feed.URL, " \t\r\n") {
			return nil, fmt.Errorf("RSS feed %d has an invalid URL", index+1)
		}
		parsed, err := url.Parse(feed.URL)
		if err != nil ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Hostname() == "" ||
			parsed.User != nil {
			return nil, fmt.Errorf("RSS feed %d must use an absolute HTTP or HTTPS URL without credentials", index+1)
		}
		normalizedURL := parsed.String()
		if seenURLs[normalizedURL] {
			return nil, fmt.Errorf("RSS feed %d duplicates another feed URL", index+1)
		}
		seenURLs[normalizedURL] = true
		feeds[index] = feed
	}
	return feeds, nil
}

func ValidateRSSFeeds(value string) error {
	_, err := parseRSSFeeds(value)
	return err
}

func PublicRSSFeeds() []RSSFeedConfig {
	raw := GetOptionsSnapshot()[RSSFeedsOptionKey]
	feeds, err := parseRSSFeeds(raw)
	if err != nil {
		return []RSSFeedConfig{}
	}
	enabled := make([]RSSFeedConfig, 0, len(feeds))
	for _, feed := range feeds {
		if feed.Enabled {
			enabled = append(enabled, feed)
		}
	}
	return enabled
}
