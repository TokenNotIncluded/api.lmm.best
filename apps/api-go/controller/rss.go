/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package controller

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

const (
	rssMaxFeedBytes      = 2 << 20
	rssMaxItemsPerFeed   = 50
	rssFetchConcurrency  = 6
	rssFeedTimeout       = 6 * time.Second
	rssOverallTimeout    = 10 * time.Second
	rssSuccessCacheTTL   = 3 * time.Minute
	rssFailureCacheTTL   = time.Minute
	rssSummaryRuneLimit  = 600
)

type rssItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Summary     string `json:"summary"`
	PublishedAt string `json:"published_at,omitempty"`
}

type rssFeedResult struct {
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	URL   string    `json:"url"`
	Items []rssItem `json:"items"`
	Error string    `json:"error,omitempty"`
}

type rssItemXML struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Description string `xml:"description"`
	Content     string `xml:"encoded"`
	PubDate     string `xml:"pubDate"`
	Date        string `xml:"date"`
}

type rssDocumentXML struct {
	Channel struct {
		Items []rssItemXML `xml:"item"`
	} `xml:"channel"`
}

type rdfDocumentXML struct {
	Items []rssItemXML `xml:"item"`
}

type atomLinkXML struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

type atomEntryXML struct {
	ID        string        `xml:"id"`
	Title     string        `xml:"title"`
	Links     []atomLinkXML `xml:"link"`
	Summary   string        `xml:"summary"`
	Content   string        `xml:"content"`
	Published string        `xml:"published"`
	Updated   string        `xml:"updated"`
}

type atomDocumentXML struct {
	Entries []atomEntryXML `xml:"entry"`
}

var rssHTMLTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

var rssDateLayouts = []string{
	time.RFC3339,
	time.RFC3339Nano,
	time.RFC1123Z,
	time.RFC1123,
	time.RFC822Z,
	time.RFC822,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"2006-01-02T15:04:05Z0700",
}

var rssCache = struct {
	sync.RWMutex
	key       string
	expiresAt time.Time
	feeds     []rssFeedResult
}{}

var rssFetchGroup singleflight.Group

func GetRSS(c *gin.Context) {
	feeds := loadRSSFeeds(model.PublicRSSFeeds())
	c.Header("Cache-Control", "private, max-age=60")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"feeds": feeds},
	})
}

func loadRSSFeeds(configs []model.RSSFeedConfig) []rssFeedResult {
	keyBytes, _ := json.Marshal(configs)
	key := string(keyBytes)
	if feeds, ok := cachedRSSFeeds(key); ok {
		return feeds
	}

	value, _, _ := rssFetchGroup.Do(key, func() (any, error) {
		if feeds, ok := cachedRSSFeeds(key); ok {
			return feeds, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), rssOverallTimeout)
		defer cancel()
		feeds := fetchRSSFeeds(ctx, configs)
		ttl := rssSuccessCacheTTL
		for _, feed := range feeds {
			if feed.Error != "" {
				ttl = rssFailureCacheTTL
				break
			}
		}
		rssCache.Lock()
		rssCache.key = key
		rssCache.expiresAt = time.Now().Add(ttl)
		rssCache.feeds = feeds
		rssCache.Unlock()
		return feeds, nil
	})
	return value.([]rssFeedResult)
}

func cachedRSSFeeds(key string) ([]rssFeedResult, bool) {
	rssCache.RLock()
	defer rssCache.RUnlock()
	if rssCache.key != key || time.Now().After(rssCache.expiresAt) {
		return nil, false
	}
	return rssCache.feeds, true
}

func fetchRSSFeeds(ctx context.Context, configs []model.RSSFeedConfig) []rssFeedResult {
	results := make([]rssFeedResult, len(configs))
	sem := make(chan struct{}, rssFetchConcurrency)
	var wg sync.WaitGroup
	for index, config := range configs {
		wg.Add(1)
		go func(index int, config model.RSSFeedConfig) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[index] = failedRSSFeed(config, "timeout")
				return
			}
			results[index] = fetchRSSFeed(ctx, config)
		}(index, config)
	}
	wg.Wait()
	return results
}

func fetchRSSFeed(parent context.Context, config model.RSSFeedConfig) rssFeedResult {
	result := rssFeedResult{
		ID:    config.ID,
		Name:  config.Name,
		URL:   config.URL,
		Items: []rssItem{},
	}
	if err := service.ValidateSSRFProtectedFetchURL(config.URL); err != nil {
		result.Error = "blocked"
		return result
	}

	ctx, cancel := context.WithTimeout(parent, rssFeedTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.URL, nil)
	if err != nil {
		result.Error = "invalid"
		return result
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", "api.lmm.best RSS reader")

	client := service.GetSSRFProtectedHTTPClient()
	if client == nil {
		result.Error = "unavailable"
		return result
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(parent.Err(), context.DeadlineExceeded) {
			result.Error = "timeout"
		} else {
			result.Error = "unavailable"
		}
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		result.Error = "http"
		return result
	}
	if resp.ContentLength > rssMaxFeedBytes {
		result.Error = "too_large"
		return result
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, rssMaxFeedBytes+1))
	if err != nil {
		result.Error = "unavailable"
		return result
	}
	if len(body) > rssMaxFeedBytes {
		result.Error = "too_large"
		return result
	}
	items, err := parseRSSDocument(body, config.URL)
	if err != nil {
		result.Error = "invalid"
		return result
	}
	if len(items) > rssMaxItemsPerFeed {
		items = items[:rssMaxItemsPerFeed]
	}
	result.Items = items
	return result
}

func failedRSSFeed(config model.RSSFeedConfig, code string) rssFeedResult {
	return rssFeedResult{
		ID:    config.ID,
		Name:  config.Name,
		URL:   config.URL,
		Items: []rssItem{},
		Error: code,
	}
}

func parseRSSDocument(body []byte, feedURL string) ([]rssItem, error) {
	var root struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal(body, &root); err != nil {
		return nil, err
	}

	var items []rssItem
	switch strings.ToLower(root.XMLName.Local) {
	case "rss":
		var document rssDocumentXML
		if err := xml.Unmarshal(body, &document); err != nil {
			return nil, err
		}
		items = convertRSSItems(document.Channel.Items, feedURL)
	case "rdf":
		var document rdfDocumentXML
		if err := xml.Unmarshal(body, &document); err != nil {
			return nil, err
		}
		items = convertRSSItems(document.Items, feedURL)
	case "feed":
		var document atomDocumentXML
		if err := xml.Unmarshal(body, &document); err != nil {
			return nil, err
		}
		items = convertAtomItems(document.Entries, feedURL)
	default:
		return nil, fmt.Errorf("unsupported feed root %q", root.XMLName.Local)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].PublishedAt > items[j].PublishedAt
	})
	return items, nil
}

func convertRSSItems(entries []rssItemXML, feedURL string) []rssItem {
	items := make([]rssItem, 0, len(entries))
	for _, entry := range entries {
		link := resolveRSSItemURL(feedURL, entry.Link)
		published := normalizeRSSDate(firstNonEmpty(entry.PubDate, entry.Date))
		summary := firstNonEmpty(entry.Description, entry.Content)
		id := strings.TrimSpace(entry.GUID)
		if id == "" {
			id = firstNonEmpty(link, strings.TrimSpace(entry.Title)+"|"+published)
		}
		title := rssPlainText(entry.Title)
		if title == "" {
			title = link
		}
		if title == "" {
			continue
		}
		items = append(items, rssItem{
			ID:          id,
			Title:       title,
			URL:         link,
			Summary:     rssPlainText(summary),
			PublishedAt: published,
		})
	}
	return items
}

func convertAtomItems(entries []atomEntryXML, feedURL string) []rssItem {
	items := make([]rssItem, 0, len(entries))
	for _, entry := range entries {
		link := resolveRSSItemURL(feedURL, atomAlternateLink(entry.Links))
		published := normalizeRSSDate(firstNonEmpty(entry.Published, entry.Updated))
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			id = firstNonEmpty(link, strings.TrimSpace(entry.Title)+"|"+published)
		}
		title := rssPlainText(entry.Title)
		if title == "" {
			title = link
		}
		if title == "" {
			continue
		}
		items = append(items, rssItem{
			ID:          id,
			Title:       title,
			URL:         link,
			Summary:     rssPlainText(firstNonEmpty(entry.Summary, entry.Content)),
			PublishedAt: published,
		})
	}
	return items
}

func atomAlternateLink(links []atomLinkXML) string {
	for _, link := range links {
		rel := strings.TrimSpace(strings.ToLower(link.Rel))
		if rel == "" || rel == "alternate" {
			return strings.TrimSpace(link.Href)
		}
	}
	if len(links) > 0 {
		return strings.TrimSpace(links[0].Href)
	}
	return ""
}

func resolveRSSItemURL(feedURL string, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	base, err := url.Parse(feedURL)
	if err != nil {
		return ""
	}
	link, err := base.Parse(raw)
	if err != nil ||
		(link.Scheme != "http" && link.Scheme != "https") ||
		link.Hostname() == "" ||
		link.User != nil {
		return ""
	}
	return link.String()
}

func normalizeRSSDate(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, layout := range rssDateLayouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func rssPlainText(value string) string {
	value = rssHTMLTagPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > rssSummaryRuneLimit {
		return string(runes[:rssSummaryRuneLimit]) + "…"
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
