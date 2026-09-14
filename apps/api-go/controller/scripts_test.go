package controller

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateScriptName(t *testing.T) {
	for _, name := range []string{"deploy.sh", "check.zsh", "build.ps1"} {
		if err := validateScriptName(name); err != nil {
			t.Errorf("validateScriptName(%q) = %v", name, err)
		}
	}
	for _, name := range []string{"../deploy.sh", "deploy", ".env", "deploy.php", "a/b.sh"} {
		if err := validateScriptName(name); err == nil {
			t.Errorf("validateScriptName(%q) accepted unsafe name", name)
		}
	}
}

func TestScriptsDirectoryUsesEnvironment(t *testing.T) {
	previous := os.Getenv("SCRIPTS_DIR")
	defer os.Setenv("SCRIPTS_DIR", previous)
	_ = os.Setenv("SCRIPTS_DIR", filepath.Join(t.TempDir(), "scripts"))
	if got := scriptsDirectory(); got != os.Getenv("SCRIPTS_DIR") {
		t.Fatalf("scriptsDirectory() = %q", got)
	}
}
