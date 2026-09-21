package appcli

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/disk"
)

// These limits bound scratch disk, decompression work and inventory memory.
// age itself does not compress: a plaintext cannot exceed its ciphertext size.
const (
	controllerBackupMaxArchive = int64(2 << 30)
	controllerBackupMaxTar     = int64(4 << 30)
	controllerBackupHeadroom   = uint64(1 << 30)
	controllerBackupMaxEntries = 100000
)

type controllerBackupArchive struct {
	CiphertextSHA256 string `json:"ciphertext_sha256"`
	PlaintextSHA256  string `json:"plaintext_sha256"`
	CiphertextBytes  int64  `json:"ciphertext_bytes"`
	PlaintextBytes   int64  `json:"plaintext_bytes"`
}

type controllerBackupSet struct {
	Format                  int                                `json:"format"`
	DeploymentID            string                             `json:"deployment_id"`
	ExpectedHost            string                             `json:"expected_host"`
	CapturedUTC             time.Time                          `json:"captured_utc"`
	DatabaseSchema          string                             `json:"database_schema"`
	EnvironmentSHA256       string                             `json:"environment_sha256"`
	GoRollbackSHA256        string                             `json:"go_rollback_sha256"`
	WebRollbackSHA256       string                             `json:"web_rollback_sha256"`
	GoRollbackPayloadSHA256 string                             `json:"go_rollback_payload_sha256"`
	FrontendRollbackSHA256  string                             `json:"frontend_rollback_sha256"`
	Archives                map[string]controllerBackupArchive `json:"archives"`
}

// verifyControllerBackupSet imports evidence only. It never creates a source
// backup or invokes transport/target commands. Errors deliberately omit paths,
// archive member names, configuration values and subprocess diagnostics.
func (runtime *productionRuntime) verifyControllerBackupSet(ctx context.Context, plan productionReleasePlan, root, identity string) (set controllerBackupSet, digest string, err error) {
	fail := func() (controllerBackupSet, string, error) {
		return controllerBackupSet{}, "", errors.New("controller backup import verification failed")
	}
	if runtime.runner == nil || ctx.Err() != nil || !productionIDPattern.MatchString(plan.DeploymentID) || plan.ExpectedHost != productionExpectedHost {
		return fail()
	}
	uid := os.Geteuid()
	if runtime.effectiveUID != nil {
		uid = runtime.effectiveUID()
	}
	if err = controllerBackupInventory(root, uid); err != nil {
		return fail()
	}
	manifest, err := controllerBackupReadPrivate(filepath.Join(root, "backup-set.json"), uid, 64<<10)
	if err != nil || decodeControllerBackupJSON(manifest, &set) != nil {
		return fail()
	}
	digest = controllerBackupDigest(manifest)
	complete, err := controllerBackupReadPrivate(filepath.Join(root, ".complete"), uid, 65)
	if err != nil || string(complete) != digest+"\n" {
		return fail()
	}
	now := time.Now()
	if runtime.now != nil {
		now = runtime.now()
	}
	if controllerBackupValidateSet(set, plan, now) != nil || controllerBackupValidateWorkspace(plan, uid) != nil {
		return fail()
	}
	key, err := controllerBackupOpenPrivate(identity, uid)
	if err != nil {
		return fail()
	}
	if err = key.Close(); err != nil {
		return fail()
	}
	temporaryRoot := filepath.Join(plan.ControllerWorkspace, "tmp")
	if controllerBackupDirectory(temporaryRoot, uid) != nil {
		return fail()
	}
	scratch, err := os.MkdirTemp(temporaryRoot, "controller-backup-verify-")
	if err != nil {
		return fail()
	}
	defer func() {
		if cleanupErr := os.RemoveAll(scratch); cleanupErr != nil {
			set, digest, err = controllerBackupSet{}, "", errors.New("controller backup plaintext cleanup failed")
		}
	}()
	for _, kind := range controllerBackupKinds {
		if ctx.Err() != nil || runtime.controllerBackupVerifyArchive(ctx, root, identity, scratch, kind, set, uid) != nil {
			return fail()
		}
	}
	// Catch collection replacement/tampering while expensive verification ran.
	if controllerBackupInventory(root, uid) != nil {
		return fail()
	}
	again, readErr := controllerBackupReadPrivate(filepath.Join(root, "backup-set.json"), uid, 64<<10)
	complete, completeErr := controllerBackupReadPrivate(filepath.Join(root, ".complete"), uid, 65)
	if readErr != nil || completeErr != nil || !bytes.Equal(again, manifest) || string(complete) != digest+"\n" {
		return fail()
	}
	for _, kind := range controllerBackupKinds {
		archive := set.Archives[kind]
		if controllerBackupVerifyFile(filepath.Join(root, kind+".age"), uid, archive.CiphertextBytes, archive.CiphertextSHA256) != nil {
			return fail()
		}
	}
	return set, digest, nil
}

func controllerBackupValidateSet(set controllerBackupSet, plan productionReleasePlan, now time.Time) error {
	_, offset := set.CapturedUTC.Zone()
	if set.Format != 1 || set.DeploymentID != plan.DeploymentID || set.ExpectedHost != plan.ExpectedHost ||
		set.CapturedUTC.IsZero() || offset != 0 || set.CapturedUTC.After(now.Add(30*time.Second)) || set.CapturedUTC.Before(now.Add(-24*time.Hour)) ||
		!isDatabaseSchema(set.DatabaseSchema) || len(set.Archives) != len(controllerBackupKinds) {
		return errors.New("invalid controller backup collection identity or capture time")
	}
	bindings := [][2]string{
		{set.GoRollbackSHA256, plan.GoRollback.PackageSHA256},
		{set.WebRollbackSHA256, plan.WebRollback.PackageSHA256},
		{set.GoRollbackPayloadSHA256, plan.GoRollback.PayloadSHA256},
		{set.FrontendRollbackSHA256, plan.WebRollback.PayloadSHA256},
	}
	for _, binding := range bindings {
		if !productionSHA256Pattern.MatchString(binding[0]) || binding[0] != binding[1] {
			return errors.New("controller backup rollback binding mismatch")
		}
	}
	if !productionSHA256Pattern.MatchString(set.EnvironmentSHA256) {
		return errors.New("invalid controller backup configuration digest")
	}
	for _, kind := range controllerBackupKinds {
		archive, ok := set.Archives[kind]
		if !ok || !productionSHA256Pattern.MatchString(archive.CiphertextSHA256) || !productionSHA256Pattern.MatchString(archive.PlaintextSHA256) ||
			archive.CiphertextBytes <= 0 || archive.CiphertextBytes > controllerBackupMaxArchive ||
			archive.PlaintextBytes <= 0 || archive.PlaintextBytes > archive.CiphertextBytes {
			return errors.New("invalid controller backup archive declaration")
		}
	}
	return nil
}

// Check every component, not just the leaf: EvalSymlinks alone permits aliases
// that happen to resolve back to the same textual path.
func controllerBackupNoLinks(name string) error {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name || name == string(filepath.Separator) {
		return errors.New("controller backup path must be canonical and absolute")
	}
	current := string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(name, current), string(filepath.Separator))
	for i, component := range parts {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) {
			return errors.New("controller backup path has an unsafe component")
		}
	}
	return nil
}

func controllerBackupDirectory(name string, uid int) error {
	if controllerBackupNoLinks(name) != nil {
		return errors.New("unsafe controller backup directory path")
	}
	info, err := os.Lstat(name)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return errors.New("controller backup directory must be private")
	}
	owner, _, ok := deploymentFileOwnership(info)
	if !ok || int(owner) != uid {
		return errors.New("controller backup directory owner mismatch")
	}
	return nil
}

func controllerBackupOpenPrivate(name string, uid int) (*os.File, error) {
	if controllerBackupNoLinks(name) != nil {
		return nil, errors.New("unsafe controller backup file path")
	}
	before, err := os.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 || before.Size() <= 0 || before.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, errors.New("controller backup file must be private and nonempty")
	}
	owner, links, ok := deploymentFileOwnership(before)
	if !ok || int(owner) != uid || links != 1 {
		return nil, errors.New("controller backup file ownership or link count mismatch")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, errors.New("cannot open controller backup file")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || controllerBackupNoLinks(name) != nil {
		_ = file.Close()
		return nil, errors.New("controller backup file changed during open")
	}
	return file, nil
}

func controllerBackupReadPrivate(name string, uid int, limit int64) ([]byte, error) {
	file, err := controllerBackupOpenPrivate(name, uid)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) > limit {
		return nil, errors.New("controller backup metadata read failed or exceeds limit")
	}
	return data, nil
}

func controllerBackupInventory(root string, uid int) error {
	if controllerBackupDirectory(root, uid) != nil {
		return errors.New("unsafe controller backup inventory root")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 6 {
		return errors.New("controller backup inventory must have exactly six members")
	}
	allowed := map[string]bool{"backup-set.json": true, ".complete": true}
	for _, kind := range controllerBackupKinds {
		allowed[kind+".age"] = true
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return errors.New("unexpected controller backup inventory member")
		}
		file, err := controllerBackupOpenPrivate(filepath.Join(root, entry.Name()), uid)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return errors.New("controller backup inventory close failed")
		}
	}
	return nil
}

func controllerBackupValidateWorkspace(plan productionReleasePlan, uid int) error {
	if controllerBackupDirectory(plan.ControllerWorkspace, uid) != nil {
		return errors.New("unsafe controller backup verification workspace")
	}
	marker, err := controllerBackupReadPrivate(filepath.Join(plan.ControllerWorkspace, productionWorkspaceMarker), uid, 16<<10)
	if err != nil {
		return err
	}
	values, err := parseSimpleManifest(marker)
	if err != nil || values["deployment_id"] != plan.DeploymentID {
		return errors.New("controller backup workspace transaction mismatch")
	}
	return nil
}

func controllerBackupCheckSpace(root string, ciphertextBytes int64) error {
	// gopsutil's Linux Usage uses statfs, not a shell/df process. Import is
	// deliberately unavailable on other OSes rather than taking a fallback.
	if goruntime.GOOS != "linux" || ciphertextBytes <= 0 || ciphertextBytes > controllerBackupMaxArchive {
		return errors.New("controller backup scratch budget unsupported")
	}
	usage, err := disk.Usage(root)
	// Btrfs reports zero total/free inodes because it allocates them dynamically;
	// apply the inode reserve only to filesystems with a finite reported pool.
	needed := uint64(ciphertextBytes)*2 + controllerBackupHeadroom
	if err != nil || usage.Total == 0 || usage.Free < needed || usage.UsedPercent >= 80 ||
		(usage.InodesTotal > 0 && (usage.InodesFree < 16 || usage.InodesUsedPercent >= 80)) || float64(usage.Free-needed) < float64(usage.Total)*0.20 {
		return errors.New("insufficient controller backup scratch disk or inode budget")
	}
	return nil
}

func controllerBackupDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func controllerBackupCopyVerified(name string, uid int, length int64, digest string, destination io.Writer) error {
	file, err := controllerBackupOpenPrivate(name, uid)
	if err != nil {
		return err
	}
	hash := sha256.New()
	count, copyErr := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(file, length+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || count != length || hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("controller backup byte length or digest mismatch")
	}
	return nil
}

func controllerBackupVerifyFile(name string, uid int, length int64, digest string) error {
	return controllerBackupCopyVerified(name, uid, length, digest, io.Discard)
}

func (runtime *productionRuntime) controllerBackupVerifyArchive(ctx context.Context, root, identity, scratch, kind string, set controllerBackupSet, uid int) error {
	archive := set.Archives[kind]
	if controllerBackupCheckSpace(scratch, archive.CiphertextBytes) != nil {
		return errors.New("controller backup scratch budget rejected")
	}
	// Decrypt a checked, private snapshot instead of reopening mutable imported
	// ciphertext in age. At most one ciphertext and one plaintext coexist.
	cipherPath, plainPath := filepath.Join(scratch, "ciphertext.age"), filepath.Join(scratch, "plaintext")
	cipher, err := os.OpenFile(cipherPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("cannot create controller backup scratch ciphertext")
	}
	copyErr := controllerBackupCopyVerified(filepath.Join(root, kind+".age"), uid, archive.CiphertextBytes, archive.CiphertextSHA256, cipher)
	closeErr := cipher.Close()
	if copyErr != nil || closeErr != nil {
		return errors.New("controller backup ciphertext verification failed")
	}
	// Claim output privately instead of depending on the caller's umask.
	plain, err := os.OpenFile(plainPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("cannot create private controller backup plaintext")
	}
	if err := plain.Close(); err != nil {
		return errors.New("cannot close private controller backup plaintext")
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandAge,
		Args: []string{"--decrypt", "--identity", identity, "--output", plainPath, cipherPath},
		Dir:  scratch, Env: []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "TMPDIR=" + scratch}, Timeout: 10 * time.Minute, Sensitive: true}); err != nil {
		return errors.New("controller backup age authentication failed")
	}
	if controllerBackupVerifyFile(plainPath, uid, archive.PlaintextBytes, archive.PlaintextSHA256) != nil {
		return errors.New("controller backup plaintext verification failed")
	}
	if kind == "database" {
		if err := runtime.controllerBackupVerifyDatabase(ctx, plainPath, scratch, uid); err != nil {
			return err
		}
	} else if controllerBackupVerifyTar(plainPath, kind, set, uid) != nil {
		return errors.New("controller backup archive verification failed")
	}
	// The subprocess must not have altered its input while validating it.
	if ctx.Err() != nil || controllerBackupVerifyFile(plainPath, uid, archive.PlaintextBytes, archive.PlaintextSHA256) != nil {
		return errors.New("controller backup plaintext changed during validation")
	}
	if err := os.Remove(plainPath); err != nil {
		return errors.New("controller backup plaintext removal failed")
	}
	if err := os.Remove(cipherPath); err != nil {
		return errors.New("controller backup scratch ciphertext removal failed")
	}
	return nil
}

func (runtime *productionRuntime) controllerBackupVerifyDatabase(ctx context.Context, name, scratch string, uid int) error {
	file, err := controllerBackupOpenPrivate(name, uid)
	if err != nil {
		return err
	}
	magic := make([]byte, 5)
	_, readErr := io.ReadFull(file, magic)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || string(magic) != "PGDMP" {
		return errors.New("controller backup database is not a custom PostgreSQL archive")
	}
	_, err = runtime.runner.Run(ctx, productionCommand{Name: commandPGRestore,
		Args: []string{"--format=custom", "--file=/dev/null", name}, Dir: scratch,
		Env: []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "TMPDIR=" + scratch}, Timeout: 30 * time.Minute, Sensitive: true})
	if err != nil {
		return errors.New("controller backup full PostgreSQL archive validation failed")
	}
	return nil
}

type controllerBackupCountingReader struct {
	reader io.Reader
	count  int64
}

func (reader *controllerBackupCountingReader) Read(data []byte) (int, error) {
	n, err := reader.reader.Read(data)
	reader.count += int64(n)
	return n, err
}

func controllerBackupVerifyTar(name, kind string, set controllerBackupSet, uid int) (err error) {
	file, err := controllerBackupOpenPrivate(name, uid)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	buffer := bufio.NewReader(file)
	magic, err := buffer.Peek(2)
	if err != nil {
		return errors.New("controller backup tar is truncated")
	}
	var source io.Reader = buffer
	if magic[0] == 0x1f && magic[1] == 0x8b {
		compressed, err := gzip.NewReader(buffer)
		if err != nil {
			return errors.New("controller backup gzip header is invalid")
		}
		defer func() { err = errors.Join(err, compressed.Close()) }()
		source = compressed
	}
	counted := &controllerBackupCountingReader{reader: io.LimitReader(source, controllerBackupMaxTar+1)}
	reader := tar.NewReader(counted)
	required, expected := "", ""
	switch kind {
	case "application":
		required, expected = "usr/bin/lmm-api-go", set.GoRollbackPayloadSHA256
	case "frontend":
		required, expected = "index.html", set.FrontendRollbackSHA256
	case "configuration":
		required, expected = "etc/lmm-api-go/lmm-api-go.env", set.EnvironmentSHA256
	default:
		return errors.New("unknown controller backup tar kind")
	}
	seen := make(map[string]byte)
	parents := make(map[string]bool)
	found, padding := false, int64(0)
	for {
		before := counted.count
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			// archive/tar also returns EOF without the two required zero blocks.
			if counted.count-before < padding+1024 {
				return errors.New("controller backup tar end marker is missing")
			}
			break
		}
		if nextErr != nil || counted.count > controllerBackupMaxTar || len(seen) >= controllerBackupMaxEntries {
			return errors.New("controller backup tar is invalid or exceeds limits")
		}
		entry := header.Name
		if header.Typeflag == tar.TypeDir {
			entry = strings.TrimSuffix(entry, "/")
		}
		if entry == "" || entry == "." || path.IsAbs(entry) || path.Clean(entry) != entry || entry == ".." || strings.HasPrefix(entry, "../") ||
			strings.ContainsAny(entry, "\\\x00\r\n") || len(entry) > 1024 || header.Linkname != "" || len(header.PAXRecords) != 0 ||
			(header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir) || header.Size < 0 || header.Size > controllerBackupMaxArchive {
			return errors.New("controller backup tar member is unsafe")
		}
		if _, exists := seen[entry]; exists {
			return errors.New("controller backup tar contains duplicate members")
		}
		// A regular ancestor would make the archive impossible to restore.
		if header.Typeflag == tar.TypeReg && parents[entry] {
			return errors.New("controller backup tar replaces an ancestor with a file")
		}
		for parent := path.Dir(entry); parent != "."; parent = path.Dir(parent) {
			if prior, exists := seen[parent]; exists && prior != tar.TypeDir {
				return errors.New("controller backup tar has a non-directory ancestor")
			}
			parents[parent] = true
		}
		seen[entry] = header.Typeflag
		padding = (512 - header.Size%512) % 512
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 || entry == required {
				return errors.New("controller backup required member is not a regular file")
			}
			continue
		}
		if entry == required {
			if header.Size <= 0 {
				return errors.New("controller backup required member is empty")
			}
			hash := sha256.New()
			var environment bytes.Buffer
			var writer io.Writer = hash
			if kind == "configuration" {
				if header.Size > 1<<20 {
					return errors.New("controller backup configuration exceeds limit")
				}
				writer = io.MultiWriter(hash, &environment)
			}
			if _, err := io.Copy(writer, reader); err != nil || hex.EncodeToString(hash.Sum(nil)) != expected {
				return errors.New("controller backup required member digest mismatch")
			}
			if kind == "configuration" && controllerBackupValidateEnvironment(environment.Bytes()) != nil {
				return errors.New("controller backup configuration authority is invalid")
			}
			found = true
		} else if _, err := io.Copy(io.Discard, reader); err != nil {
			return errors.New("controller backup tar member is truncated")
		}
	}
	// Read all compressed bytes through the gzip trailer/EOF, not just tar EOF.
	// Only zero tar record padding may follow the end marker.
	var trailing [32 << 10]byte
	for {
		n, readErr := counted.Read(trailing[:])
		if counted.count > controllerBackupMaxTar || !controllerBackupAllZero(trailing[:n]) {
			return errors.New("controller backup tar trailing data is invalid")
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return errors.New("controller backup compressed stream failed full validation")
		}
	}
	if !found || counted.count%512 != 0 {
		return errors.New("controller backup tar is incomplete")
	}
	return nil
}

func controllerBackupAllZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func controllerBackupValidateEnvironment(data []byte) error {
	values, err := parseProductionEnvironment(data)
	if err != nil {
		return errors.New("invalid controller backup environment syntax")
	}
	databaseURL, err := productionDatabaseURL(values)
	if err != nil {
		return errors.New("invalid controller backup PostgreSQL authority")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed.Hostname() == "" || parsed.Path == "" || parsed.Path == "/" || parsed.Fragment != "" {
		return errors.New("invalid controller backup PostgreSQL URL")
	}
	if _, err := url.ParseQuery(parsed.RawQuery); err != nil {
		return errors.New("invalid controller backup PostgreSQL parameters")
	}
	return nil
}
