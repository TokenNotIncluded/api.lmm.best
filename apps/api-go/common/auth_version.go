package common

import _ "embed"

//go:embed auth_version.txt
var dashboardAuthVersion string

// DashboardAuthVersion identifies the dashboard authentication wire contract.
// Rust includes the same canonical file at compile time.
func DashboardAuthVersion() string { return dashboardAuthVersion }
