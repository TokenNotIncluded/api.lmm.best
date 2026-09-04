package appcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type remoteBackupVerificationRunner struct {
	root       string
	digests    map[string]string
	members    []string
	unsafeName string
}

func (runner remoteBackupVerificationRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	if command.Name != commandSSH || len(command.Args) < 4 {
		return nil, errors.New("unexpected remote backup command")
	}
	remote := command.Args[3:]
	switch {
	case len(remote) == 3 && remote[0] == "test" && remote[1] == "-d":
		return nil, nil
	case len(remote) == 4 && remote[0] == "test" && remote[1] == "!" && remote[2] == "-L":
		return nil, nil
	case len(remote) == 7 && remote[0] == "find":
		paths := make([]string, 0, len(runner.members))
		for _, name := range runner.members {
			paths = append(paths, filepath.Join(runner.root, name))
		}
		return []byte(strings.Join(paths, "\n") + "\n"), nil
	case len(remote) == 5 && remote[0] == "stat" && remote[1] == "-c":
		if remote[2] == "%U:%a:%F" {
			return []byte("arch:700:directory\n"), nil
		}
		name := filepath.Base(remote[4])
		fileType := "regular file"
		if name == runner.unsafeName {
			fileType = "symbolic link"
		}
		return []byte("arch:600:1:128:" + fileType + "\n"), nil
	case len(remote) == 3 && remote[0] == "sha256sum" && remote[1] == "--":
		digest, ok := runner.digests[remote[2]]
		if !ok {
			return nil, errors.New("missing remote digest fixture")
		}
		return []byte(digest + "  " + remote[2] + "\n"), nil
	default:
		return nil, fmt.Errorf("unexpected remote backup command: %v", remote)
	}
}

func writeExternalBackupFixture(t *testing.T, root string) (map[string]string, string) {
	t.Helper()
	digests := make(map[string]string)
	var sums strings.Builder
	for _, name := range externalBackupMemberNames() {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("fixture-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
		digest, err := sha256File(path)
		if err != nil {
			t.Fatal(err)
		}
		digests[name] = digest
		_, _ = fmt.Fprintf(&sums, "%s  %s\n", digest, name)
	}
	checksumPath := filepath.Join(root, "SHA256SUMS")
	if err := os.WriteFile(checksumPath, []byte(sums.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	checksumDigest, err := sha256File(checksumPath)
	if err != nil {
		t.Fatal(err)
	}
	digests["SHA256SUMS"] = checksumDigest
	return digests, checksumDigest
}

func TestRemoteExternalBackupVerificationChecksEveryMember(t *testing.T) {
	local := t.TempDir()
	localDigests, checksumDigest := writeExternalBackupFixture(t, local)
	remoteRoot := "/home/arch/.local/state/lmm-api-production-backups/deploy-test"
	remoteDigests := make(map[string]string, len(localDigests))
	members := append(externalBackupMemberNames(), "SHA256SUMS")
	for name, digest := range localDigests {
		remoteDigests[filepath.Join(remoteRoot, name)] = digest
	}
	runtime := &productionReleaseRuntime{runner: remoteBackupVerificationRunner{
		root: remoteRoot, digests: remoteDigests, members: members,
	}}

	if err := runtime.verifyRemoteExternalBackupCopy(context.Background(), productionOffhostAlias, remoteRoot, local, "arch", checksumDigest); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteExternalBackupVerificationRejectsMissingTamperedOrLinkedMember(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func([]string, map[string]string, string) ([]string, map[string]string, string)
	}{
		{
			name: "missing member",
			mutate: func(members []string, digests map[string]string, _ string) ([]string, map[string]string, string) {
				return members[1:], digests, ""
			},
		},
		{
			name: "tampered member",
			mutate: func(members []string, digests map[string]string, root string) ([]string, map[string]string, string) {
				digests[filepath.Join(root, "database.age")] = strings.Repeat("f", 64)
				return members, digests, ""
			},
		},
		{
			name: "linked member",
			mutate: func(members []string, digests map[string]string, _ string) ([]string, map[string]string, string) {
				return members, digests, "database.age"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			local := t.TempDir()
			localDigests, checksumDigest := writeExternalBackupFixture(t, local)
			remoteRoot := "/home/arch/.local/state/lmm-api-production-backups/deploy-test"
			remoteDigests := make(map[string]string, len(localDigests))
			members := append(externalBackupMemberNames(), "SHA256SUMS")
			for name, digest := range localDigests {
				remoteDigests[filepath.Join(remoteRoot, name)] = digest
			}
			members, remoteDigests, unsafeName := test.mutate(members, remoteDigests, remoteRoot)
			runtime := &productionReleaseRuntime{runner: remoteBackupVerificationRunner{
				root: remoteRoot, digests: remoteDigests, members: members, unsafeName: unsafeName,
			}}

			if err := runtime.verifyRemoteExternalBackupCopy(context.Background(), productionOffhostAlias, remoteRoot, local, "arch", checksumDigest); err == nil {
				t.Fatal("unsafe remote backup copy was accepted")
			}
		})
	}
}

func TestLocalExternalBackupInventoryRejectsLinkedOrUnexpectedMembers(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "symlink",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				path := filepath.Join(root, "database.age")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("application.archive", path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "hardlink",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				path := filepath.Join(root, "database.age")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(filepath.Join(root, "application.archive"), path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra member",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("unexpected"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			_, _ = writeExternalBackupFixture(t, root)
			test.mutate(t, root)
			runtime := &productionRuntime{effectiveUID: os.Geteuid}

			if err := runtime.validateExternalBackupInventory(root, externalBackupMemberNames()); err == nil {
				t.Fatal("unsafe local external backup inventory was accepted")
			}
		})
	}
}
