// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package toolmarket

import (
	"encoding/json"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/redaction"
)

// Results written by older versions must pass the same output filter as new
// results. Reading a result never contacts the provider or rewrites a quote.
// The current connection secrets are read only for the same user/account;
// disabled installations still need masking, so selected() is not used here.
func (m *Module) executionResult(p Principal, credential string, record Execution) (json.RawMessage, error) {
	if record.Owner != p || !p.valid() {
		return nil, ErrMissing
	}
	var secret Secret
	if err := m.store.view(func(d database) error {
		if i, ok := d.Installs[record.Charge.Installation]; ok && i.Owner == p {
			secret = d.Secrets[i.ID]
		}
		return nil
	}); err != nil {
		return nil, err
	}
	result, err := redaction.JSON(record.Result, credential, secret.Access, secret.Refresh)
	if err != nil {
		return nil, ErrPending
	}
	return result, nil
}
