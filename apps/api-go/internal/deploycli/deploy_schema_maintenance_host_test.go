package deploycli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSchemaMaintenanceAdmissionCannotAdoptOtherOwner(t *testing.T) {
	original := []byte("location @lmm_api_backend {\n proxy_pass http://127.0.0.1:3000;\n}\n")
	barrier, err := schemaMaintenanceAdmissionBarrier(original, "schema-test")
	if err != nil || !bytes.HasSuffix(barrier, original) || !bytes.Contains(barrier, []byte("X-LMM-Schema-Maintenance 'schema-test'")) || !bytes.Contains(barrier, []byte("Retry-After '60'")) {
		t.Fatalf("canonical own barrier: %q %v", barrier, err)
	}
	for name, config := range map[string][]byte{
		"own previous barrier": barrier,
		"financial":            append([]byte("return 503 'lmm-credit-transition:other';\n"), original...),
		"billing":              append([]byte("return 503 'lmm-billing-drain:other';\n"), original...),
		"startup":              append([]byte(merchantStartupAdmissionKind+":other\n"), original...),
		"two upstream owners":  append(append([]byte{}, original...), original...),
		"unknown config":       []byte("return 200 'anything';\n"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := schemaMaintenanceAdmissionBarrier(config, "schema-test"); err == nil {
				t.Fatal("adopted another or unknown admission owner")
			}
		})
	}
	if _, err := schemaMaintenanceAdmissionBarrier(original, "bad'\nreturn 200;"); err == nil {
		t.Fatal("accepted injected transition identifier")
	}
}

func TestSchemaMaintenanceDirectOriginResponseBindsOwnerAndHeaders(t *testing.T) {
	good := "HTTP/1.1 503 Service Unavailable\r\nServer: nginx\r\nRetry-After: 60\r\nX-LMM-Schema-Maintenance: schema-test\r\n\r\nlmm-schema-maintenance:schema-test"
	if err := validateSchemaMaintenanceAdmissionResponse([]byte(good), "schema-test"); err != nil {
		t.Fatal(err)
	}
	for name, response := range map[string]string{
		"healthy other origin": strings.Replace(good, "503", "200", 1),
		"wrong body owner":     strings.Replace(good, "lmm-schema-maintenance:schema-test", "lmm-schema-maintenance:other", 1),
		"wrong header owner":   strings.Replace(good, "Maintenance: schema-test", "Maintenance: other", 1),
		"missing retry":        strings.Replace(good, "Retry-After: 60\r\n", "", 1),
		"wrong retry":          strings.Replace(good, "Retry-After: 60", "Retry-After: 0", 1),
		"duplicate retry":      strings.Replace(good, "Retry-After: 60", "Retry-After: 60\r\nretry-after: 60", 1),
		"trailing body":        good + "\nextra",
		"financial body":       strings.Replace(good, "lmm-schema-maintenance:", "lmm-credit-transition:", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateSchemaMaintenanceAdmissionResponse([]byte(response), "schema-test"); err == nil {
				t.Fatal("accepted an unbound direct-origin response")
			}
		})
	}
}

func TestSchemaMaintenanceRequiresFinanciallyCleanNormalShutdownReports(t *testing.T) {
	refund := "refund_tasks execution_complete=true accepted=2 finished=2 active=0 failed=0 (execution completion is not financial success)\n"
	flush := "quota dashboard flush: persisted=12 failed=0 dropped=0\n"
	good := "received signal: terminated\n" + refund + flush + "server exited\n"
	if err := validateSchemaMaintenanceShutdown([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for name, journal := range map[string]string{
		"missing refund":    strings.Replace(good, refund, "", 1),
		"missing flush":     strings.Replace(good, flush, "", 1),
		"refund incomplete": strings.Replace(good, "finished=2", "finished=1", 1),
		"refund failed":     strings.Replace(good, "active=0 failed=0", "active=0 failed=1", 1),
		"flush dropped":     strings.Replace(good, "dropped=0", "dropped=1", 1),
		"double flush":      good + flush,
		"double refund":     good + refund,
		"missing exit":      strings.Replace(good, "server exited", "", 1),
		"panic":             good + "panic: unexpected error\n",
	} {
		t.Run(name, func(t *testing.T) {
			if validateSchemaMaintenanceShutdown([]byte(journal)) == nil {
				t.Fatal("accepted incomplete/failed shutdown financial drain")
			}
		})
	}
}

type schemaMaintenanceTestLease struct {
	checks int
	err    error
}

func (l *schemaMaintenanceTestLease) Check(context.Context) error { l.checks++; return l.err }
func (l *schemaMaintenanceTestLease) Close() error                { return nil }

func TestSchemaMaintenanceHostChecksRequireLivePlanAndGuardian(t *testing.T) {
	ctx := context.Background()
	lease := &schemaMaintenanceTestLease{}
	h := &productionSchemaMaintenanceHost{runtime: &productionRuntime{}, lease: lease, validate: func(context.Context) error { return errors.New("plan changed") }}
	if h.check(ctx) == nil || lease.checks != 0 {
		t.Fatal("changed plan reached a guardian operation")
	}
	h.validate = func(context.Context) error { return nil }
	lease.err = errors.New("guardian exited")
	if h.check(ctx) == nil || lease.checks != 1 {
		t.Fatal("lost guardian permitted mutation")
	}
	h.lease = nil
	if h.check(ctx) == nil {
		t.Fatal("nil guardian permitted mutation")
	}
	if (*productionSchemaMaintenanceHost)(nil).check(ctx) == nil {
		t.Fatal("nil host permitted mutation")
	}
}

func newSchemaMaintenanceHostEvidenceFixture(t *testing.T) (*productionSchemaMaintenanceHost, productionSchemaMaintenanceHostCapture) {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "schema-test")
	if err := os.MkdirAll(filepath.Join(workspace, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	h := &productionSchemaMaintenanceHost{runtime: &productionRuntime{requiredOwnerUID: uint32(os.Geteuid())}, binding: productionSchemaMaintenanceHostBinding{TransitionID: "schema-test", PlanSHA256: strings.Repeat("a", 64), Host: "dmit-ubuntu", CapsuleSHA256: strings.Repeat("b", 64), Workspace: workspace}}
	capture := productionSchemaMaintenanceHostCapture{Format: 1, TransitionID: h.binding.TransitionID, PlanSHA256: h.binding.PlanSHA256, Host: h.binding.Host, Phase: "CLOSE_INTENT", CapsuleSHA256: h.binding.CapsuleSHA256, ProviderSHA256: strings.Repeat("c", 64), PID: 12345, InvocationID: strings.Repeat("d", 32), OriginalAdmissionSHA256: strings.Repeat("e", 64), ClosedAdmissionSHA256: strings.Repeat("f", 64), StopStartedUTC: time.Unix(1000, 0).UTC()}
	return h, capture
}

func TestSchemaMaintenanceHostEvidenceIsPrivateImmutableAndBound(t *testing.T) {
	h, capture := newSchemaMaintenanceHostEvidenceFixture(t)
	if err := h.writeCapture("close-intent.json", capture); err != nil {
		t.Fatal(err)
	}
	if got, err := h.readCapture("close-intent.json"); err != nil || got != capture {
		t.Fatalf("actual private capture: %+v %v", got, err)
	}
	if err := h.writeCapture("close-intent.json", capture); err == nil {
		t.Fatal("overwrote the original close intent")
	}
	if err := h.writeNew("../escaped", []byte("anything")); err == nil {
		t.Fatal("evidence escaped fixed state directory")
	}
	path := filepath.Join(h.binding.Workspace, "state", "close-intent.json")
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private intent mode: %v %v", info, err)
	}
	h.binding.PlanSHA256 = strings.Repeat("9", 64)
	if _, err := h.readCapture("close-intent.json"); err == nil {
		t.Fatal("accepted another immutable plan's capture")
	}
	h.binding.PlanSHA256 = capture.PlanSHA256
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte(" "), raw...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.readCapture("close-intent.json"); err == nil {
		t.Fatal("accepted noncanonical capture")
	}
}
