package appcli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionMerchantStoreSealedStartHookOnlyAcceptsExactNativeChecker(t *testing.T) {
	binary := "/usr/bin/lmm-api"
	workspace := filepath.Join(defaultProductionPaths().WorkRoot, "release-start-test")
	good := "{ path=" + binary + " ; argv[]=" + binary + " operator production writer-start-check --workspace " + workspace + " ; ignore_errors=no ; }"
	if actual, err := merchantStoreSealedStartCommand(good, binary); err != nil || actual != binary+"\x00"+binary+" operator production writer-start-check --workspace "+workspace+"\x00no" {
		t.Fatalf("canonical native hook was rejected: %q %v", actual, err)
	}
	for name, command := range map[string]string{
		"shell":               strings.ReplaceAll(good, binary, "/bin/sh"),
		"alternate binary":    strings.ReplaceAll(good, binary, "/tmp/lmm-api"),
		"write operation":     strings.Replace(good, "writer-start-check", "apply", 1),
		"override":            strings.Replace(good, " ; ignore_errors", " --skip-writer-check ; ignore_errors", 1),
		"ignore errors":       strings.Replace(good, "ignore_errors=no", "ignore_errors=yes", 1),
		"unowned workspace":   strings.Replace(good, workspace, "/tmp/release-start-test", 1),
		"relative workspace":  strings.Replace(good, workspace, "work/release-start-test", 1),
		"traversal workspace": strings.Replace(good, workspace, defaultProductionPaths().WorkRoot+"/../release-start-test", 1),
		"duplicate command":   good + "\n" + good,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := merchantStoreSealedStartCommand(command, binary); err == nil {
				t.Fatal("unsafe lifecycle hook was accepted")
			}
		})
	}
}
