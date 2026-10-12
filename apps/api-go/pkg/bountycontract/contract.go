// Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later
// Package bountycontract defines the shared rules for all bounty entry points.
// It has no database, transport, or payment dependencies.
package bountycontract

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	General    = "general"
	OpenSource = "open_source"
	Individual = "individual"
	Company    = "company"
	// Last representable second in a four-digit calendar year. Zero is unlimited.
	MaxDeadline int64 = 253402300799
)

func Kind(value, repository string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		// Legacy callers send only repository_url. Do not silently change their contract.
		if strings.TrimSpace(repository) != "" {
			return OpenSource, nil
		}
		return General, nil
	}
	if value != General && value != OpenSource {
		return "", errors.New("kind must be general or open_source")
	}
	return value, nil
}

func PublisherType(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Individual, nil
	}
	if value != Individual && value != Company {
		return "", errors.New("publisher_type must be individual or company")
	}
	return value, nil
}

// RecruitmentOpen does not govern submissions or payment for previously accepted work.
func RecruitmentOpen(deadline, now int64) bool {
	return deadline == 0 || deadline > now && deadline <= MaxDeadline
}

func ValidText(value string, min, max int) bool {
	if !utf8.ValidString(value) {
		return false
	}
	n := utf8.RuneCountInString(strings.TrimSpace(value))
	return n >= min && n <= max
}

// DeliveryURL validates a stored link only. Callers must never fetch it automatically.
func DeliveryURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > 2048 || !utf8.ValidString(raw) || strings.ContainsAny(raw, "\\") || strings.ContainsFunc(raw, unicode.IsControl) {
		return "", errors.New("delivery_url must be a valid HTTPS URL of at most 2048 bytes")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || strings.ContainsAny(u.Host, " \t\r\n") {
		return "", errors.New("delivery_url must be an absolute HTTPS URL without credentials")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 0 || n > 65535 {
			return "", errors.New("delivery URL port is invalid")
		}
	}
	normalized := u.String()
	if len(normalized) > 2048 {
		return "", errors.New("encoded delivery URL exceeds 2048 bytes")
	}
	return normalized, nil
}

func ValidateDelivery(link, note string) (string, string, error) {
	link, err := DeliveryURL(link)
	if err != nil {
		return "", "", err
	}
	note = strings.TrimSpace(note)
	if !ValidText(note, 0, 2000) {
		return "", "", errors.New("completion note must contain at most 2000 characters")
	}
	if link == "" && !ValidText(note, 20, 2000) {
		return "", "", errors.New("provide a delivery URL or a completion note of at least 20 characters")
	}
	return link, note, nil
}

// CanonicalToolName keeps old clients working without doubling tools/list.
func CanonicalToolName(name string) string {
	if strings.HasPrefix(name, "open_source_bounties.") {
		return "bounties." + strings.TrimPrefix(name, "open_source_bounties.")
	}
	return name
}
