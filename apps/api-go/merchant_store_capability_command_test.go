package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/internal/appcli"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

func TestMerchantStoreCapabilityCommandDoesNotOpenDatabase(t *testing.T) {
	// These deliberately invalid inputs would reject any database/resource
	// initialization. Publication needs the compiled constant alone.
	t.Setenv("SQL_DSN", "invalid-capability-fixture-database")
	t.Setenv("LMM_CREDIT_TRANSITION_PLAN", "invalid-capability-fixture-plan")
	t.Setenv("LMM_CREDIT_TRANSITION_SHA256", "invalid-capability-fixture-sha")
	var out, errs bytes.Buffer
	code := runMerchantStoreWriterGateCommand([]string{"capability"}, &out, &errs)
	if code != appcli.ExitOK || out.String() != fmt.Sprintf("%d\n", model.MerchantStoreWriterCapability) || errs.Len() != 0 {
		t.Fatalf("compiled capability getter: code=%d output=%q error=%q", code, out.String(), errs.String())
	}
}

type merchantStoreCapabilityFailWriter struct{}

func (merchantStoreCapabilityFailWriter) Write([]byte) (int, error) {
	return 0, errors.New("fixture output unavailable")
}

func TestMerchantStoreCapabilityCommandRejectsOverridesAndOutputFailure(t *testing.T) {
	for _, args := range [][]string{{"capability", "5"}, {"capability", "--require-writable"}, {"capability", "--expected-current=5"}} {
		var out, errs bytes.Buffer
		if code := runMerchantStoreWriterGateCommand(args, &out, &errs); code != appcli.ExitUsage || out.Len() != 0 {
			t.Fatalf("capability override was accepted: args=%v code=%d", args, code)
		}
	}
	var errs bytes.Buffer
	if code := runMerchantStoreWriterGateCommand([]string{"capability"}, merchantStoreCapabilityFailWriter{}, &errs); code != appcli.ExitError {
		t.Fatalf("unwritten capability returned success: %d", code)
	}
}
