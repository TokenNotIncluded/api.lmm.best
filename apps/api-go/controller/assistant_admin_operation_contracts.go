package controller

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:generate go run ../cmd/assistant-contracts -root ..
//go:embed assistant_admin_operation_contracts.json
var assistantAdminOperationContractsJSON []byte

var assistantAdminOperationContracts = sync.OnceValue(func() map[string]json.RawMessage {
	var contracts map[string]json.RawMessage
	if err := json.Unmarshal(assistantAdminOperationContractsJSON, &contracts); err != nil {
		panic("invalid embedded assistant operation contracts: " + err.Error())
	}
	return contracts
})

// assistantAdminOperationContract documents the existing HTTP request format.
// Permissions and execution are owned by the authenticated route registry;
// the presence of a contract never grants access to an operation.
func assistantAdminOperationContract(handlerName string) map[string]any {
	name := handlerName[strings.LastIndex(handlerName, ".")+1:]
	var contract map[string]any
	if raw, ok := assistantAdminOperationContracts()[name]; ok && json.Unmarshal(raw, &contract) == nil {
		contract["usage"] = "Schema describes HTTP decoding, not all business validation. Query and path values use HTTP strings. Read existing values before changing configuration; preserve unrelated fields. Existing API authorization, validation and security verification still apply. Never invent undocumented request fields."
		return contract
	}
	return map[string]any{"contract_status": "unavailable", "limitations": "No source-derived request contract is available for this handler. Do not guess a payload."}
}
