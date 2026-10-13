// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package assistant

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/storage"
)

var greetingLanguages = [...]string{"en", "zh", "zh-TW", "fr", "ja", "ru", "vi"}
var greetingVariable = regexp.MustCompile(`\$\$|\$[A-Za-z_][A-Za-z0-9_]*`)

const greetingRecordID = "overview-greeting"

type GreetingPreference struct {
	Templates map[string]string `json:"templates"`
	Revision  int64             `json:"revision"`
	UpdatedAt int64             `json:"updated_at"`
}

type greetingVersion struct {
	Revision  int64 `json:"revision"`
	UpdatedAt int64 `json:"updated_at"`
}

// Each locale has its own bounded record: seven maximum-length templates can
// exceed records' 16 KiB limit after JSON escaping. The version and all locales
// are read/written under the same existing account-scope transaction lock.
type greetingTemplate struct {
	Template *string `json:"template"`
}

func validGreetingTemplate(language, template string) bool {
	known := false
	for _, supported := range greetingLanguages {
		known = known || language == supported
	}
	if !known || !utf8.ValidString(template) || utf8.RuneCountInString(template) > 512 || strings.ContainsRune(template, 0) {
		return false
	}
	for _, variable := range greetingVariable.FindAllString(template, -1) {
		switch variable {
		case "$$", "$name", "$time", "$date", "$weekday", "$level", "$site", "$balance":
		default:
			return false
		}
	}
	return true
}

func readGreeting(tx storage.Tx) (GreetingPreference, error) {
	value := GreetingPreference{Templates: map[string]string{}}
	var version greetingVersion
	err := tx.Get("preferences", greetingRecordID, &version)
	if errors.Is(err, access.ErrNotFound) {
		// Absence is the domain's initial revision; defaults are localized by Web.
		return value, nil
	}
	if err != nil {
		return value, err
	}
	if version.Revision < 1 || version.Revision > maxSafeHistoryID || version.UpdatedAt <= 0 || version.UpdatedAt > maxSafeHistoryID {
		return value, access.ErrUnavailable
	}
	value.Revision, value.UpdatedAt = version.Revision, version.UpdatedAt
	for _, language := range greetingLanguages {
		var stored greetingTemplate
		err = tx.Get("preferences", greetingRecordID+":"+language, &stored)
		if errors.Is(err, access.ErrNotFound) {
			continue
		}
		if err != nil {
			return value, err
		}
		if stored.Template == nil || !validGreetingTemplate(language, *stored.Template) {
			return value, access.ErrUnavailable
		}
		if *stored.Template != "" {
			value.Templates[language] = *stored.Template
		}
	}
	return value, nil
}

func (s *HistoryService) Greeting(ctx context.Context, token string) (GreetingPreference, error) {
	p, err := s.actor(ctx, token)
	if err != nil {
		return GreetingPreference{}, err
	}
	repo, ok := s.repo.(storage.Repository)
	if !ok || access.Missing(repo) {
		return GreetingPreference{}, access.ErrUnavailable
	}
	var value GreetingPreference
	err = repo.Within(ctx, p.Account.Key(), func(tx storage.Tx) error {
		var err error
		value, err = readGreeting(tx)
		return err
	})
	return value, err
}

// UpdateGreeting is a manual personal preference, not model generation. A
// replay with an old revision conflicts, including resets and identical text.
func (s *HistoryService) UpdateGreeting(ctx context.Context, token, language, template string, revision int64) (GreetingPreference, error) {
	p, err := s.actor(ctx, token)
	if err != nil {
		return GreetingPreference{}, err
	}
	if revision < 0 || revision > maxSafeHistoryID || !validGreetingTemplate(language, template) {
		return GreetingPreference{}, access.ErrInvalid
	}
	repo, ok := s.repo.(storage.Repository)
	if !ok || access.Missing(repo) {
		return GreetingPreference{}, access.ErrUnavailable
	}
	var value GreetingPreference
	err = repo.Within(ctx, p.Account.Key(), func(tx storage.Tx) error {
		var err error
		value, err = readGreeting(tx)
		if err != nil {
			return err
		}
		if value.Revision != revision {
			return access.ErrConflict
		}
		if value.Revision == maxSafeHistoryID {
			return access.ErrLimit
		}
		if strings.TrimSpace(template) == "" {
			template = ""
			delete(value.Templates, language)
		} else {
			value.Templates[language] = template
		}
		value.Revision++
		value.UpdatedAt = s.now().Unix()
		if value.UpdatedAt <= 0 || value.UpdatedAt > maxSafeHistoryID {
			return access.ErrUnavailable
		}
		if err = tx.Put("preferences", greetingRecordID+":"+language, greetingTemplate{&template}); err != nil {
			return err
		}
		return tx.Put("preferences", greetingRecordID, greetingVersion{value.Revision, value.UpdatedAt})
	})
	return value, err
}
