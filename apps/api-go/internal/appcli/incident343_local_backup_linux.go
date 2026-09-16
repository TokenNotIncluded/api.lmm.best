//go:build linux

package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// This capability is used only by the fixed incident recovery executable.
// Fake command runners retain their ordinary dump behavior in existing tests.
type incident343LocalDumper interface {
	DumpIncident343(context.Context, string, []string, string) error
}

func dumpIncident343Database(ctx context.Context, runner productionCommandRunner, database string, environment []string, target string) error {
	if local, ok := runner.(incident343LocalDumper); ok {
		// A peer-identity failure MUST NOT fall back to a different database or role.
		return local.DumpIncident343(ctx, database, environment, target)
	}
	_, err := runner.Run(ctx, productionCommand{Name: commandPGDump,
		Args: []string{"--no-password", "--format=custom", "--file=" + target, database},
		Env:  environment, Sensitive: true, Timeout: 2 * time.Minute})
	return err
}

const incident343DatabaseIdentitySQL = `SELECT json_build_object(
 'database',current_database(),'database_oid',d.oid::bigint,
 'started',extract(epoch FROM pg_postmaster_start_time())::text,
 'version',current_setting('server_version_num'),'port',current_setting('port'))
 FROM pg_catalog.pg_database d WHERE d.datname=current_database()`

type incident343DatabaseIdentity struct {
	Database string `json:"database"`
	OID      uint64 `json:"database_oid"`
	Started  string `json:"started"`
	Version  string `json:"version"`
	Port     string `json:"port"`
}

func parseIncident343DatabaseIdentity(data []byte) (incident343DatabaseIdentity, error) {
	var result incident343DatabaseIdentity
	if len(data) > 4096 {
		return result, errors.New("oversized incident database identity")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, errors.New("invalid incident database identity")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return result, errors.New("trailing incident database identity")
	}
	port, err := strconv.ParseUint(result.Port, 10, 16)
	if err != nil || port == 0 || result.Port != strconv.FormatUint(port, 10) || result.OID == 0 || result.OID > 1<<32-1 ||
		len(result.Database) < 1 || len(result.Database) > 63 || strings.ContainsAny(result.Database, "\x00\r\n") ||
		!regexp.MustCompile(`^[0-9]{1,20}\.[0-9]{1,6}$`).MatchString(result.Started) ||
		!regexp.MustCompile(`^[0-9]{6}$`).MatchString(result.Version) {
		return result, errors.New("invalid incident database identity fields")
	}
	return result, nil
}

func incident343PeerURL(identity incident343DatabaseIdentity, directory string) (string, error) {
	if directory != "/run/postgresql" && directory != "/tmp" {
		return "", errors.New("unapproved PostgreSQL socket directory")
	}
	query := url.Values{"host": {directory}, "port": {identity.Port}, "user": {"postgres"}, "sslmode": {"disable"}, "connect_timeout": {"8"}}
	return "postgresql:///" + url.PathEscape(identity.Database) + "?" + query.Encode(), nil
}

func incident343Identity(ctx context.Context, runner productionCommandRunner, database string, environment []string, peer bool) (incident343DatabaseIdentity, error) {
	args := []string{"-X", "--no-password", "-At", "-v", "ON_ERROR_STOP=1", "-c", incident343DatabaseIdentitySQL, database}
	command := commandPSQL
	if peer {
		command = commandRunuser
		args = append([]string{"-u", "postgres", "--", commandPSQL}, args...)
	}
	output, err := runner.Run(ctx, productionCommand{Name: command, Args: args, Env: environment, Sensitive: true, Timeout: 15 * time.Second, OutputLimit: 4096})
	if err != nil {
		return incident343DatabaseIdentity{}, errors.New("incident database identity probe failed")
	}
	return parseIncident343DatabaseIdentity(output)
}

func (runner osProductionCommandRunner) DumpIncident343(ctx context.Context, database string, environment []string, target string) error {
	if os.Geteuid() != 0 {
		return errors.New("incident peer backup requires root")
	}
	parsed, err := url.Parse(database)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		(parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1") {
		return errors.New("incident peer backup requires the local application database")
	}
	// Do not turn this fixed native capability into an arbitrary file writer.
	expected := filepath.Join(defaultProductionPaths().WorkRoot, incident343Deployment, "state", "incident-343-schema-recovery")
	if target != filepath.Join(expected, "before-schema.database.dump") && target != filepath.Join(expected, "backup-retry-1", "before-schema.database.dump") {
		return errors.New("incident backup target is outside the native audit")
	}
	for directory := filepath.Dir(target); directory != "/"; directory = filepath.Dir(directory) {
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
			return errors.New("unsafe incident backup parent")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return errors.New("incident backup parent must be root-owned")
		}
	}
	account, err := user.Lookup("postgres")
	if err != nil {
		return errors.New("local postgres account is unavailable")
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || uid == 0 {
		return errors.New("invalid local postgres account")
	}
	identity, err := incident343Identity(ctx, runner, database, environment, false)
	if err != nil {
		return err
	}
	clean := []string{"PATH=/usr/bin:/bin", "HOME=" + account.HomeDir, "LC_ALL=C", "PGCONNECT_TIMEOUT=8"}
	for _, directory := range []string{"/run/postgresql", "/tmp"} {
		socket := filepath.Join(directory, ".s.PGSQL."+identity.Port)
		info, err := os.Lstat(socket)
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(uid) {
			continue
		}
		local, err := incident343PeerURL(identity, directory)
		if err != nil {
			return err
		}
		peer, err := incident343Identity(ctx, runner, local, clean, true)
		if err != nil || peer != identity {
			continue
		}
		return streamIncident343PeerBackup(ctx, local, clean, target)
	}
	return errors.New("no local PostgreSQL peer matches the application database identity")
}

type incident343BoundedFile struct {
	file      *os.File
	remaining int64
}

func (w *incident343BoundedFile) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, errors.New("incident backup output limit exceeded")
	}
	n, err := w.file.Write(data)
	w.remaining -= int64(n)
	return n, err
}

func streamIncident343PeerBackup(parent context.Context, local string, environment []string, target string) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return errors.New("cannot create exclusive incident backup")
	}
	defer output.Close()
	stderr, err := os.OpenFile(target+".stderr", os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return errors.New("cannot create private incident backup log")
	}
	defer stderr.Close()
	// Root owns the files; runuser only receives pipes. No directory permissions,
	// database grants, schema filters, or table exclusions are changed.
	process := exec.CommandContext(ctx, commandRunuser, "-u", "postgres", "--", commandPGDump,
		"--no-password", "--format=custom", "--lock-wait-timeout=5s", local)
	process.Env = environment
	process.Dir = "/"
	process.Stdout = &incident343BoundedFile{file: output, remaining: 2 << 30}
	process.Stderr = &incident343BoundedFile{file: stderr, remaining: 1 << 20}
	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	process.Cancel = func() error {
		if process.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-process.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	process.WaitDelay = 3 * time.Second
	if err := process.Run(); err != nil {
		return errors.New("verified local full database backup failed; private audit retained")
	}
	if err := output.Sync(); err != nil {
		return errors.New("incident database backup sync failed")
	}
	return nil
}
