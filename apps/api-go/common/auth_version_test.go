package common

import "testing"

func TestDashboardAuthVersionWireContract(t *testing.T) {
	// Keep one explicit assertion for the externally visible protocol marker.
	if got := DashboardAuthVersion(); got != "864b7076dbcd0a3c01b5520316720ebf" {
		t.Fatalf("unexpected Auth-Version: %q", got)
	}
}
