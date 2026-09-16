//go:build linux

package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestIncident343PeerIdentityStrictTypes(t *testing.T) {
	good := `{"database":"app_db","database_oid":16384,"started":"1789589513.123456","version":"180004","port":"5432"}`
	identity, err := parseIncident343DatabaseIdentity([]byte(good))
	if err != nil || identity.OID != 16384 {
		t.Fatalf("valid identity failed: %v", err)
	}
	for _, bad := range []string{
		strings.Replace(good, `16384`, `"16384"`, 1),
		strings.Replace(good, `16384`, `0`, 1),
		strings.Replace(good, `"5432"`, `"65536"`, 1),
		strings.Replace(good, `"5432"`, `"05432"`, 1),
		strings.Replace(good, `"180004"`, `"18.4"`, 1),
		strings.Replace(good, `"1789589513.123456"`, `"bad"`, 1),
		strings.Replace(good, `"app_db"`, `""`, 1),
		strings.Replace(good, `"database_oid"`, `"unknown"`, 1),
		good + ` {}`, `null`, strings.Repeat(" ", 4097),
	} {
		if _, err := parseIncident343DatabaseIdentity([]byte(bad)); err == nil {
			t.Fatal("accepted invalid database identity")
		}
	}
	if !strings.Contains(incident343DatabaseIdentitySQL, "d.oid::bigint") {
		t.Fatal("oid JSON must be explicitly numeric")
	}
}

func TestIncident343PeerURLCannotInjectOptions(t *testing.T) {
	id := incident343DatabaseIdentity{Database: "app/odd?sslmode=require&user=evil#x", Port: "5432"}
	raw, err := incident343PeerURL(id, "/run/postgresql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "postgresql:///") {
		t.Fatal("not a libpq connection URI")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "" || parsed.Path != "/"+id.Database || parsed.Fragment != "" || parsed.Query().Get("user") != "postgres" || parsed.Query().Get("sslmode") != "disable" || len(parsed.Query()) != 5 {
		t.Fatal("database name escaped the URI path")
	}
	if _, err := incident343PeerURL(id, "/tmp/attacker"); err == nil {
		t.Fatal("accepted unreviewed socket directory")
	}
}

type incident343LocalRunnerTest struct {
	calls      int
	localCalls int
	failure    error
	last       productionCommand
}

func (r *incident343LocalRunnerTest) Run(_ context.Context, c productionCommand) ([]byte, error) {
	r.calls++
	r.last = c
	return nil, nil
}
func (r *incident343LocalRunnerTest) DumpIncident343(context.Context, string, []string, string) error {
	r.localCalls++
	return r.failure
}

type incident343OrdinaryRunnerTest struct{ last productionCommand }

func (r *incident343OrdinaryRunnerTest) Run(_ context.Context, c productionCommand) ([]byte, error) {
	r.last = c
	return nil, nil
}

func TestIncident343PeerFailureNeverFallsBack(t *testing.T) {
	expected := errors.New("identity mismatch")
	runner := &incident343LocalRunnerTest{failure: expected}
	err := dumpIncident343Database(context.Background(), runner, "app", nil, "target")
	if !errors.Is(err, expected) || runner.localCalls != 1 || runner.calls != 0 {
		t.Fatal("peer failure bypassed identity guard")
	}
	runner.failure = nil
	if err := dumpIncident343Database(context.Background(), runner, "app", nil, "target"); err != nil || runner.calls != 0 {
		t.Fatal("local dump unexpectedly invoked ordinary runner")
	}
	ordinary := &incident343OrdinaryRunnerTest{}
	if err := dumpIncident343Database(context.Background(), ordinary, "app", []string{"PGCONNECT_TIMEOUT=8"}, "target"); err != nil {
		t.Fatal(err)
	}
	if ordinary.last.Name != commandPGDump || !ordinary.last.Sensitive || !reflect.DeepEqual(ordinary.last.Args, []string{"--no-password", "--format=custom", "--file=target", "app"}) {
		t.Fatal("ordinary injected runner contract changed")
	}
}

func TestIncident343BackupWriterBoundsAndExclusivity(t *testing.T) {
	target := filepath.Join(t.TempDir(), "backup")
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	writer := &incident343BoundedFile{file: file, remaining: 4}
	if n, err := writer.Write([]byte("abcd")); err != nil || n != 4 {
		t.Fatal("bounded write failed")
	}
	if _, err := writer.Write([]byte("x")); err == nil {
		t.Fatal("exceeded backup bound")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	// Existing evidence is rejected before invoking any OS/database process.
	if err := streamIncident343PeerBackup(context.Background(), "unused", nil, target); err == nil {
		t.Fatal("existing evidence overwritten")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "abcd" {
		t.Fatal("existing evidence changed")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("backup privacy changed")
	}
}

func TestIncident343PeerIdentityCommandIsSensitive(t *testing.T) {
	runner := &incident343IdentityRunnerTest{}
	identity := incident343DatabaseIdentity{Database: "app", OID: 16384, Started: "1789589513.123456", Version: "180004", Port: "5432"}
	runner.response, _ = json.Marshal(identity)
	actual, err := incident343Identity(context.Background(), runner, "postgresql:///app", []string{"LC_ALL=C"}, true)
	if err != nil || actual != identity {
		t.Fatal("identity read failed")
	}
	if runner.last.Name != commandRunuser || !runner.last.Sensitive || runner.last.OutputLimit != 4096 || !reflect.DeepEqual(runner.last.Args[:4], []string{"-u", "postgres", "--", commandPSQL}) {
		t.Fatal("unsafe identity command")
	}
}

type incident343IdentityRunnerTest struct {
	last     productionCommand
	response []byte
}

func (r *incident343IdentityRunnerTest) Run(_ context.Context, c productionCommand) ([]byte, error) {
	r.last = c
	return r.response, nil
}
