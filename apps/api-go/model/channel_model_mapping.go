package model

import (
	"encoding/json"
	"errors"
)

// ResolveChannelModelName follows the same alias chain as relay model mapping.
// Capability metadata and channel tests need the final name before choosing a
// native request format; the public model name remains unchanged.
func ResolveChannelModelName(modelName, mappingJSON string) (string, error) {
	if mappingJSON == "" || mappingJSON == "{}" {
		return modelName, nil
	}
	var mapping map[string]string
	if err := json.Unmarshal([]byte(mappingJSON), &mapping); err != nil {
		return "", errors.New("unmarshal_model_mapping_failed")
	}
	current := modelName
	visited := map[string]bool{current: true}
	for {
		next := mapping[current]
		if next == "" || next == current {
			return current, nil
		}
		if visited[next] {
			return "", errors.New("model_mapping_contains_cycle")
		}
		visited[next] = true
		current = next
	}
}
