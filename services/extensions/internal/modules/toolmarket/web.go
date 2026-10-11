// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later.
// Contract source: main 6efcb6fb0f6d apps/api-go/model/tool_market*.go.
package toolmarket

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// WebPermission is an action, not a grant inferred from read-only identity RPCs.
// The production Authority must ask Rust to authorize this exact action.
func WebPermission(action string) string {
	switch action {
	case "draft", "mine":
		return "toolmarket.read"
	case "install", "favorite":
		return "toolmarket.authorize"
	case "reviews", "review", "review_detail":
		return "toolmarket.review"
	case "configure":
		return "toolmarket.config"
	case "save", "validate", "submit", "activate", "pause", "delete", "inspect":
		return "toolmarket.manage"
	case "invoke", "grant", "budget":
		// Disabled operations only authenticate their caller and entitlement.
		// They never request, infer or return an execution/funding grant.
		return "toolmarket.read"
	default:
		return ""
	}
}

// WebAuthorize rejects team ownership in the legacy personal-user DTO. Neither
// an account header nor a service credential may choose the Web actor.
func (m *Module) WebAuthorize(ctx context.Context, credential, account, action string) (int64, error) {
	if m == nil || m.authority == nil || m.store == nil {
		return 0, ErrUnavailable
	}
	if action != "toolmarket.read" && action != "toolmarket.manage" && action != "toolmarket.authorize" && action != "toolmarket.review" && action != "toolmarket.config" && action != "toolmarket.execute" {
		return 0, ErrDenied
	}
	p, err := m.authority.Authorize(ctx, credential, account, action)
	if err != nil {
		return 0, err
	}
	user, err := strconv.ParseInt(p.User, 10, 64)
	if err != nil || user <= 0 || !p.valid() || p.Account != account || account != strconv.FormatInt(user, 10) {
		return 0, ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return 0, ErrUnavailable
	}
	return user, nil
}

func (m *Module) PublicWebConfig(ctx context.Context) (map[string]any, error) {
	release, err := m.webEnter(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := m.store.view(func(database) error { return nil }); err != nil {
		return nil, err
	}
	return map[string]any{
		"enabled": false, "builtin_enabled": false, "fee_bps": 0, "recipient_id": 0,
		"quota_per_unit": 500000, "credits_per_usd": "500000", "web_client_id": "web-market", "mcp_path": "/mcp/market",
		"provider_presets": []any{}, "execution_ready": false,
		"capabilities": map[string]bool{"service_deletion": true, "client_record_cleanup": false, "meta_delegation": false, "mcp_oauth": false, "metamcp": false},
	}, nil
}
func (m *Module) webEnter(ctx context.Context) (func(), error) {
	if m == nil || m.store == nil || m.active == nil || ctx == nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	select {
	case m.active <- struct{}{}:
		return func() { <-m.active }, nil
	default:
		return nil, ErrLimit
	}
}
func validWebPage(offset, limit int) bool {
	return offset >= 0 && offset <= 10000 && limit >= 1 && limit <= 100
}
func (m *Module) webActor(ctx context.Context, credential, account string) (int64, error) {
	if credential == "" && account == "" {
		return 0, nil
	}
	return m.WebAuthorize(ctx, credential, account, "toolmarket.read")
}
func webVisible(s WebServiceRecord, v webSnapshot, user int64) bool {
	if s.Status != "published" || v.Version.Status != "published" || s.LiveVersionID != v.Version.ID || v.Version.ServiceID != s.ID {
		return false
	}
	if v.Version.Visibility == "public" || user > 0 && s.OwnerID == user {
		return true
	}
	if user > 0 && v.Version.Visibility == "shared" {
		for _, allowed := range v.AllowedUsers {
			if allowed == user {
				return true
			}
		}
	}
	return false
}
func webSummary(s WebServiceRecord, v webSnapshot) WebSummary {
	out := WebSummary{ID: s.ID, OwnerID: s.OwnerID, VersionID: v.Version.ID, Name: v.Version.Name, Description: v.Version.Description, ExecutionType: v.Version.ExecutionType, PublishedAt: v.Version.PublishedAt, ToolCount: len(v.Tools)}
	for i, tool := range v.Tools {
		if i == 0 || tool.PriceQuota < out.MinPriceQuota {
			out.MinPriceQuota = tool.PriceQuota
		}
		if tool.PriceQuota > out.MaxPriceQuota {
			out.MaxPriceQuota = tool.PriceQuota
		}
	}
	return out
}
func webMatches(v webSnapshot, text string) bool {
	if strings.Contains(strings.ToLower(v.Version.Name), text) || strings.Contains(strings.ToLower(v.Version.Description), text) {
		return true
	}
	for _, t := range v.Tools {
		if strings.Contains(strings.ToLower(t.Name), text) || strings.Contains(strings.ToLower(t.Description), text) {
			return true
		}
	}
	return false
}
func webSlice[T any](rows []T, offset, limit int) []T {
	if offset >= len(rows) {
		return []T{}
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[offset:end]
}
func (m *Module) PublicWebCatalogue(ctx context.Context, credential, account string, query WebQuery) ([]WebSummary, error) {
	user, err := m.webActor(ctx, credential, account)
	if err != nil {
		return nil, err
	}
	release, err := m.webEnter(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	text := strings.TrimSpace(query.Text)
	if !validWebPage(query.Offset, query.Limit) || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 120 || query.ExecutionType != "" && query.ExecutionType != "remote" && query.ExecutionType != "builtin" && query.ExecutionType != "serverless" {
		return nil, ErrInvalid
	}
	rows := []WebSummary{}
	err = m.store.view(func(d database) error {
		w := webState(&d)
		for _, s := range w.Services {
			v, ok := w.Versions[s.LiveVersionID]
			if !ok || !webVisible(s, v, user) || query.ExecutionType != "" && query.ExecutionType != v.Version.ExecutionType || !webMatches(v, strings.ToLower(text)) {
				continue
			}
			rows = append(rows, webSummary(s, v))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool {
		if (rows[i].ExecutionType == "builtin") != (rows[j].ExecutionType == "builtin") {
			return rows[i].ExecutionType == "builtin"
		}
		if rows[i].PublishedAt != rows[j].PublishedAt {
			return rows[i].PublishedAt > rows[j].PublishedAt
		}
		return rows[i].ID < rows[j].ID
	})
	return webSlice(rows, query.Offset, query.Limit), nil
}
func webDetail(s WebServiceRecord, v webSnapshot, user int64, draft bool) WebDetail {
	tools := append([]WebTool{}, v.Tools...)
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].Name != tools[j].Name {
			return tools[i].Name < tools[j].Name
		}
		return tools[i].ToolID < tools[j].ToolID
	})
	free := 0
	for _, t := range tools {
		if t.PriceQuota == 0 {
			free++
		}
	}
	pricing := "paid"
	if len(tools) > 0 && free == len(tools) {
		pricing = "free"
	} else if free > 0 {
		pricing = "partially_free"
	}
	if !draft {
		s.DraftVersionID = ""
	}
	out := WebDetail{Service: s, Version: v.Version, Tools: tools, Pricing: pricing, Validated: v.ValidationDigest != "" && v.ValidationDigest == v.Digest}
	if user == s.OwnerID {
		out.AllowedUsers = append([]int64{}, v.AllowedUsers...)
	}
	return out
}
func (m *Module) PublicWebDetail(ctx context.Context, credential, account, id string) (WebDetail, error) {
	user, err := m.webActor(ctx, credential, account)
	if err != nil {
		return WebDetail{}, err
	}
	release, err := m.webEnter(ctx)
	if err != nil {
		return WebDetail{}, err
	}
	defer release()
	if !validID.MatchString(id) {
		return WebDetail{}, ErrInvalid
	}
	var out WebDetail
	err = m.store.view(func(d database) error {
		w := webState(&d)
		s, ok := w.Services[id]
		v, found := w.Versions[s.LiveVersionID]
		if !ok || !found || !webVisible(s, v, user) {
			return ErrMissing
		}
		out = webDetail(s, v, user, false)
		return nil
	})
	return out, err
}
func webOwned(w *webDatabase, user int64, id string) (WebServiceRecord, error) {
	s, ok := w.Services[id]
	if !ok || s.OwnerID != user || s.Status == "deleted" {
		return s, ErrMissing
	}
	return s, nil
}
func webDraft(w *webDatabase, s WebServiceRecord) (webSnapshot, error) {
	id := s.DraftVersionID
	if id == "" {
		id = s.LiveVersionID
	}
	v, ok := w.Versions[id]
	if !ok || v.Version.ServiceID != s.ID {
		return v, ErrMissing
	}
	return v, nil
}

// LegacyWeb reauthorizes every operation independently of the HTTP adapter.
// No operation here dispatches an execution or grants permission to spend.
func (m *Module) LegacyWeb(ctx context.Context, credential, account string, in WebRequest) (any, error) {
	permission := WebPermission(in.Action)
	if permission == "" {
		return nil, ErrMissing
	}
	user, err := m.WebAuthorize(ctx, credential, account, permission)
	if err != nil {
		return nil, err
	}
	release, err := m.webEnter(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	p := Principal{User: strconv.FormatInt(user, 10), Account: account}
	if err = m.admit(p); err != nil {
		return nil, err
	}
	if in.ID != "" && !validID.MatchString(in.ID) {
		return nil, ErrInvalid
	}
	switch in.Action {
	case "save":
		v, err := payload[WebDraftInput](in.Body, "name", "execution_type", "visibility", "endpoint", "tools")
		if err != nil {
			return nil, err
		}
		return m.webSave(p, user, in.ID, v)
	case "draft", "review_detail":
		var out WebDetail
		err = m.store.view(func(d database) error {
			w := webState(&d)
			s, ok := w.Services[in.ID]
			if !ok || s.Status == "deleted" {
				return ErrMissing
			}
			if in.Action == "draft" && s.OwnerID != user {
				return ErrMissing
			}
			v, e := webDraft(w, s)
			if e != nil {
				return e
			}
			out = webDetail(s, v, user, true)
			return nil
		})
		return out, err
	case "mine":
		return m.webMine(user, in.Kind, in.Offset, in.Limit)
	case "reviews":
		return m.webReviews(in.Offset, in.Limit)
	case "inspect":
		return m.webInspect(ctx, in.Body)
	case "validate":
		return nil, m.webValidate(ctx, p, user, in.ID, "", false, credential, account, permission)
	case "submit", "activate":
		v, e := payload[struct {
			VersionID string `json:"version_id"`
		}](in.Body, "version_id")
		if e != nil || !validID.MatchString(v.VersionID) {
			return nil, ErrInvalid
		}
		if in.Action == "activate" {
			if e = m.webValidate(ctx, p, user, in.ID, v.VersionID, false, credential, account, permission); e != nil {
				return nil, e
			}
			if _, e = m.WebAuthorize(ctx, credential, account, permission); e != nil {
				return nil, e
			}
		}
		return nil, m.webTransition(p, user, in.ID, v.VersionID, in.Action)
	case "review":
		v, e := payload[struct {
			VersionID string `json:"version_id"`
			Approve   bool   `json:"approve"`
			Note      string `json:"note"`
		}](in.Body, "version_id", "approve", "note")
		if e != nil || !validID.MatchString(v.VersionID) || strings.TrimSpace(v.Note) == "" || len(v.Note) > 1000 {
			return nil, ErrInvalid
		}
		if v.Approve {
			if e = m.webValidate(ctx, p, user, in.ID, v.VersionID, true, credential, account, permission); e != nil {
				return nil, e
			}
			if _, e = m.WebAuthorize(ctx, credential, account, permission); e != nil {
				return nil, e
			}
		}
		return nil, m.webReview(p, user, in.ID, v.VersionID, v.Approve, v.Note)
	case "install":
		return nil, m.webInstall(p, user, in.Body)
	case "favorite":
		v, e := payload[struct {
			Favorite bool `json:"favorite"`
		}](in.Body, "favorite")
		if e != nil {
			return nil, e
		}
		return nil, m.store.update(func(d *database) error {
			w := webState(d)
			key := digest([]any{user, in.ID})
			if v.Favorite {
				s, ok := w.Services[in.ID]
				version, found := w.Versions[s.LiveVersionID]
				if !ok || !found || !webVisible(s, version, user) {
					return ErrMissing
				}
				if len(w.Favorites) >= 100000 {
					return ErrLimit
				}
				w.Favorites[key] = m.now().Unix()
			} else {
				delete(w.Favorites, key)
			}
			d.audit(p, "web.favorite", in.ID, "ok")
			return nil
		})
	case "pause":
		v, e := payload[struct {
			Paused bool `json:"paused"`
		}](in.Body, "paused")
		if e != nil {
			return nil, e
		}
		return nil, m.store.update(func(d *database) error {
			w := webState(d)
			s, e := webOwned(w, user, in.ID)
			if e != nil {
				return e
			}
			if s.Status != "published" && s.Status != "paused" {
				return ErrConflict
			}
			if v.Paused {
				s.Status = "paused"
			} else {
				s.Status = "published"
			}
			s.UpdatedAt = m.now().Unix()
			w.Services[s.ID] = s
			d.audit(p, "web.pause", s.ID, "ok")
			return nil
		})
	case "delete":
		return nil, m.webDelete(p, user, in.ID)
	case "invoke", "grant", "budget":
		return nil, ErrFunds
	default:
		return nil, ErrUnavailable
	}
}

func (m *Module) webSave(p Principal, user int64, id string, in WebDraftInput) (WebServiceRecord, error) {
	if err := m.validateWebDraft(in); err != nil {
		return WebServiceRecord{}, err
	}
	creating := id == ""
	versionID, err := randomID()
	if err != nil {
		return WebServiceRecord{}, err
	}
	if id == "" {
		id, err = randomID()
		if err != nil {
			return WebServiceRecord{}, err
		}
	}
	var out WebServiceRecord
	err = m.store.update(func(d *database) error {
		w := webState(d)
		s, exists := w.Services[id]
		if exists {
			var e error
			s, e = webOwned(w, user, id)
			if e != nil {
				return e
			}
			if prior, ok := w.Versions[s.DraftVersionID]; ok && prior.Version.Status == "pending" {
				return ErrConflict
			}
		} else {
			if !creating {
				return ErrMissing
			}
			if len(w.Services) >= 10000 {
				return ErrLimit
			}
			s = WebServiceRecord{ID: id, OwnerID: user, Status: "draft", CreatedAt: m.now().Unix()}
		}
		if len(w.Versions) >= 50000 {
			return ErrLimit
		}
		v := webSnapshot{Version: WebVersion{ID: versionID, ServiceID: id, Status: "draft", Name: in.Name, Description: in.Description, ExecutionType: in.ExecutionType, Visibility: in.Visibility, Endpoint: in.Endpoint, CreatedAt: m.now().Unix(), Digest: digest(in)}, AllowedUsers: append([]int64{}, in.AllowedUsers...), Digest: digest(in), Tools: []WebTool{}}
		identities := map[string]string{}
		for _, prior := range w.Versions {
			if prior.Version.ServiceID == id {
				for _, t := range prior.Tools {
					identities[t.Name] = t.ToolID
				}
			}
		}
		for _, t := range in.Tools {
			toolID := identities[t.Name]
			if toolID == "" {
				var e error
				toolID, e = randomID()
				if e != nil {
					return e
				}
			}
			permissions, _ := json.Marshal(t.Permissions)
			output := string(t.OutputSchema)
			if output == "null" {
				output = ""
			}
			v.Tools = append(v.Tools, WebTool{ToolID: toolID, VersionID: versionID, Name: t.Name, Description: t.Description, InputSchema: string(t.InputSchema), OutputSchema: output, Permissions: string(permissions), PriceQuota: t.PriceQuota})
		}
		s.DraftVersionID = versionID
		s.UpdatedAt = m.now().Unix()
		w.Services[id] = s
		w.Versions[versionID] = v
		d.audit(p, "web.draft.save", id, "ok")
		out = s
		return nil
	})
	return out, err
}
func (m *Module) validateWebDraft(in WebDraftInput) error {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 120 || len(in.Description) > 8000 || in.ExecutionType != "remote" || len(in.Tools) < 1 || len(in.Tools) > 100 || len(in.AllowedUsers) > 100 {
		return ErrInvalid
	}
	if in.Visibility != "public" && in.Visibility != "private" && in.Visibility != "shared" || in.Visibility != "shared" && len(in.AllowedUsers) > 0 {
		return ErrInvalid
	}
	for _, id := range in.AllowedUsers {
		if id <= 0 {
			return ErrInvalid
		}
	}
	if _, err := m.net.url(in.Endpoint); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, t := range in.Tools {
		if !validTool.MatchString(t.Name) || seen[t.Name] || len(t.Description) > 4000 || t.PriceQuota < 0 || t.PriceQuota > 9007199254740991 || len(t.Permissions) > 16 {
			return ErrInvalid
		}
		seen[t.Name] = true
		if len(t.ProviderPricing) > 0 && string(t.ProviderPricing) != "null" || t.BillingMode != "" || t.InputTokenPriceQuota != 0 || t.MaxInputTokens != 0 || len(t.BillingRules) > 0 && string(t.BillingRules) != "null" && string(t.BillingRules) != "[]" {
			return ErrPrice
		}
		for i, raw := range []json.RawMessage{t.InputSchema, t.OutputSchema} {
			if i == 1 && (len(raw) == 0 || string(raw) == "null") {
				continue
			}
			if len(raw) > 16384 {
				return ErrInvalid
			}
			s, err := readSchema(raw, 0)
			if err != nil || s.Type != "object" {
				return ErrInvalid
			}
		}
		for _, permission := range t.Permissions {
			switch permission {
			case "read", "write", "delete", "send", "network", "files", "external_account":
			default:
				return ErrInvalid
			}
		}
	}
	return nil
}

func (m *Module) webRemote(ctx context.Context, endpoint string) ([]Tool, error) {
	// LegacyWeb already owns one of the module's bounded request slots.
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	c, err := m.connect(ctx, Server{Endpoint: endpoint, Auth: "none"}, Secret{})
	if err != nil {
		return nil, err
	}
	defer c.close(ctx)
	return c.tools(ctx)
}
func (m *Module) webInspect(ctx context.Context, raw json.RawMessage) (any, error) {
	v, err := payload[struct {
		Endpoint       string `json:"endpoint"`
		Authentication *struct {
			Mode   string `json:"mode"`
			Secret string `json:"secret,omitempty"`
		} `json:"authentication,omitempty"`
		ServiceID string `json:"service_id,omitempty"`
		VersionID string `json:"version_id,omitempty"`
		Preset    string `json:"preset,omitempty"`
	}](raw, "endpoint")
	if err != nil {
		return nil, err
	}
	if v.Authentication != nil && (v.Authentication.Mode != "none" || v.Authentication.Secret != "") || v.ServiceID != "" || v.VersionID != "" || v.Preset != "" {
		return nil, ErrUnavailable
	}
	tools, err := m.webRemote(ctx, v.Endpoint)
	if err != nil {
		return nil, err
	}
	out := []WebToolInput{}
	for _, t := range tools {
		out = append(out, WebToolInput{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema, OutputSchema: t.OutputSchema, Permissions: []string{"network"}, PriceQuota: 0})
	}
	return out, nil
}
func sameWebSchema(a, b string) bool {
	if a == "" || a == "null" {
		a = "null"
	}
	if b == "" || b == "null" {
		b = "null"
	}
	var av, bv any
	if decodeStrict([]byte(a), &av) != nil || decodeStrict([]byte(b), &bv) != nil {
		return false
	}
	return jsonEqual(av, bv)
}
func (m *Module) webValidate(ctx context.Context, p Principal, user int64, id, versionID string, review bool, credential, account, permission string) error {
	var captured webSnapshot
	err := m.store.view(func(d database) error {
		w := webState(&d)
		s, ok := w.Services[id]
		if !ok || s.Status == "deleted" {
			return ErrMissing
		}
		if !review && s.OwnerID != user {
			return ErrMissing
		}
		v, e := webDraft(w, s)
		if e != nil {
			return e
		}
		if versionID != "" && versionID != v.Version.ID {
			return ErrConflict
		}
		captured = v
		return nil
	})
	if err != nil {
		return err
	}
	remote, err := m.webRemote(ctx, captured.Version.Endpoint)
	if err != nil {
		return err
	}
	byName := map[string]Tool{}
	for _, t := range remote {
		byName[t.Name] = t
	}
	digests := map[string]string{}
	for _, local := range captured.Tools {
		t, ok := byName[local.Name]
		if !ok || !sameWebSchema(local.InputSchema, string(t.InputSchema)) || !sameWebSchema(local.OutputSchema, string(t.OutputSchema)) {
			return ErrConflict
		}
		digests[local.ToolID] = digest(t)
	}
	if actor, e := m.WebAuthorize(ctx, credential, account, permission); e != nil {
		return e
	} else if actor != user {
		return ErrDenied
	}
	return m.store.update(func(d *database) error {
		w := webState(d)
		s, ok := w.Services[id]
		if !ok || s.Status == "deleted" {
			return ErrMissing
		}
		if !review && s.OwnerID != user {
			return ErrMissing
		}
		current, e := webDraft(w, s)
		if e != nil {
			return e
		}
		if current.Version.ID != captured.Version.ID || current.Digest != captured.Digest {
			return ErrConflict
		}
		current.ValidationDigest = current.Digest
		current.RemoteDigests = digests
		w.Versions[current.Version.ID] = current
		d.audit(p, "web.validate", id, "ok")
		return nil
	})
}
func (m *Module) webTransition(p Principal, user int64, id, versionID, action string) error {
	return m.store.update(func(d *database) error {
		w := webState(d)
		s, e := webOwned(w, user, id)
		if e != nil {
			return e
		}
		v, ok := w.Versions[versionID]
		if action == "activate" && s.Status == "published" && s.LiveVersionID == versionID && v.Version.Status == "published" {
			return nil
		}
		if s.DraftVersionID != versionID {
			return ErrConflict
		}
		if !ok || v.Version.ServiceID != id {
			return ErrMissing
		}
		if action == "submit" {
			if v.Version.Status != "draft" && v.Version.Status != "rejected" {
				return ErrConflict
			}
			v.Version.Status = "pending"
		} else {
			if s.Status != "draft" && s.Status != "published" || v.Version.Status != "draft" {
				return ErrConflict
			}
			if v.Version.Visibility != "private" || v.ValidationDigest == "" || v.ValidationDigest != v.Digest {
				return ErrDenied
			}
			for _, t := range v.Tools {
				if t.PriceQuota != 0 || len(v.RemoteDigests[t.ToolID]) != 64 {
					return ErrDenied
				}
			}
			v.Version.Status = "published"
			v.Version.PublishedAt = m.now().Unix()
			s.LiveVersionID = versionID
			s.DraftVersionID = ""
			s.Status = "published"
		}
		s.UpdatedAt = m.now().Unix()
		w.Services[id] = s
		w.Versions[versionID] = v
		d.audit(p, "web."+action, id, "ok")
		return nil
	})
}
func (m *Module) webReview(p Principal, user int64, id, versionID string, approve bool, note string) error {
	return m.store.update(func(d *database) error {
		w := webState(d)
		s, ok := w.Services[id]
		v, found := w.Versions[versionID]
		if !ok || !found || s.Status == "deleted" || v.Version.ServiceID != id {
			return ErrMissing
		}
		if s.DraftVersionID != versionID || v.Version.Status != "pending" {
			return ErrConflict
		}
		v.Version.ReviewNote = note
		v.Version.ReviewedBy = user
		v.Version.Status = "rejected"
		if approve {
			if v.ValidationDigest == "" || v.ValidationDigest != v.Digest {
				return ErrDenied
			}
			v.Version.Status = "published"
			v.Version.PublishedAt = m.now().Unix()
			s.LiveVersionID = versionID
			s.DraftVersionID = ""
			if s.Status != "paused" {
				s.Status = "published"
			}
		}
		s.UpdatedAt = m.now().Unix()
		w.Services[id] = s
		w.Versions[versionID] = v
		d.audit(p, "web.review", id, "ok")
		return nil
	})
}
func (m *Module) webInstall(p Principal, user int64, raw json.RawMessage) error {
	v, e := payload[struct {
		ClientID  string `json:"client_id"`
		ToolID    string `json:"tool_id"`
		VersionID string `json:"version_id"`
		Loaded    bool   `json:"loaded"`
	}](raw, "client_id", "tool_id", "version_id", "loaded")
	if e != nil || strings.TrimSpace(v.ClientID) != v.ClientID || v.ClientID == "" || len(v.ClientID) > 128 || !validID.MatchString(v.ToolID) || v.Loaded && !validID.MatchString(v.VersionID) {
		return ErrInvalid
	}
	return m.store.update(func(d *database) error {
		w := webState(d)
		key := digest([]any{user, v.ClientID, v.ToolID})
		if !v.Loaded {
			delete(w.Installations, key)
			d.audit(p, "web.installation.unload", v.ToolID, "ok")
			return nil
		}
		version, ok := w.Versions[v.VersionID]
		s, found := w.Services[version.Version.ServiceID]
		if !ok || !found || !webVisible(s, version, user) {
			return ErrMissing
		}
		toolFound := false
		for _, t := range version.Tools {
			if t.ToolID == v.ToolID {
				toolFound = true
			}
		}
		if !toolFound {
			return ErrMissing
		}
		if len(w.Installations) >= 20000 {
			return ErrLimit
		}
		w.Installations[key] = WebInstallation{UserID: user, ClientID: v.ClientID, ToolID: v.ToolID, VersionID: v.VersionID, CreatedAt: m.now().Unix()}
		d.audit(p, "web.installation.load", v.ToolID, "ok")
		return nil
	})
}
func (m *Module) webMine(user int64, kind string, offset, limit int) (any, error) {
	if !validWebPage(offset, limit) {
		return nil, ErrInvalid
	}
	var result any
	err := m.store.view(func(d database) error {
		w := webState(&d)
		switch kind {
		case "services":
			type named struct {
				WebServiceRecord
				Name string `json:"name"`
			}
			rows := []named{}
			for _, s := range w.Services {
				if s.OwnerID != user || s.Status == "deleted" {
					continue
				}
				v, e := webDraft(w, s)
				if e != nil {
					return e
				}
				rows = append(rows, named{s, v.Version.Name})
			}
			sort.Slice(rows, func(i, j int) bool {
				if rows[i].CreatedAt != rows[j].CreatedAt {
					return rows[i].CreatedAt > rows[j].CreatedAt
				}
				return rows[i].ID < rows[j].ID
			})
			result = webSlice(rows, offset, limit)
		case "installations":
			rows := []WebInstallation{}
			for _, i := range w.Installations {
				if i.UserID == user {
					rows = append(rows, i)
				}
			}
			sort.Slice(rows, func(i, j int) bool {
				if rows[i].ClientID != rows[j].ClientID {
					return rows[i].ClientID < rows[j].ClientID
				}
				return rows[i].ToolID < rows[j].ToolID
			})
			result = webSlice(rows, offset, limit)
		case "favorites":
			type favorite struct {
				WebSummary
				at int64
			}
			rows := []favorite{}
			for _, s := range w.Services {
				at, ok := w.Favorites[digest([]any{user, s.ID})]
				v, found := w.Versions[s.LiveVersionID]
				if ok && found && webVisible(s, v, user) {
					rows = append(rows, favorite{webSummary(s, v), at})
				}
			}
			sort.Slice(rows, func(i, j int) bool {
				if rows[i].at != rows[j].at {
					return rows[i].at > rows[j].at
				}
				return rows[i].ID < rows[j].ID
			})
			items := []WebSummary{}
			for _, row := range rows {
				items = append(items, row.WebSummary)
			}
			result = webSlice(items, offset, limit)
		case "grants", "tokens", "budgets":
			return ErrUnavailable
		default:
			return ErrInvalid
		}
		return nil
	})
	return result, err
}
func (m *Module) webReviews(offset, limit int) (any, error) {
	if !validWebPage(offset, limit) {
		return nil, ErrInvalid
	}
	rows := []WebServiceRecord{}
	err := m.store.view(func(d database) error {
		w := webState(&d)
		for _, s := range w.Services {
			v, ok := w.Versions[s.DraftVersionID]
			if s.Status != "deleted" && ok && v.Version.Status == "pending" {
				rows = append(rows, s)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UpdatedAt != rows[j].UpdatedAt {
			return rows[i].UpdatedAt > rows[j].UpdatedAt
		}
		return rows[i].ID < rows[j].ID
	})
	return webSlice(rows, offset, limit), nil
}
func (m *Module) webDelete(p Principal, user int64, id string) error {
	return m.store.update(func(d *database) error {
		w := webState(d)
		s, e := webOwned(w, user, id)
		if e != nil {
			return e
		}
		s.Status = "deleted"
		s.LiveVersionID = ""
		s.DraftVersionID = ""
		s.UpdatedAt = m.now().Unix()
		w.Services[id] = s
		tools := map[string]bool{}
		for _, v := range w.Versions {
			if v.Version.ServiceID == id {
				for _, t := range v.Tools {
					tools[t.ToolID] = true
				}
			}
		}
		for key, i := range w.Installations {
			if tools[i.ToolID] {
				delete(w.Installations, key)
			}
		}
		d.audit(p, "web.delete", id, "ok")
		return nil
	})
}
