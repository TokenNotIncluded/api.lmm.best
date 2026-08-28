package appcli

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultGeoIPDatabasePath       = "/var/lib/geoip2/DBIP-Country-Lite.mmdb"
	defaultGeoIPSourceHost         = "download.db-ip.com"
	geoIPCompressedDownloadLimit   = 32 << 20
	geoIPDecompressedDatabaseLimit = 128 << 20
	geoIPMaximumCompressionRatio   = 100
)

type geoIPUpdateOptions struct {
	DatabasePath string
	SourceURL    string
}

// RunGeoIP owns the country database lifecycle used by the package-managed
// Nginx edge policy. It deliberately lives in the same binary as the backend
// and deployment CLI; there is no separate production shell updater.
func RunGeoIP(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "update" {
		_, _ = fmt.Fprintf(stderr, "%s geoip: choose update\n", ProgramName)
		return ExitUsage
	}
	options, err := parseGeoIPUpdateOptions(args[1:], stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s geoip update: %v\n", ProgramName, err)
		return ExitUsage
	}
	if os.Geteuid() != 0 {
		_, _ = fmt.Fprintf(stderr, "%s geoip update: must run as root\n", ProgramName)
		return ExitError
	}
	if err := updateGeoIPDatabase(context.Background(), options); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s geoip update: %v\n", ProgramName, err)
		return ExitError
	}
	_, _ = fmt.Fprintf(stdout, "database=%s\n", options.DatabasePath)
	return ExitOK
}

func parseGeoIPUpdateOptions(args []string, stderr io.Writer) (geoIPUpdateOptions, error) {
	options := geoIPUpdateOptions{DatabasePath: defaultGeoIPDatabasePath}
	flags := flag.NewFlagSet("geoip update", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.DatabasePath, "database-path", options.DatabasePath, "DB-IP Country MMDB path")
	flags.StringVar(&options.SourceURL, "source-url", "", "test-only HTTPS source URL (must use download.db-ip.com)")
	if err := flags.Parse(args); err != nil {
		return geoIPUpdateOptions{}, err
	}
	if flags.NArg() != 0 {
		return geoIPUpdateOptions{}, errors.New("unexpected positional arguments")
	}
	clean, err := cleanAbsoluteNonRoot(options.DatabasePath)
	if err != nil {
		return geoIPUpdateOptions{}, fmt.Errorf("invalid --database-path: %w", err)
	}
	if filepath.Ext(clean) != ".mmdb" {
		return geoIPUpdateOptions{}, errors.New("--database-path must name an .mmdb file")
	}
	options.DatabasePath = clean
	if options.SourceURL != "" {
		if err := validateGeoIPSourceURL(options.SourceURL); err != nil {
			return geoIPUpdateOptions{}, fmt.Errorf("invalid --source-url: %w", err)
		}
	}
	return options, nil
}

func validateGeoIPSourceURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), defaultGeoIPSourceHost) {
		return errors.New("source must use the DB-IP HTTPS host")
	}
	if parsed.User != nil || (parsed.Port() != "" && parsed.Port() != "443") {
		return errors.New("source must not contain credentials or a non-HTTPS port")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasSuffix(parsed.EscapedPath(), ".mmdb.gz") {
		return errors.New("source must name a DB-IP .mmdb.gz object without query or fragment")
	}
	return nil
}

func ensureGeoIPDirectory(path string) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return errors.New("database directory must be absolute")
	}
	current := string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("database path component %q is not a real directory", current)
		}
	}
	return nil
}

func updateGeoIPDatabase(ctx context.Context, options geoIPUpdateOptions) error {
	if options.DatabasePath == "" {
		return errors.New("database path is empty")
	}
	if err := ensureGeoIPDirectory(filepath.Dir(options.DatabasePath)); err != nil {
		return fmt.Errorf("prepare database directory: %w", err)
	}
	sourceURL := options.SourceURL
	if sourceURL == "" {
		sourceURL = "https://" + defaultGeoIPSourceHost + "/free/dbip-country-lite-" + time.Now().UTC().Format("2006-01") + ".mmdb.gz"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	request.Header.Set("User-Agent", "lmm-api/geoip-updater")
	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many GeoIP download redirects")
			}
			return validateGeoIPSourceURL(request.URL.String())
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download database: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download database: HTTP %s", response.Status)
	}
	compressed, err := io.ReadAll(io.LimitReader(response.Body, geoIPCompressedDownloadLimit+1))
	if err != nil {
		return fmt.Errorf("read compressed database: %w", err)
	}
	if len(compressed) == 0 || len(compressed) > geoIPCompressedDownloadLimit {
		return errors.New("compressed database is empty or too large")
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("validate gzip database: %w", err)
	}
	defer reader.Close()
	temporary, err := os.CreateTemp(filepath.Dir(options.DatabasePath), ".DBIP-Country-Lite.mmdb.*.new")
	if err != nil {
		return fmt.Errorf("create database staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	defer cleanup()
	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("set database mode: %w", err)
	}
	decompressed, err := io.Copy(temporary, io.LimitReader(reader, geoIPDecompressedDatabaseLimit+1))
	if err != nil {
		return fmt.Errorf("decompress database: %w", err)
	}
	if decompressed == 0 || decompressed > geoIPDecompressedDatabaseLimit {
		return errors.New("decompressed database is empty or too large")
	}
	if decompressed > int64(len(compressed))*geoIPMaximumCompressionRatio {
		return errors.New("compressed database exceeds the safe expansion ratio")
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync database: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	if err := validateGeoIPDatabase(temporaryPath); err != nil {
		return err
	}
	previousPath := options.DatabasePath + ".previous"
	if info, err := os.Lstat(previousPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("database previous file is unsafe")
		}
		_ = os.Remove(previousPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info, err := os.Lstat(options.DatabasePath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("existing database file is unsafe")
		}
		if err := os.Rename(options.DatabasePath, previousPath); err != nil {
			return fmt.Errorf("preserve previous database: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(temporaryPath, options.DatabasePath); err != nil {
		_ = os.Rename(previousPath, options.DatabasePath)
		return fmt.Errorf("publish database: %w", err)
	}
	if err := syncDirectory(filepath.Dir(options.DatabasePath)); err != nil {
		_ = os.Remove(options.DatabasePath)
		_ = os.Rename(previousPath, options.DatabasePath)
		return fmt.Errorf("sync database directory: %w", err)
	}
	if err := reloadNginxAfterGeoIPUpdate(); err != nil {
		_ = os.Remove(options.DatabasePath)
		_ = os.Rename(previousPath, options.DatabasePath)
		return fmt.Errorf("reload nginx with database: %w", err)
	}
	_ = os.Remove(previousPath)
	return nil
}

func validateGeoIPDatabase(path string) error {
	lookup, err := exec.LookPath("mmdblookup")
	if err != nil {
		return fmt.Errorf("mmdblookup is required to validate the country database: %w", err)
	}
	for _, address := range []string{"8.8.8.8", "1.1.1.1"} {
		command := exec.Command(lookup, "--file", path, "--ip", address, "country", "iso_code")
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("validate database for %s: %w: %s", address, err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func reloadNginxAfterGeoIPUpdate() error {
	if _, err := exec.LookPath("nginx"); err != nil {
		return fmt.Errorf("nginx is required: %w", err)
	}
	if output, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		return fmt.Errorf("nginx -t: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("systemctl", "reload", "nginx").CombinedOutput(); err != nil {
		return fmt.Errorf("reload nginx: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
