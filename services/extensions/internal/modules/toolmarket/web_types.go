// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later.
// Web DTOs preserve the contract of TokenNotIncluded/api.lmm.best main
// 6efcb6fb0f6d: apps/api-go/model/tool_market{,_queries,_private,_access}.go.
package toolmarket

import "encoding/json"

// WebServiceRecord and WebVersion keep author drafts separate from immutable
// released snapshots. They are module business records, never Core identities.
type WebServiceRecord struct {
	ID             string `json:"id"`
	OwnerID        int64  `json:"owner_id"`
	LiveVersionID  string `json:"live_version_id"`
	DraftVersionID string `json:"draft_version_id"`
	Status         string `json:"status"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}
type WebVersion struct {
	ID            string `json:"id"`
	ServiceID     string `json:"service_id"`
	Status        string `json:"status"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	ExecutionType string `json:"execution_type"`
	Visibility    string `json:"visibility"`
	Endpoint      string `json:"endpoint"`
	Digest        string `json:"digest"`
	ReviewedBy    int64  `json:"reviewed_by"`
	ReviewNote    string `json:"review_note"`
	CreatedAt     int64  `json:"created_at"`
	PublishedAt   int64  `json:"published_at"`
}
type WebTool struct {
	ToolID       string `json:"tool_id"`
	VersionID    string `json:"version_id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	InputSchema  string `json:"input_schema"`
	OutputSchema string `json:"output_schema"`
	Permissions  string `json:"permissions"`
	PriceQuota   int64  `json:"price_quota"`
}
type WebDetail struct {
	Service      WebServiceRecord `json:"service"`
	Version      WebVersion       `json:"version"`
	Tools        []WebTool        `json:"tools"`
	Pricing      string           `json:"pricing"`
	Validated    bool             `json:"validated"`
	AllowedUsers []int64          `json:"allowed_users,omitempty"`
}
type WebSummary struct {
	ID            string `json:"id"`
	OwnerID       int64  `json:"owner_id"`
	VersionID     string `json:"version_id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	ExecutionType string `json:"execution_type"`
	PublishedAt   int64  `json:"published_at"`
	ToolCount     int    `json:"tool_count"`
	MinPriceQuota int64  `json:"min_price_quota"`
	MaxPriceQuota int64  `json:"max_price_quota"`
	MeteredTools  int    `json:"metered_tools"`
	ProviderTools int    `json:"provider_tools"`
}
type WebToolInput struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	Permissions  []string        `json:"permissions"`
	PriceQuota   int64           `json:"price_quota"`
	// Variable/provider prices require a separate verified pricing adapter.
	ProviderPricing      json.RawMessage `json:"provider_pricing,omitempty"`
	BillingMode          string          `json:"billing_mode,omitempty"`
	InputTokenPriceQuota int64           `json:"input_token_price_quota,omitempty"`
	MaxInputTokens       int64           `json:"max_input_tokens,omitempty"`
	BillingRules         json.RawMessage `json:"billing_rules,omitempty"`
}
type WebDraftInput struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	ExecutionType string         `json:"execution_type"`
	Visibility    string         `json:"visibility"`
	Endpoint      string         `json:"endpoint"`
	AllowedUsers  []int64        `json:"allowed_users"`
	Tools         []WebToolInput `json:"tools"`
}
type WebInstallation struct {
	UserID    int64  `json:"user_id"`
	ClientID  string `json:"client_id"`
	ToolID    string `json:"tool_id"`
	VersionID string `json:"version_id"`
	CreatedAt int64  `json:"created_at"`
}
type WebQuery struct {
	Text, ExecutionType string
	Offset, Limit       int
}
type WebRequest struct {
	Action, ID, Kind string
	Offset, Limit    int
	Body             json.RawMessage
}
type webSnapshot struct {
	Version          WebVersion        `json:"version"`
	Tools            []WebTool         `json:"tools"`
	AllowedUsers     []int64           `json:"allowed_users"`
	Digest           string            `json:"digest"`
	ValidationDigest string            `json:"validation_digest"`
	RemoteDigests    map[string]string `json:"remote_digests,omitempty"`
}
type webDatabase struct {
	Services      map[string]WebServiceRecord `json:"services"`
	Versions      map[string]webSnapshot      `json:"versions"`
	Installations map[string]WebInstallation  `json:"installations"`
	Favorites     map[string]int64            `json:"favorites"`
}

func webState(d *database) *webDatabase {
	if d.Web == nil {
		d.Web = &webDatabase{Services: map[string]WebServiceRecord{}, Versions: map[string]webSnapshot{}, Installations: map[string]WebInstallation{}, Favorites: map[string]int64{}}
	}
	return d.Web
}
