package deploycli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	deployEngineName          = "lmm-api-deploy-engine"
	deployEngineInstalledPath = "/usr/lib/lmm-api-deploy/engine"
	deployEnginePackageMember = "usr/lib/lmm-api-deploy/engine"
)

// Legacy signed releases used their provider as the deployment program. New
// releases bind a separate executable in the same signed archive. Missing or
// changed modern tools never fall back to a provider.
func (p productionReleasePackagePlan) deploymentSHA256() string {
	if p.DeployEngineSHA256 != "" {
		return p.DeployEngineSHA256
	}
	return p.PayloadSHA256
}

func (p productionPackageMetadata) deploymentSHA256() string {
	if p.DeployEngineSHA256 != "" {
		return p.DeployEngineSHA256
	}
	return p.BinarySHA256
}

func productionRemoteEnginePath(plan productionReleasePlan, state productionReleaseControllerState) string {
	if plan.GoCandidate.DeployEngineSHA256 != "" {
		return filepath.Join(state.RemoteWorkspace, "staging", deployEngineName)
	}
	return productionRemoteOperatorPath(state)
}

// The absence of a member must be proved by a successful inventory read, not
// inferred from a failed extraction. Inventory and payload remain signature
// bound by the caller and by verifySignedPackageLayout.
func readOptionalDeployEngine(ctx context.Context, runner productionCommandRunner, archive string, signed bool) ([]byte, error) {
	listing, err := runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-tf", archive}})
	if err != nil {
		return nil, fmt.Errorf("inspect deployment tool inventory: %w", err)
	}
	member := ""
	for _, item := range strings.Split(strings.TrimSpace(string(listing)), "\n") {
		item = strings.TrimSuffix(strings.TrimPrefix(item, "./"), "/")
		match := item == deployEnginePackageMember
		if signed {
			parts := strings.Split(item, "/")
			match = len(parts) == 2 && parts[0] != "" && parts[0] != "." && parts[0] != ".." && parts[1] == deployEngineName
		}
		if !match {
			continue
		}
		if member != "" {
			return nil, errors.New("duplicate deployment tool archive member")
		}
		member = item
	}
	if member == "" {
		return nil, nil
	}
	body, err := runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", archive, member}})
	if err != nil || len(body) == 0 {
		return nil, errors.New("deployment tool archive member is missing or unreadable")
	}
	return body, nil
}

func (runtime *productionReleaseRuntime) verifyRemoteDeployEngine(ctx context.Context, alias, path, digest string) error {
	if !productionSHA256Pattern.MatchString(digest) || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, " \t\r\n") {
		return errors.New("deployment tool identity is invalid")
	}
	if _, err := runtime.ssh(ctx, alias, time.Minute, "test", "!", "-L", path); err != nil {
		return errors.New("deployment tool is a symlink")
	}
	info, err := runtime.ssh(ctx, alias, time.Minute, "stat", "-c", "%F %u %h %a", "--", path)
	if err != nil {
		return fmt.Errorf("inspect deployment tool: %w", err)
	}
	// Staged tools are private; installed ones are root-owned and public only
	// for execution. Neither group nor other users may change them.
	if strings.TrimSpace(string(info)) != "regular file 0 1 700" && strings.TrimSpace(string(info)) != "regular file 0 1 755" {
		return errors.New("deployment tool has unsafe ownership, type or mode")
	}
	actual, err := runtime.remoteFileSHA256(ctx, alias, path)
	if err != nil || actual != digest {
		return errors.New("deployment tool digest differs from signed release")
	}
	return nil
}

func deploymentInstalledCommand(provider string, p productionReleasePackagePlan) string {
	if p.DeployEngineSHA256 != "" {
		return deployEngineInstalledPath
	}
	return provider
}

func merchantStoreCapsuleEnginePath(c productionMerchantStoreCapsule) string {
	directory := filepath.Join(c.Root, "tmp", "migrations", "merchant-store-candidate")
	if c.Candidate.DeployEngineSHA256 != "" {
		return filepath.Join(directory, deployEngineName)
	}
	return filepath.Join(directory, productionCandidateLinkName)
}

func merchantStoreHeldEngineEntrypoint(workspace string, manifest productionManifest) string {
	legacy := merchantStoreHeldStartEntrypoint(workspace)
	if manifest.DeployEngineSHA256 != "" {
		return filepath.Join(filepath.Dir(legacy), deployEngineName)
	}
	return legacy
}

// Startup capture can inspect either historic provider hooks or the one fixed
// standalone tool. Capsule verification later binds its exact signed digest.
func merchantStoreLoadedOperator(loaded map[string]string, provider string) string {
	semantic, err := existingSchemaCommandSemantics(loaded["ExecStartPre"])
	if err == nil && strings.HasPrefix(semantic, deployEngineInstalledPath+"\x00") {
		return deployEngineInstalledPath
	}
	return provider
}

func (m productionManifest) deploymentSHA256() string {
	if m.DeployEngineSHA256 != "" {
		return m.DeployEngineSHA256
	}
	if m.MerchantStoreWriter != nil {
		return m.MerchantStoreWriter.Candidate.PayloadSHA256
	}
	return m.ProbeBinarySHA256
}

func (c productionMerchantStoreCapsule) deploymentSHA256() string {
	if c.Candidate.DeployEngineSHA256 != "" {
		return c.Candidate.DeployEngineSHA256
	}
	if c.Writer != nil {
		return c.Writer.Candidate.PayloadSHA256
	}
	return c.Candidate.PayloadSHA256
}
