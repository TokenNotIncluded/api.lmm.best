package deploymentfence

// OwnerBinding is the canonical durable owner wire format shared by native
// controllers and explicitly owner-bound schema workers. Fields and ordering
// retain the existing signed capsule and exact-CAS representation.
type OwnerBinding struct {
	Format             int    `json:"format"`
	State              string `json:"state"`
	DeploymentID       string `json:"deployment_id"`
	Host               string `json:"host"`
	PlanSHA256         string `json:"plan_sha256"`
	ContractSHA256     string `json:"contract_sha256"`
	ProviderSHA256     string `json:"provider_sha256"`
	Nonce              string `json:"nonce"`
	HolderPID          int    `json:"holder_pid"`
	HolderUnit         string `json:"holder_unit"`
	HolderInvocationID string `json:"holder_invocation_id"`
	BackendPID         int32  `json:"backend_pid"`
	SystemIdentifier   string `json:"system_identifier"`
	Database           string `json:"database"`
	DatabaseOID        int64  `json:"database_oid"`
	Schema             string `json:"schema"`
	SchemaOID          int64  `json:"schema_oid"`
	Role               string `json:"role"`
	Purpose            string `json:"purpose,omitempty"`
	Service            string `json:"service,omitempty"`
	StartInvocationID  string `json:"start_invocation_id,omitempty"`
}
