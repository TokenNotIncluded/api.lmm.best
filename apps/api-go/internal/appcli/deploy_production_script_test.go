package appcli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestProductionPromoteRetryScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("production deployment scripts require bash")
	}

	helper, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "scripts", "production-promote-retry.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(helper); err != nil {
		t.Fatalf("stat promote retry helper: %v", err)
	}

	for _, test := range []struct {
		name          string
		firstMessage  string
		firstStatus   int
		secondMessage string
		secondStatus  int
		wantStatus    int
		wantAttempts  int
	}{
		{
			name:         "ordinary failure is not retried",
			firstMessage: "validation failed",
			firstStatus:  7,
			wantStatus:   7,
			wantAttempts: 1,
		},
		{
			name:          "transport ambiguous reconciliation retries once",
			firstMessage:  "production activation became transport-ambiguous and reconciliation failed: reconcile=command ssh timed out",
			firstStatus:   9,
			secondMessage: "reconciled persisted activation",
			secondStatus:  0,
			wantStatus:    0,
			wantAttempts:  2,
		},
		{
			name:          "second failure remains a failure",
			firstMessage:  "production activation became transport-ambiguous and reconciliation failed: reconcile=command ssh timed out",
			firstStatus:   9,
			secondMessage: "reconciliation still unavailable",
			secondStatus:  11,
			wantStatus:    11,
			wantAttempts:  2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			counter := filepath.Join(t.TempDir(), "attempts")
			stderrLog := filepath.Join(t.TempDir(), "promote.stderr")
			script := `
source "$PROMOTE_HELPER"
mock_promote() {
  local count=0
  if [[ -f "$ATTEMPT_FILE" ]]; then count=$(cat "$ATTEMPT_FILE"); fi
  count=$((count + 1))
  printf '%s\n' "$count" >"$ATTEMPT_FILE"
  if (( count == 1 )); then
    printf '%s\n' "$FIRST_MESSAGE" >&2
    return "$FIRST_STATUS"
  fi
  printf '%s\n' "$SECOND_MESSAGE" >&2
  return "$SECOND_STATUS"
}
production_promote_with_transport_retry "$STDERR_LOG" mock_promote
`
			command := exec.Command("bash", "-c", script)
			command.Env = append(os.Environ(),
				"PROMOTE_HELPER="+helper,
				"ATTEMPT_FILE="+counter,
				"STDERR_LOG="+stderrLog,
				"FIRST_MESSAGE="+test.firstMessage,
				"FIRST_STATUS="+strconv.Itoa(test.firstStatus),
				"SECOND_MESSAGE="+test.secondMessage,
				"SECOND_STATUS="+strconv.Itoa(test.secondStatus),
				"PRODUCTION_PROMOTE_RETRY_DELAY_SECONDS=0",
			)
			output, runErr := command.CombinedOutput()
			status := 0
			if runErr != nil {
				var exitErr *exec.ExitError
				if !errors.As(runErr, &exitErr) {
					t.Fatalf("run helper: %v\n%s", runErr, output)
				}
				status = exitErr.ExitCode()
			}
			if status != test.wantStatus {
				t.Fatalf("status=%d want=%d output=%s", status, test.wantStatus, output)
			}
			attemptsRaw, err := os.ReadFile(counter)
			if err != nil {
				t.Fatal(err)
			}
			attempts, err := strconv.Atoi(strings.TrimSpace(string(attemptsRaw)))
			if err != nil {
				t.Fatal(err)
			}
			if attempts != test.wantAttempts {
				t.Fatalf("attempts=%d want=%d output=%s", attempts, test.wantAttempts, output)
			}
			if !strings.Contains(string(output), test.firstMessage) {
				t.Fatalf("first stderr was not preserved: %s", output)
			}
			if test.wantAttempts == 2 && !strings.Contains(string(output), test.secondMessage) {
				t.Fatalf("second stderr was not preserved: %s", output)
			}
		})
	}
}
