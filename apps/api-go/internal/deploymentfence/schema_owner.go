package deploymentfence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var maintenanceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
var maintenanceHexPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var maintenanceInvocationPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var maintenanceSchemaPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ParseSchemaMaintenanceOwner does not accept an ordinary deploy/start owner.
// Its canonical bytes are also compared with the sole reserved database row.
func ParseSchemaMaintenanceOwner(body []byte) (OwnerBinding, error) {
	var owner OwnerBinding
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if len(body) == 0 || len(body) > 16384 || decoder.Decode(&owner) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return owner, errors.New("invalid schema maintenance owner")
	}
	identifier, err := strconv.ParseUint(owner.SystemIdentifier, 10, 64)
	if owner.Format != 1 || owner.State != "ACTIVE" || owner.Purpose != "schema-maintenance" ||
		!maintenanceIDPattern.MatchString(owner.DeploymentID) || !maintenanceIDPattern.MatchString(owner.Host) ||
		!maintenanceHexPattern.MatchString(owner.PlanSHA256) || !maintenanceHexPattern.MatchString(owner.ContractSHA256) ||
		!maintenanceHexPattern.MatchString(owner.ProviderSHA256) || !maintenanceInvocationPattern.MatchString(owner.Nonce) ||
		!maintenanceInvocationPattern.MatchString(owner.HolderInvocationID) || owner.HolderPID <= 1 || owner.BackendPID <= 1 ||
		owner.HolderUnit != "lmm-schema-maintenance-"+owner.DeploymentID+".service" || owner.Service != "lmm-api.service" || owner.StartInvocationID != "" ||
		owner.Database == "" || len(owner.Database) > 63 || owner.DatabaseOID <= 0 || owner.SchemaOID <= 0 ||
		!maintenanceSchemaPattern.MatchString(owner.Schema) || strings.HasPrefix(owner.Schema, "pg_") || owner.Schema == "information_schema" ||
		owner.Role == "" || len(owner.Role) > 63 || strings.ContainsAny(owner.Database+owner.Role, "\x00\r\n") ||
		err != nil || identifier == 0 || strconv.FormatUint(identifier, 10) != owner.SystemIdentifier {
		return owner, errors.New("invalid schema maintenance owner binding")
	}
	canonical, err := json.Marshal(owner)
	if err != nil || !bytes.Equal(canonical, body) {
		return owner, errors.New("noncanonical schema maintenance owner")
	}
	return owner, nil
}

func ReadSchemaMaintenanceOwner(path, expectedSHA256 string) (OwnerBinding, []byte, error) {
	if !maintenanceHexPattern.MatchString(expectedSHA256) {
		return OwnerBinding{}, nil, errors.New("invalid schema maintenance owner digest")
	}
	body, err := readSchemaMaintenanceOwnerFile(path)
	if err != nil {
		return OwnerBinding{}, nil, err
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != expectedSHA256 {
		return OwnerBinding{}, nil, errors.New("schema maintenance owner digest differs")
	}
	owner, err := ParseSchemaMaintenanceOwner(body)
	return owner, body, err
}
