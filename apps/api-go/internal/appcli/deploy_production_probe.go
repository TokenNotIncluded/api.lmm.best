package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type productionStatusResponse struct {
	Success bool `json:"success"`
	Ready   bool `json:"ready"`
	Data    struct {
		Version string `json:"version"`
	} `json:"data"`
}

type productionLiveResponse struct {
	Success bool `json:"success"`
	Live    bool `json:"live"`
}

type productionModelsResponse struct {
	Data json.RawMessage `json:"data"`
}

func (runtime *productionRuntime) runNativeRequest(ctx context.Context, binary, baseURL, path string, tokenFile string) ([]byte, error) {
	args := []string{
		"request",
		"--base-url", baseURL,
		"--path", path,
		"--timeout", productionProbeTimeout.String(),
		"--fail",
	}
	if tokenFile != "" {
		args = append(args, "--token-file", tokenFile)
	}
	return runVerifiedBinary(ctx, runtime.runner, binary, args, nil, "", productionProbeTimeout+2*time.Second, tokenFile != "")
}

func (runtime *productionRuntime) probeStatus(ctx context.Context, binary, baseURL, expectedVersion string) (string, error) {
	body, err := runtime.runNativeRequest(ctx, binary, baseURL, "/api/status", "")
	if err != nil {
		return "", err
	}
	var response productionStatusResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("decode status response: %w", err)
	}
	if !response.Success || !response.Ready || !productionVersionPattern.MatchString(response.Data.Version) {
		return "", errors.New("status response is not ready or has an invalid version")
	}
	if expectedVersion != "" && response.Data.Version != expectedVersion {
		return "", fmt.Errorf("status version=%s, want=%s", response.Data.Version, expectedVersion)
	}
	return response.Data.Version, nil
}

func (runtime *productionRuntime) probeLive(ctx context.Context, binary string) error {
	body, err := runtime.runNativeRequest(ctx, binary, runtime.paths.LocalBaseURL, "/api/livez", "")
	if err != nil {
		return err
	}
	var response productionLiveResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("decode live response: %w", err)
	}
	if !response.Success || !response.Live {
		return errors.New("live response is unhealthy")
	}
	return nil
}

func (runtime *productionRuntime) probeFrontend(ctx context.Context, binary, expectedSHA256 string) error {
	body, err := runtime.runNativeRequest(ctx, binary, runtime.paths.PublicBaseURL, "/", "")
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256Bytes(body))
	if digest != expectedSHA256 {
		return fmt.Errorf("public frontend SHA-256=%s, want=%s", digest, expectedSHA256)
	}
	return nil
}

func (runtime *productionRuntime) probeModels(ctx context.Context, binary, tokenFile string) error {
	info, err := os.Lstat(tokenFile)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() == 0 {
		return errors.New("protected production probe token is missing or unsafe")
	}
	body, err := runtime.runNativeRequest(ctx, binary, runtime.paths.PublicBaseURL, "/v1/models", tokenFile)
	if err != nil {
		return err
	}
	var response productionModelsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("decode authenticated models response: %w", err)
	}
	var models []json.RawMessage
	if len(response.Data) == 0 || json.Unmarshal(response.Data, &models) != nil {
		return errors.New("authenticated models response does not contain an array")
	}
	return nil
}

func (runtime *productionRuntime) probeBackendLocal(ctx context.Context, manifest productionManifest, version string) error {
	if _, err := runtime.probeStatus(ctx, manifest.ProbeBinary, runtime.paths.LocalBaseURL, version); err != nil {
		return fmt.Errorf("local status probe: %w", err)
	}
	if err := runtime.probeLive(ctx, manifest.ProbeBinary); err != nil {
		return fmt.Errorf("local live probe: %w", err)
	}
	return nil
}

func (runtime *productionRuntime) probeRelease(ctx context.Context, manifest productionManifest, version, frontendSHA256 string) error {
	attempts := runtime.probeAttempts
	if attempts < 1 {
		attempts = 1
	}
	var lastError error
	for attempt := 0; attempt < attempts; attempt++ {
		if _, err := runtime.probeStatus(ctx, manifest.ProbeBinary, runtime.paths.LocalBaseURL, version); err != nil {
			lastError = fmt.Errorf("local status probe: %w", err)
		} else if err := runtime.probeLive(ctx, manifest.ProbeBinary); err != nil {
			lastError = fmt.Errorf("local live probe: %w", err)
		} else if _, err := runtime.probeStatus(ctx, manifest.ProbeBinary, runtime.paths.PublicBaseURL, version); err != nil {
			lastError = fmt.Errorf("public status probe: %w", err)
		} else if err := runtime.probeFrontend(ctx, manifest.ProbeBinary, frontendSHA256); err != nil {
			lastError = fmt.Errorf("public frontend probe: %w", err)
		} else if err := runtime.probeModels(ctx, manifest.ProbeBinary, filepath.Join(filepath.Dir(manifest.ProbeBinary), "..", "state", productionProbeTokenFilename)); err != nil {
			// The probe binary is a direct staging child; state is its sibling.
			lastError = fmt.Errorf("authenticated business probe: %w", err)
		} else {
			return nil
		}
		if attempt+1 < attempts {
			runtime.sleep(time.Second)
		}
	}
	return lastError
}

func (runtime *productionRuntime) readServiceRestarts(ctx context.Context) (int64, error) {
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"show", runtime.paths.Service, "--property=NRestarts", "--value"}})
	if err != nil {
		return 0, err
	}
	return parseSingleInt(output, "NRestarts")
}

func (runtime *productionRuntime) verifyServiceRestartBaseline(ctx context.Context, manifest productionManifest) error {
	restarts, err := runtime.readServiceRestarts(ctx)
	if err != nil {
		return err
	}
	if restarts != manifest.ServiceRestartBaseline {
		return fmt.Errorf("service restart count changed: got=%d baseline=%d", restarts, manifest.ServiceRestartBaseline)
	}
	return nil
}

func (runtime *productionRuntime) checkMemoryHeadroom(ctx context.Context) error {
	read := func(property string) (int64, error) {
		output, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"show", runtime.paths.Service, "--property=" + property, "--value"}})
		if err != nil {
			return 0, err
		}
		value := strings.TrimSpace(string(output))
		if value == "infinity" || value == "max" {
			return 0, nil
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			return 0, fmt.Errorf("invalid %s value %q", property, value)
		}
		return parsed, nil
	}
	current, err := read("MemoryCurrent")
	if err != nil {
		return fmt.Errorf("read service memory usage: %w", err)
	}
	high, err := read("MemoryHigh")
	if err != nil {
		return fmt.Errorf("read service memory high watermark: %w", err)
	}
	maximum, err := read("MemoryMax")
	if err != nil {
		return fmt.Errorf("read service memory maximum: %w", err)
	}
	swapMaximum, err := read("MemorySwapMax")
	if err != nil {
		return fmt.Errorf("read service swap maximum: %w", err)
	}
	if high != 320*1024*1024 || maximum != 384*1024*1024 || swapMaximum != 256*1024*1024 {
		return fmt.Errorf("production memory guards differ from 320M/384M/256M: current=%d high=%d max=%d swap=%d", current, high, maximum, swapMaximum)
	}
	if current*100 >= high*90 {
		return fmt.Errorf("service memory pressure is too high: current=%d high=%d", current, high)
	}
	return nil
}

func (runtime *productionRuntime) verifyFrontendPermissions() error {
	current := filepath.Join(runtime.paths.FrontendRoot, "current")
	target, err := os.Readlink(current)
	if err != nil || !strings.HasPrefix(target, "releases/") {
		return errors.New("frontend current release link is unsafe")
	}
	release := filepath.Join(runtime.paths.FrontendRoot, target)
	if !pathWithinRoot(filepath.Join(runtime.paths.FrontendRoot, "releases"), release) {
		return errors.New("frontend current release escapes the release root")
	}
	for _, root := range []string{release, filepath.Join(runtime.paths.FrontendRoot, "assets")} {
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("public frontend contains a symlink: %s", path)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if info.Mode().Perm()&0o055 != 0o055 {
					return fmt.Errorf("public frontend directory is not traversable: %s mode=%o", path, info.Mode().Perm())
				}
				return nil
			}
			if !entry.Type().IsRegular() || info.Mode().Perm()&0o044 != 0o044 {
				return fmt.Errorf("public frontend file is not readable: %s mode=%o", path, info.Mode().Perm())
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func actionableJournalLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "failed (2: No such file or directory)") && strings.Contains(trimmed, " /static/") {
		return false
	}
	// A client that disconnects while nginx is proxying through a service
	// restart can leave this harmless sendfile error in the journal. It is not
	// evidence of a failed backend health gate and must not strand a confirmed
	// release in AWAITING_CONFIRMATION.
	if strings.Contains(trimmed, "sendfile() failed (32: Broken pipe)") &&
		strings.Contains(trimmed, "while sending request to upstream") {
		return false
	}
	// A downstream client can close a proxied request before nginx finishes
	// reading the upstream response. This connection-level event is expected
	// during normal traffic and is not a failed release health gate.
	if strings.Contains(trimmed, "upstream prematurely closed connection while reading upstream") {
		return false
	}
	// A request that arrives while the guarded transaction is stopping and
	// restarting the local Go service can briefly fail before the replacement
	// listener is ready.  The subsequent local/public health probes are the
	// authoritative gate; keep unrelated upstream refusals actionable.
	if strings.Contains(trimmed, "connect() failed (111: Connection refused)") &&
		strings.Contains(trimmed, "while connecting to upstream") &&
		strings.Contains(trimmed, `upstream: "http://127.0.0.1:3000/`) {
		return false
	}
	// nginx emits a companion auth_request 502 after the local access-policy
	// upstream refuses a request during the guarded service restart. The
	// native health probes below are authoritative once the listener returns.
	if strings.Contains(trimmed, "auth request unexpected status: 502") &&
		strings.Contains(trimmed, "while sending to client") {
		return false
	}
	// nginx writes this successful reload notice to stderr, so journald can
	// assign error priority even though the message itself is explicitly a
	// notice. The post-reload public and local probes remain authoritative.
	if strings.Contains(trimmed, "[notice]") && strings.HasSuffix(trimmed, "signal process started") {
		return false
	}
	return true
}

func (runtime *productionRuntime) checkErrorJournals(ctx context.Context, since time.Time) error {
	args := []string{"--quiet", "--since", "@" + strconv.FormatInt(since.Unix(), 10), "--priority=err", "--no-pager", "--output=cat"}
	for _, unit := range runtime.paths.JournalUnits {
		args = append(args, "--unit", unit)
	}
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandJournalctl, Args: args})
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(output), "\n") {
		if actionableJournalLine(line) {
			return fmt.Errorf("production error journal is not clean: %s", strings.TrimSpace(line))
		}
	}
	return nil
}

func (runtime *productionRuntime) healthCheck(ctx context.Context, workspace productionWorkspace, manifest productionManifest) error {
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"is-active", "--quiet", runtime.paths.Service}}); err != nil {
		return errors.New("lmm-api service is not active")
	}
	if err := runtime.verifyServiceRestartBaseline(ctx, manifest); err != nil {
		return err
	}
	if err := runtime.checkMemoryHeadroom(ctx); err != nil {
		return err
	}
	if err := runtime.verifyManifestInstalled(ctx, manifest, false); err != nil {
		return fmt.Errorf("installed production package identities changed: %w", err)
	}
	if err := verifyFrontendIdentity(runtime.paths.FrontendRoot, manifest.Frontend.NewTarget, manifest.Frontend.NewIndexSHA256); err != nil {
		return err
	}
	if err := verifyProductionMemoryDropIn(filepath.Join(runtime.paths.PackagedDropInDir, productionMemoryFileName)); err != nil {
		return err
	}
	if err := retireKnownMemoryOverrides(runtime.paths.DropInDir); err != nil {
		return err
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Q", "lmm-api"}}); err == nil {
		return errors.New("removed split lmm-api package reappeared")
	}
	for _, path := range runtime.paths.RemovedPaths {
		if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("removed split-architecture path remains: %s", path)
		}
	}
	if err := runtime.verifyFrontendPermissions(); err != nil {
		return err
	}
	if manifest.NginxEdgeRestoreSHA256 != "" && !manifest.PreserveEdgePolicy {
		if err := runtime.verifyEdgePolicy(ctx, runtime.paths.EdgeAssetRoot); err != nil {
			return err
		}
	}
	if err := runtime.probeRelease(ctx, manifest, manifest.ExpectedVersion, manifest.Frontend.NewIndexSHA256); err != nil {
		return err
	}
	if err := runtime.verifyServiceRestartBaseline(ctx, manifest); err != nil {
		return err
	}
	if err := runtime.checkErrorJournals(ctx, manifest.ObservationStartedUTC); err != nil {
		return err
	}
	return nil
}
