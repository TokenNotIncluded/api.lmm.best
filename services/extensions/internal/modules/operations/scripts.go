// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"time"
)

const MaxScripts = 128
const MaxScriptBytes = 256 << 10
const MaxSnapshotBytes = 8 << 20

var scriptName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,95}\.(sh|zsh|ps1)$`)

func ValidScriptName(name string) bool { return scriptName.MatchString(name) }

type ReviewedScript struct {
	Name   string
	SHA256 string
}

// A nil manifest is unconfigured; a non-nil empty manifest is an explicitly
// reviewed empty catalogue, still requiring an available non-symlink root.
type ScriptConfig struct {
	Root     string
	Manifest []ReviewedScript
}
type ScriptMeta struct {
	Name    string `json:"name"`
	Size    int    `json:"size"`
	Updated string `json:"updated"`
}
type scriptSnapshot struct {
	meta ScriptMeta
	data []byte
}
type Scripts struct {
	entries   map[string]scriptSnapshot
	catalogue []ScriptMeta
}

// LoadScripts snapshots only the installation-reviewed manifest, verifies every
// hash and bounds before returning anything. It never executes or fetches files.
func LoadScripts(c ScriptConfig) (*Scripts, error) {
	if c.Root == "" || c.Manifest == nil {
		return nil, ErrUnavailable
	}
	if len(c.Manifest) > MaxScripts {
		return nil, ErrInvalid
	}
	path, err := filepath.Abs(c.Root)
	if err != nil {
		return nil, ErrInvalid
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnavailable
	}
	// Reject symlinks in every configured root component, not only the basename.
	for p := path; ; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e != nil || i.Mode()&os.ModeSymlink != 0 {
			return nil, ErrUnavailable
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return nil, ErrUnavailable
	}
	s := &Scripts{entries: map[string]scriptSnapshot{}, catalogue: []ScriptMeta{}}
	total := 0
	for _, review := range c.Manifest {
		if !ValidScriptName(review.Name) {
			return nil, ErrInvalid
		}
		if _, exists := s.entries[review.Name]; exists {
			return nil, ErrInvalid
		}
		expected, e := hex.DecodeString(review.SHA256)
		if e != nil || len(expected) != sha256.Size || review.SHA256 != hex.EncodeToString(expected) {
			return nil, ErrInvalid
		}
		before, e := root.Lstat(review.Name)
		if e != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() > MaxScriptBytes {
			return nil, ErrUnavailable
		}
		// Never follow a raced leaf symlink or block on a replaced FIFO.
		f, e := root.OpenFile(review.Name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return nil, ErrUnavailable
		}
		current, e := f.Stat()
		if e != nil || !os.SameFile(before, current) {
			f.Close()
			return nil, ErrUnavailable
		}
		data, e := io.ReadAll(io.LimitReader(f, MaxScriptBytes+1))
		closeErr := f.Close()
		after, statErr := root.Lstat(review.Name)
		if e != nil || closeErr != nil || statErr != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || len(data) > MaxScriptBytes {
			return nil, ErrUnavailable
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != review.SHA256 {
			return nil, ErrUnavailable
		}
		total += len(data)
		if total > MaxSnapshotBytes {
			return nil, ErrInvalid
		}
		meta := ScriptMeta{review.Name, len(data), before.ModTime().UTC().Format(time.RFC3339Nano)}
		s.entries[review.Name] = scriptSnapshot{meta, data}
		s.catalogue = append(s.catalogue, meta)
	}
	sort.Slice(s.catalogue, func(i, j int) bool { return s.catalogue[i].Name < s.catalogue[j].Name })
	return s, nil
}
func (s *Scripts) List() ([]ScriptMeta, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	return append([]ScriptMeta{}, s.catalogue...), nil
}
func (s *Scripts) Raw(name string) ([]byte, error) {
	if !ValidScriptName(name) {
		return nil, ErrInvalid
	}
	if s == nil {
		return nil, ErrUnavailable
	}
	entry, ok := s.entries[name]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), entry.data...), nil
}

type Config struct {
	Releases *sql.DB
	Scripts  *ScriptConfig
}
type Service struct {
	Releases *Releases
	Scripts  *Scripts
}

// New assembles requested content only; any configured dependency failure fails
// construction. The caller retains ownership of any supplied pool, including
// on failure. A missing component is unavailable, not a successful empty DTO.
func New(ctx context.Context, c Config) (*Service, error) {
	s := new(Service)
	var err error
	if c.Releases != nil {
		s.Releases, err = NewPostgres(ctx, c.Releases)
		if err != nil {
			return nil, err
		}
	}
	if c.Scripts != nil {
		s.Scripts, err = LoadScripts(*c.Scripts)
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}

type Availability struct {
	ReleaseNotes     bool `json:"release_notes"`
	ScriptCatalogue  bool `json:"script_catalogue"`
	ScriptRepository bool `json:"script_repository"`
}

func (s *Service) Availability() Availability {
	if s == nil {
		return Availability{}
	}
	return Availability{s.Releases != nil, s.Scripts != nil, false}
}
