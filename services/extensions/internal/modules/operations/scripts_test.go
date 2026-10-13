// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package operations

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reviewed(name string, data []byte) ReviewedScript {
	h := sha256.Sum256(data)
	return ReviewedScript{name, hex.EncodeToString(h[:])}
}
func TestLoadScriptsReviewedImmutableSnapshot(t *testing.T) {
	root := t.TempDir()
	data := []byte("#!/bin/sh\r\nprintf '<reviewed>'\n\x00")
	if err := os.WriteFile(filepath.Join(root, "install.sh"), data, 0600); err != nil {
		t.Fatal(err)
	}
	c := ScriptConfig{root, []ReviewedScript{reviewed("install.sh", data)}}
	s, err := LoadScripts(c)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, readErr := os.ReadFile(filepath.Join(root, "install.sh"))
	entries, listErr := os.ReadDir(root)
	if readErr != nil || listErr != nil || !bytes.Equal(unchanged, data) || len(entries) != 1 {
		t.Fatal("loader mutated filesystem")
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].Size != len(data) {
		t.Fatalf("%v %v", list, err)
	}
	list[0].Name = "mutated"
	out, _ := s.Raw("install.sh")
	out[0] = 'x'
	if err = os.WriteFile(filepath.Join(root, "install.sh"), []byte("unreviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	actual, _ := s.Raw("install.sh")
	if !bytes.Equal(actual, data) {
		t.Fatal("snapshot or caller mutation")
	}
	if _, err = LoadScripts(c); err == nil {
		t.Fatal("hash mismatch accepted")
	}
	if _, err = s.Raw("missing.sh"); err != ErrNotFound {
		t.Fatal(err)
	}
	for _, name := range []string{"../install.sh", "/install.sh", "x/../install.sh", "a\\install.sh", ".sh", "install.SH", "a.sh%00", "a.sh\x00", "a.sh/", "a b.sh", "a.exe"} {
		if _, err = s.Raw(name); err != ErrInvalid {
			t.Fatalf("accepted %q", name)
		}
	}
}
func TestLoadScriptsRejectsUnavailableAndSymlinks(t *testing.T) {
	root := t.TempDir()
	data := []byte("reviewed")
	if err := os.WriteFile(filepath.Join(root, "real.sh"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.sh", filepath.Join(root, "link.sh")); err != nil {
		t.Fatal(err)
	}
	cases := []ScriptConfig{{}, {root, nil}, {filepath.Join(root, "missing"), []ReviewedScript{}}, {root, []ReviewedScript{reviewed("link.sh", data)}}, {root, []ReviewedScript{reviewed("missing.sh", data)}}, {root, []ReviewedScript{reviewed("../real.sh", data)}}, {root, []ReviewedScript{{"real.sh", "bad"}}}, {root, []ReviewedScript{reviewed("real.sh", data), reviewed("real.sh", data)}}}
	parent := t.TempDir()
	if err := os.Symlink(root, filepath.Join(parent, "link")); err != nil {
		t.Fatal(err)
	}
	cases = append(cases, ScriptConfig{filepath.Join(parent, "link"), []ReviewedScript{}})
	for i, c := range cases {
		if _, err := LoadScripts(c); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
	s, err := LoadScripts(ScriptConfig{root, []ReviewedScript{}})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if list == nil || len(list) != 0 {
		t.Fatal("reviewed empty catalogue not preserved")
	}
	if _, err = (*Scripts)(nil).List(); err != ErrUnavailable {
		t.Fatal(err)
	}
}
func TestLoadScriptsBounds(t *testing.T) {
	root := t.TempDir()
	data := []byte(strings.Repeat("x", MaxScriptBytes+1))
	if err := os.WriteFile(filepath.Join(root, "big.sh"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadScripts(ScriptConfig{root, []ReviewedScript{reviewed("big.sh", data)}}); err == nil {
		t.Fatal("unbounded file")
	}
	if _, err := LoadScripts(ScriptConfig{root, make([]ReviewedScript, MaxScripts+1)}); err != ErrInvalid {
		t.Fatal(err)
	}
	manifest := []ReviewedScript{}
	data = data[:MaxScriptBytes]
	for i := 0; i < 33; i++ {
		name := strings.Repeat("x", i+1) + ".sh"
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
		manifest = append(manifest, reviewed(name, data))
	}
	if _, err := LoadScripts(ScriptConfig{root, manifest}); err != ErrInvalid {
		t.Fatal("unbounded snapshot", err)
	}
}
func TestPublicationValidation(t *testing.T) {
	for _, tc := range [][2]string{{"../v1", "ok"}, {" v1", "ok"}, {strings.Repeat("v", 65), "ok"}, {"v1", ""}, {"v1", " \n "}, {"v1", strings.Repeat("x", MaxContent+1)}, {"v1", "\xff"}, {"v1", "a\x00b"}} {
		if validPublication(tc[0], tc[1]) {
			t.Fatalf("accepted %q", tc)
		}
	}
	if !validPublication("v1.2.3-rc+2", "<b>notes</b>") {
		t.Fatal("valid content rejected")
	}
}
