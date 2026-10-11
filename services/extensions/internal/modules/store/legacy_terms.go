// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type LegacyCheckoutPolicy struct {
	Version  string
	Accepted bool
}
type legacySellerTerms struct {
	Version   string `json:"version"`
	Content   string `json:"content"`
	UpdatedAt int64  `json:"updated_at"`
}
type legacyTermsAcceptance struct {
	Version    string `json:"version"`
	UserID     int64  `json:"user_id"`
	AcceptedAt int64  `json:"accepted_at"`
}

func validTermsContent(s string) bool { return utf8.ValidString(s) && Text(s, 64<<10) }
func readLegacyTerms(tx Tx) (*legacySellerTerms, error) {
	var row legacySellerTerms
	err := tx.Get("commands", "@web:seller-terms", &row)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !validTermsContent(row.Content) || len(row.Version) != 36 || row.UpdatedAt <= 0 {
		return nil, ErrUnavailable
	}
	return &row, nil
}
func legacyTermsView(tx Tx, userID int64) (map[string]any, error) {
	row, err := readLegacyTerms(tx)
	if err != nil {
		return nil, err
	}
	view := map[string]any{"version": "", "content": "", "required": true, "configured": false, "accepted": false, "updated_at": 0}
	if row == nil {
		return view, nil
	}
	view["version"] = row.Version
	view["content"] = row.Content
	view["configured"] = true
	view["updated_at"] = row.UpdatedAt
	if userID > 0 {
		var a legacyTermsAcceptance
		err := tx.Get("commands", ID("legacy-seller-terms-v1", userID, row.Version), &a)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if err == nil {
			if a.UserID != userID || a.Version != row.Version || a.AcceptedAt <= 0 {
				return nil, ErrUnavailable
			}
			view["accepted"] = true
		}
	}
	return view, nil
}
func (s *Service) legacyTerms(ctx context.Context, actor Account, r LegacyRequest) (any, error) {
	shopID := ID("legacy-web-shop-v1", actor)
	var out map[string]any
	err := s.repo.Within(ctx, shopID, func(tx Tx) error {
		if r.Action == "save_terms" {
			var in struct {
				Content         string `json:"content"`
				ExpectedVersion string `json:"expected_version"`
			}
			if legacyDecode(r.Body, &in) != nil {
				return ErrInvalid
			}
			in.Content = strings.TrimSpace(in.Content)
			if !validTermsContent(in.Content) || len(in.ExpectedVersion) > 36 {
				return ErrInvalid
			}
			row, err := readLegacyTerms(tx)
			if err != nil {
				return err
			}
			version := ""
			if row != nil {
				version = row.Version
			}
			if version != in.ExpectedVersion {
				return ErrConflict
			}
			if row == nil || row.Content != in.Content {
				id, err := legacyRandomID()
				if err != nil {
					return err
				}
				row = &legacySellerTerms{Version: id, Content: in.Content, UpdatedAt: time.Now().Unix()}
				if err := tx.Put("commands", "@web:seller-terms", row); err != nil {
					return err
				}
			}
		}
		var err error
		out, err = legacyTermsView(tx, 0)
		return err
	})
	return out, err
}
func (s *Service) PublicProductTerms(ctx context.Context, userID int64, id string) (map[string]any, error) {
	if userID < 0 {
		return nil, ErrInvalid
	}
	row, err := s.LocateProduct(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err = s.publicLegacyView(row, true); err != nil {
		return nil, err
	}
	var out map[string]any
	err = s.repo.Within(ctx, row.ShopID, func(tx Tx) error { var err error; out, err = legacyTermsView(tx, userID); return err })
	return out, err
}
func freezeLegacyTerms(tx Tx, userID int64, key string, p *LegacyCheckoutPolicy) (*legacySellerTerms, int64, error) {
	row, err := readLegacyTerms(tx)
	if err != nil {
		return nil, 0, err
	}
	if row == nil {
		return nil, 0, ErrSellerTermsNotConfigured
	}
	if p.Version != row.Version {
		return nil, 0, &TermsUpdatedError{RequestKey: key}
	}
	id := ID("legacy-seller-terms-v1", userID, row.Version)
	var a legacyTermsAcceptance
	err = tx.Get("commands", id, &a)
	if err == nil {
		if a.UserID != userID || a.Version != row.Version || a.AcceptedAt <= 0 {
			return nil, 0, ErrUnavailable
		}
		return row, a.AcceptedAt, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, 0, err
	}
	if !p.Accepted {
		return nil, 0, ErrSellerTermsRequired
	}
	a = legacyTermsAcceptance{Version: row.Version, UserID: userID, AcceptedAt: time.Now().Unix()}
	if err = tx.Put("commands", id, a); err != nil {
		return nil, 0, err
	}
	return row, a.AcceptedAt, nil
}
