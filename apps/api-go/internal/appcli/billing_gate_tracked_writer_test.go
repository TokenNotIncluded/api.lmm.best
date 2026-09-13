//go:build !windows

package appcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type trackedReportRunner struct {
	base    *fakeProductionRunner
	report  string
	unowned bool
}

func (r trackedReportRunner) Run(ctx context.Context, command productionCommand) ([]byte, error) {
	if r.unowned && command.Name == commandPacman && strings.Contains(strings.Join(command.Args, " "), refundTaskDrainCapability) {
		return []byte("another-package\n"), nil
	}
	out, err := r.base.Run(ctx, command)
	if err == nil && command.Name == commandSystemctl && !r.base.serviceActive && strings.Contains(strings.Join(command.Args, " "), "InvocationID") {
		out = []byte(strings.ReplaceAll(string(out), "InvocationID=11111111111111111111111111111111", "InvocationID="))
	}
	if err == nil && command.Name == commandJournalctl && strings.Contains(strings.Join(command.Args, " "), "_PID=") && r.report != "" {
		out = append(out, []byte(r.report+"\n")...)
	}
	return out, err
}

const validTrackedReport = "[SYS] 2026/09/13 - 10:20:00 | refund_tasks execution_complete=true accepted=2 finished=2 active=0 failed=0 (execution completion is not financial success)"

func TestTrackedRefundWriterUsesCompletionInsteadOfHistoricalAbsence(t *testing.T) {
	for _, tc := range []struct {
		name, report, marker           string
		mismatch, unowned, wantSuccess bool
	}{
		{name: "valid", report: validTrackedReport, marker: "v1\n", wantSuccess: true},
		{name: "missing report", marker: "v1\n"},
		{name: "failed report", report: strings.Replace(validTrackedReport, "failed=0", "failed=1", 1), marker: "v1\n"},
		{name: "unknown marker", report: validTrackedReport, marker: "v2\n"},
		{name: "different running binary", report: validTrackedReport, marker: "v1\n", mismatch: true},
		{name: "unowned marker", report: validTrackedReport, marker: "v1\n", unowned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProductionFixture(t)
			f.runner.refundIntent = true
			f.runner.missingStartup = true
			f.runner.journalLoss = true
			marker := filepath.Join(filepath.Dir(f.runtime.paths.GoRevisionFile), refundTaskDrainCapability)
			if err := os.WriteFile(marker, []byte(tc.marker), 0644); err != nil {
				t.Fatal(err)
			}
			f.runtime.billingExecutableSHA256 = func(int) (string, error) {
				if tc.mismatch {
					return strings.Repeat("0", 64), nil
				}
				return sha256File(filepath.Join(filepath.Dir(f.runtime.paths.InstalledBinary), backendGoName))
			}
			f.runtime.runner = trackedReportRunner{base: f.runner, report: tc.report, unowned: tc.unowned}
			// A previous legacy acknowledgement must not interfere with a verified tracked writer.
			if err := os.MkdirAll(filepath.Join(f.workspace.root, "state"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.workspace.root, "state", "legacy-refund-risk.json"), []byte("obsolete"), 0600); err != nil {
				t.Fatal(err)
			}
			status, err := f.runtime.apply(context.Background(), f.workspace, f.options)
			if tc.wantSuccess {
				if err != nil || status.Phase != "AWAITING_CONFIRMATION" {
					t.Fatalf("status=%+v err=%v", status, err)
				}
				manifest, err := f.runtime.readManifest(f.workspace)
				if err != nil {
					t.Fatal(err)
				}
				if !manifest.BillingGate.StopVerified || manifest.BillingGate.ShutdownJournalSHA256 == "" || manifest.BillingGate.InvocationJournalSHA256 != "" {
					t.Fatalf("incorrect completion evidence: %+v", manifest.BillingGate)
				}
			} else if err == nil {
				t.Fatal("invalid tracker evidence accepted")
			}
			if tc.mismatch || tc.unowned || tc.marker != "v1\n" {
				for _, event := range f.runner.events {
					if event == "systemd-stop" {
						t.Fatal("unverified tracker stopped the writer")
					}
				}
			}
		})
	}
}
