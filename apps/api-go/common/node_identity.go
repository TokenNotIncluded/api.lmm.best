package common

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	APIInstanceSlotEnv             = "LMM_API_INSTANCE_SLOT"
	APIInstanceSlotMaxLength       = 32
	systemInstanceReporterIDMaxLen = 128
)

var apiInstanceSlotPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9_-]*[a-z0-9])?$`)

type NodeIdentity struct {
	Name                    string `json:"name"`
	Source                  string `json:"source"`
	ManuallyConfigured      bool   `json:"manually_configured"`
	ShouldConfigureManually bool   `json:"should_configure_manually"`
}

func initNodeNameIdentity() error {
	if envNodeName := os.Getenv("NODE_NAME"); envNodeName != "" {
		NodeName = envNodeName
		NodeNameSource = NodeNameSourceManual
		NodeNameManuallyConfigured = true
	} else {
		hostname, _ := os.Hostname()
		NodeName = hostname
		NodeNameSource = NodeNameSourceHostname
		NodeNameManuallyConfigured = false
	}

	APIInstanceSlot = os.Getenv(APIInstanceSlotEnv)
	if _, err := DeriveSystemInstanceReporterID(NodeName, APIInstanceSlot); err != nil {
		return fmt.Errorf("invalid %s: %w", APIInstanceSlotEnv, err)
	}
	return nil
}

// DeriveSystemInstanceReporterID returns the database/API identity used only by
// the Go system-instance heartbeat reporter. Leaving slot unset preserves the
// legacy NodeName identity exactly; a configured slot does not change NodeName
// for logs, tasks, or other physical-node semantics.
func DeriveSystemInstanceReporterID(nodeName string, slot string) (string, error) {
	if strings.TrimSpace(nodeName) == "" {
		return "", fmt.Errorf("node name is empty")
	}
	if err := ValidateAPIInstanceSlot(slot); err != nil {
		return "", err
	}
	if slot == "" {
		return nodeName, nil
	}

	reporterID := nodeName + "@" + slot
	if len(reporterID) > systemInstanceReporterIDMaxLen {
		return "", fmt.Errorf("reporter identity exceeds %d characters", systemInstanceReporterIDMaxLen)
	}
	return reporterID, nil
}

// ValidateAPIInstanceSlot accepts a deliberately narrow identifier alphabet so
// the value remains safe in database keys, URLs, logs, and operator-facing UI.
func ValidateAPIInstanceSlot(slot string) error {
	if slot == "" {
		return nil
	}
	if len(slot) > APIInstanceSlotMaxLength {
		return fmt.Errorf("must be at most %d characters", APIInstanceSlotMaxLength)
	}
	if !apiInstanceSlotPattern.MatchString(slot) {
		return fmt.Errorf("must use lowercase letters, digits, hyphens, or underscores and start and end with a letter or digit")
	}
	return nil
}

func GetNodeIdentity() NodeIdentity {
	return NodeIdentity{
		Name:                    NodeName,
		Source:                  NodeNameSource,
		ManuallyConfigured:      NodeNameManuallyConfigured,
		ShouldConfigureManually: !NodeNameManuallyConfigured,
	}
}
