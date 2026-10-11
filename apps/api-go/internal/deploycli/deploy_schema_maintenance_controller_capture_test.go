//go:build linux

package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// This runner records a fixed SSH invocation and returns fixture bytes. It
// makes no network request and does not represent native remote qualification.
type schemaControllerCaptureRunner struct {
	output   []byte
	err      error
	commands []productionCommand
}

func (r *schemaControllerCaptureRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	r.commands = append(r.commands, command)
	return append([]byte(nil), r.output...), r.err
}

func schemaControllerCaptureFixture(t *testing.T, current int, action string) (*productionSchemaMaintenanceController, productionSchemaMaintenanceOriginPlan, schemaMaintenanceRPCReply, *schemaControllerCaptureRunner) {
	t.Helper()
	host, plan, preSHA, recovery, _ := newSchemaMaintenanceRecoveryFixture(t)
	raw, err := canonicalProductionSchemaMaintenanceRecoveryBinding(recovery, plan, preSHA, host.runtime.paths)
	if err != nil {
		t.Fatal(err)
	}
	schemaMaintenanceRecoveryWriteFile(t, filepath.Join(plan.Origins[current].Workspace, "state", "recovery-binding.json"), raw)
	origin := plan.Origins[1-current]
	post := recovery.Origins[1-current]
	capture := productionSchemaMaintenanceHostCapture{
		Format: 1, TransitionID: plan.TransitionID, PlanSHA256: preSHA, Host: origin.Host,
		Phase: "WRITER_STOPPED", CapsuleSHA256: origin.OldCapsule.SHA256, ProviderSHA256: origin.Recovery.Candidate.PayloadSHA256,
		PID: 123, InvocationID: strings.Repeat("a", 32), OriginalAdmissionSHA256: strings.Repeat("b", 64), ClosedAdmissionSHA256: strings.Repeat("c", 64),
		NginxPID: 456, NginxInvocationID: strings.Repeat("d", 32), NginxGenerationSHA256: strings.Repeat("e", 64),
		StopStartedUTC: time.Date(2026, 10, 12, 1, 2, 3, 0, time.UTC), ShutdownJournalSHA256: strings.Repeat("f", 64),
	}
	switch action {
	case "capture-closed":
		capture.Phase = "ADMISSION_CLOSED"
	case "capture-recovery", "capture-ready":
		capture.Phase = "RECOVERY_READY"
		capture.ConfirmedCapsuleSHA256, capture.ConfirmedProviderSHA256 = post.Capsule.SHA256, origin.Recovery.Candidate.PayloadSHA256
		if action == "capture-ready" {
			capture.Phase = "ADMISSION_RESTORED"
			capture.ConfirmedPID, capture.ConfirmedInvocationID = 789, strings.Repeat("1", 32)
		}
	}
	result, err := json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	reply := schemaMaintenanceRPCReply{Protocol: schemaMaintenanceRPCProtocol, Binding: schemaMaintenanceRPCBinding{
		PlanSHA256: preSHA, TransitionID: plan.TransitionID, DaemonPID: 987,
		DaemonInvocationID: strings.Repeat("2", 32), DaemonExeSHA256: plan.Worker.Provider.DeployEngineSHA256, DaemonStartTicks: 12345,
	}, Action: action, Nonce: strings.Repeat("3", 64), OK: true, Result: result}
	encoded, err := schemaMaintenanceRPCMarshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	runner := &schemaControllerCaptureRunner{output: append(encoded, '\n')}
	host.runtime.runner = runner
	controller := &productionSchemaMaintenanceController{runtime: host.runtime, plan: plan, planPath: host.binding.PlanPath, planSHA: preSHA, origin: plan.Origins[current]}
	return controller, origin, reply, runner
}

func TestSchemaControllerRemoteCaptureUsesBothFixedAliasesAndBudgets(t *testing.T) {
	for current := range 2 {
		for _, action := range []string{"capture-closed", "capture-stopped", "capture-recovery", "capture-ready"} {
			t.Run(action+"-from-"+merchantStartupOrigins[current].Host, func(t *testing.T) {
				controller, origin, _, runner := schemaControllerCaptureFixture(t, current, action)
				capture, err := controller.remoteCapture(context.Background(), origin, action)
				if err != nil || capture.Host != origin.Host {
					t.Fatalf("canonical remote capture: %+v, %v", capture, err)
				}
				if len(runner.commands) != 1 {
					t.Fatalf("capture retried or issued other commands: %d", len(runner.commands))
				}
				command := runner.commands[0]
				engine := filepath.Join(filepath.Dir(origin.ArtifactCapsule.Path), "tmp", "migrations", "merchant-store-candidate", deployEngineName)
				words := []string{engine, "production", "schema-maintenance", "rpc", "--plan", controller.planPath, "--plan-sha256", controller.planSHA, "--action", action}
				for i := range words {
					words[i] = schemaMaintenanceSSHWord(words[i])
				}
				expected := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ConnectTimeout=10", origin.Alias, strings.Join(words, " ")}
				budget, err := schemaMaintenanceRPCActionTimeout(action)
				if err != nil || command.Name != commandSSH || !reflect.DeepEqual(command.Args, expected) || command.Timeout != budget+10*time.Second || command.OutputLimit != 65536 || len(command.Env) != 0 {
					t.Fatalf("remote capture escaped its fixed SSH/action budget: %+v, %v", command, err)
				}
			})
		}
	}
}

func TestSchemaControllerRemoteCaptureRejectsAmbiguousOrWrongTypedResults(t *testing.T) {
	cases := map[string]func(*schemaMaintenanceRPCReply){
		"wrong transition": func(r *schemaMaintenanceRPCReply) { r.Binding.TransitionID = "other-transition" },
		"wrong plan":       func(r *schemaMaintenanceRPCReply) { r.Binding.PlanSHA256 = strings.Repeat("4", 64) },
		"wrong engine":     func(r *schemaMaintenanceRPCReply) { r.Binding.DaemonExeSHA256 = strings.Repeat("4", 64) },
		"wrong action":     func(r *schemaMaintenanceRPCReply) { r.Action = "capture-closed" },
		"invalid nonce":    func(r *schemaMaintenanceRPCReply) { r.Nonce = "invalid" },
		"failed reply":     func(r *schemaMaintenanceRPCReply) { r.OK, r.Error = false, "native capture failed" },
		"untyped extra member": func(r *schemaMaintenanceRPCReply) {
			r.Result = append(r.Result[:len(r.Result)-1:len(r.Result)-1], []byte(`,"all_stopped":true}`)...)
		},
		"wrong host": func(r *schemaMaintenanceRPCReply) {
			r.Result = bytes.Replace(r.Result, []byte(`"host":"dmit-ubuntu"`), []byte(`"host":"arch-dmit"`), 1)
		},
		"unproved stopped phase": func(r *schemaMaintenanceRPCReply) {
			r.Result = bytes.Replace(r.Result, []byte(`"phase":"WRITER_STOPPED"`), []byte(`"phase":"WRITER_STOPPED_UNSEALED"`), 1)
		},
		"wrong writer": func(r *schemaMaintenanceRPCReply) {
			var capture productionSchemaMaintenanceHostCapture
			_ = json.Unmarshal(r.Result, &capture)
			capture.ProviderSHA256 = strings.Repeat("4", 64)
			r.Result, _ = json.Marshal(capture)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			controller, origin, reply, runner := schemaControllerCaptureFixture(t, 0, "capture-stopped")
			mutate(&reply)
			encoded, err := schemaMaintenanceRPCMarshal(reply)
			if err != nil {
				t.Fatal(err)
			}
			runner.output = append(encoded, '\n')
			if _, err := controller.remoteCapture(context.Background(), origin, "capture-stopped"); err == nil {
				t.Fatal("ambiguous or unrelated typed capture accepted")
			}
		})
	}
	for name, mutate := range map[string]func([]byte) []byte{
		"duplicate envelope": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"ok":true`), []byte(`"ok":true,"ok":true`), 1)
		},
		"duplicate capture": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"pid":123`), []byte(`"pid":123,"pid":123`), 1)
		},
		"missing typed field": func(raw []byte) []byte { return bytes.Replace(raw, []byte(`"nginx_pid":456,`), nil, 1) },
		"second JSON value":   func(raw []byte) []byte { return append(raw, []byte(`{}`)...) },
	} {
		t.Run(name, func(t *testing.T) {
			controller, origin, _, runner := schemaControllerCaptureFixture(t, 0, "capture-stopped")
			runner.output = mutate(runner.output)
			if _, err := controller.remoteCapture(context.Background(), origin, "capture-stopped"); err == nil {
				t.Fatal("noncanonical or incomplete typed capture accepted")
			}
		})
	}
}

func TestSchemaControllerRemoteCaptureFailureNeverRetries(t *testing.T) {
	controller, origin, _, runner := schemaControllerCaptureFixture(t, 0, "capture-stopped")
	runner.err = errors.New("fixture SSH timeout after possible dispatch")
	if _, err := controller.remoteCapture(context.Background(), origin, "capture-stopped"); err == nil || len(runner.commands) != 1 {
		t.Fatalf("uncertain remote capture retried: %d, %v", len(runner.commands), err)
	}
	runner.commands = nil
	if _, err := controller.remoteCapture(context.Background(), origin, "arbitrary-command"); err == nil || len(runner.commands) != 0 {
		t.Fatal("unknown action reached SSH transport")
	}
	if word := schemaMaintenanceSSHWord("literal'$(command)"); word != "'literal'\\''$(command)'" {
		t.Fatalf("SSH argument is not literal single-quoted data: %q", word)
	}
}
