package appcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

type candidateFrontendPackageRunner struct {
	t        *testing.T
	contents []byte
}

func (runner candidateFrontendPackageRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	runner.t.Helper()
	if command.Name != commandBsdtar {
		runner.t.Fatalf("command=%q, want bsdtar", command.Name)
	}
	if len(command.Args) != 3 || command.Args[0] != "-xOf" || command.Args[1] != "/candidate.pkg.tar.zst" || command.Args[2] != productionPackagedFrontendIndex {
		runner.t.Fatalf("arguments=%q", command.Args)
	}
	return runner.contents, nil
}

func TestCandidateFrontendIndexSHA256ReadsCandidatePackage(t *testing.T) {
	contents := []byte("candidate frontend")
	runtime := &productionRuntime{runner: candidateFrontendPackageRunner{t: t, contents: contents}}

	got, err := runtime.candidateFrontendIndexSHA256(context.Background(), "/candidate.pkg.tar.zst")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	want := hex.EncodeToString(digest[:])
	if got != want {
		t.Fatalf("candidateFrontendIndexSHA256()=%q, want %q", got, want)
	}
}
