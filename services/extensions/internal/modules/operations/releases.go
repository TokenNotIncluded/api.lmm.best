// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
// Package operations owns immutable operational content, never core records.
package operations

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("invalid operations input")
	ErrUnavailable = errors.New("operations unavailable")
	ErrNotFound    = errors.New("operations content not found")
	ErrCapacity    = errors.New("operations publication capacity reached")
	ErrUnsupported = errors.New("script repository management unsupported")
)

const MaxID int64 = 9007199254740991
const MaxPublications = 10000
const MaxContent = 32768

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

type ReleaseNote struct {
	ID          int64  `json:"id"`
	Version     string `json:"version"`
	Revision    int    `json:"revision"`
	Content     string `json:"content"`
	PublishedAt int64  `json:"published_at"`
	PublishedBy int64  `json:"published_by"`
}

type Releases struct{ db *sql.DB }

func validID(id int64) bool { return id > 0 && id <= MaxID }
func validPublication(version, content string) bool {
	return versionPattern.MatchString(version) && utf8.ValidString(content) && !strings.ContainsRune(content, 0) && len(content) <= MaxContent && strings.TrimSpace(content) != ""
}

const noteColumns = "id,version,revision,content,published_at,published_by"

type scanner interface{ Scan(...any) error }

func scanNote(row scanner) (*ReleaseNote, error) {
	n := new(ReleaseNote)
	err := row.Scan(&n.ID, &n.Version, &n.Revision, &n.Content, &n.PublishedAt, &n.PublishedBy)
	return n, err
}
func (s *Releases) Latest(ctx context.Context, user int64) (*ReleaseNote, error) {
	if !validID(user) {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := scanNote(s.db.QueryRowContext(ctx, "SELECT "+noteColumns+" FROM lmm_operations.publications p WHERE NOT EXISTS (SELECT 1 FROM lmm_operations.reads r WHERE r.user_id=$1 AND r.note_id=p.id AND r.revision=p.revision) ORDER BY id DESC LIMIT 1", user))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	return n, nil
}
func (s *Releases) List(ctx context.Context, limit int) ([]ReleaseNote, error) {
	if limit < 1 || limit > 50 {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, "SELECT "+noteColumns+" FROM lmm_operations.publications ORDER BY id DESC LIMIT $1", limit)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	out := []ReleaseNote{}
	for rows.Next() {
		n, e := scanNote(rows)
		if e != nil {
			return nil, ErrUnavailable
		}
		out = append(out, *n)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return out, nil
}

// Publish serializes publications across processes. Exact replay is relative to
// the latest revision of this version, not an older matching content hash.
func (s *Releases) Publish(ctx context.Context, user int64, version, content string) (*ReleaseNote, error) {
	if !validID(user) || !validPublication(version, content) {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(746382910)"); err != nil {
		return nil, ErrUnavailable
	}
	old, err := scanNote(tx.QueryRowContext(ctx, "SELECT "+noteColumns+" FROM lmm_operations.publications WHERE version=$1 ORDER BY revision DESC LIMIT 1", version))
	revision := 1
	if err == nil {
		if old.Content == content {
			if tx.Commit() != nil {
				return nil, ErrUnavailable
			}
			return old, nil
		}
		revision = old.Revision + 1
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnavailable
	}
	var count int
	if tx.QueryRowContext(ctx, "SELECT count(*) FROM lmm_operations.publications").Scan(&count) != nil {
		return nil, ErrUnavailable
	}
	if count >= MaxPublications {
		return nil, ErrCapacity
	}
	n, err := scanNote(tx.QueryRowContext(ctx, "INSERT INTO lmm_operations.publications(version,revision,content,published_at,published_by) VALUES($1,$2,$3,$4,$5) RETURNING "+noteColumns, version, revision, content, time.Now().Unix(), user))
	if err != nil || tx.Commit() != nil {
		return nil, ErrUnavailable
	}
	return n, nil
}
func (s *Releases) Read(ctx context.Context, user, id int64) error {
	if !validID(user) || !validID(id) {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result, err := s.db.ExecContext(ctx, "INSERT INTO lmm_operations.reads(user_id,note_id,revision) SELECT $1,id,revision FROM lmm_operations.publications WHERE id=$2 ON CONFLICT DO NOTHING", user, id)
	if err != nil {
		return ErrUnavailable
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ErrUnavailable
	}
	if affected == 0 {
		var exists bool
		if s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM lmm_operations.publications WHERE id=$1)", id).Scan(&exists) != nil {
			return ErrUnavailable
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}
