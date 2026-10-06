package assistantcontracts

import "go/ast"

// refine records business semantics that cannot be recovered from a JSON tag.
// Keep these narrow and tied to the named controller/model implementation.
// Normal request fields continue to come from the decoder's real Go type.
func (g *generator) refine(name string, c *contract) {
	switch name {
	case "CreateWalletTransfer":
		// The raw compatibility request and the public denomination request
		// are mutually exclusive. RawMessage alone cannot describe that rule.
		legacy := object(map[string]any{
			"quota":       map[string]any{"type": "integer", "minimum": 1, "maximum": int64(9007199254740991)},
			"request_key": map[string]any{"type": "string"},
		})
		legacy["required"] = []string{"quota"}
		legacy["not"] = map[string]any{"anyOf": []any{
			map[string]any{"required": []string{"schema_version"}},
			map[string]any{"required": []string{"amount"}},
			map[string]any{"required": []string{"unit"}},
			map[string]any{"required": []string{"expected_public_credits_per_usd_exact"}},
		}}
		versioned := func(unit string) map[string]any {
			fields := map[string]any{
				"schema_version": map[string]any{"type": "integer", "enum": []int{2}},
				"amount":         map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
				"unit":           map[string]any{"type": "string", "enum": []string{unit}},
				"request_key":    map[string]any{"type": "string"},
			}
			required := []string{"schema_version", "amount", "unit"}
			if unit == "CREDIT" {
				fields["expected_public_credits_per_usd_exact"] = map[string]any{"type": "string", "minLength": 1}
				required = append(required, "expected_public_credits_per_usd_exact")
			}
			result := object(fields)
			result["required"] = required
			result["additionalProperties"] = false
			return result
		}
		c.body = map[string]any{"oneOf": []any{legacy, versioned("LEDGER_QUOTA"), versioned("CREDIT")}}
		c.hasBody, c.unknown = true, false
		c.notes = append(c.notes, "Legacy quota and LEDGER_QUOTA are integer ledger units. Public CREDIT uses a decimal string and requires the exact public_credits_per_usd_exact value read from the current balance metadata; stale values return 409. Never mix quota with versioned fields. Keep the same request_key and exact ledger amount when retrying a transfer.")
	case "UpdateUser":
		// UpdateUser first decodes a map to detect trust_level_override and
		// then unmarshals model.User. EditWithTx persists only this field set;
		// admin_permissions is handled separately by its authorization helper.
		_ = g.load("model")
		schema, complete := g.schema(definition{ast.NewIdent("User"), &source{pkg: "model"}}, nil, map[string]bool{})
		all, _ := schema["properties"].(map[string]any)
		fields := map[string]any{}
		for _, key := range []string{"id", "username", "display_name", "group", "remark", "password", "admin_permissions", "trust_level_override"} {
			if field, ok := all[key]; ok {
				fields[key] = field
			}
		}
		c.body = object(fields)
		c.body["required"] = []string{"id", "username", "display_name", "group", "remark"}
		c.hasBody = true
		c.unknown = !complete
		c.notes = append(c.notes, "Read the target user first. Include current display_name, group and remark unless changing them: omitted values are written as empty. Omit password to preserve it. Role and status changes use ManageUser. Only root may grant administrator permissions; lower-role target restrictions still apply. trust_level_override only clears a legacy override.")
	case "UpdateChannel":
		if properties, ok := c.body["properties"].(map[string]any); ok {
			delete(properties, "status")
			c.body["required"] = []string{"id"}
		}
		c.notes = append(c.notes, "Read the channel first and retain required existing values. The status field is rejected here; use UpdateChannelStatus or BatchUpdateChannelStatus. Sensitive fields require the channel sensitive-write permission. key_mode controls key replacement versus append.")
	case "UpdateOption":
		c.body["required"] = []string{"key", "value"}
		c.notes = append(c.notes, "value is converted to a string by this route. For JSON-valued options such as ModelRatio, ModelPrice and CompletionRatio, send a JSON-encoded string, never a nested object. Read existing option values, merge only requested model entries, then validate with ValidateOptions and write related options with UpdateOptionsBulk. Locked model pricing changes are ignored with warnings; inspect warnings and locked_models before claiming a change was applied. Never unlock pricing unless explicitly requested by the administrator. For a single lock update, send key=ModelPriceLock, model=the exact model ID and value=an explicit boolean; this atomically preserves other models' locks. Prices are unlocked by default.")
	case "PutPublicCreditUnitOptions":
		_ = g.load("model")
		schema, complete := g.schema(definition{ast.NewIdent("PublicCreditUnitUpdate"), &source{pkg: "model"}}, nil, map[string]bool{})
		c.body = schema
		c.body["required"] = []string{"credit_unit_schema_version", "public_credits_per_usd_exact", "expected_public_credits_per_usd_exact", "expected_ledger_quota_per_usd_exact"}
		c.hasBody = true
		c.unknown = !complete
		if properties, ok := c.body["properties"].(map[string]any); ok {
			if version, ok := properties["credit_unit_schema_version"].(map[string]any); ok {
				version["enum"] = []int{2}
			}
		}
		c.notes = append(c.notes, "Read GetPublicCreditUnitOptions first. public_credits_per_usd_exact is a positive whole number no greater than 9007199254740991 encoded as a string. Copy both expected exact bases from that snapshot. This changes only the public denomination and never the internal ledger or wallet USD value. A concurrent change returns 409; read a new snapshot before retrying. Do not fall back to generic UpdateOption when this versioned route is unavailable.")
	case "UpdateOptionsBulk", "ValidateOptions":
		c.body["required"] = []string{"values"}
		c.notes = append(c.notes, "values is a non-empty map of option keys to STRING values, with at most 128 entries. JSON settings must be JSON-encoded strings. Read and merge existing pricing maps before replacement. ValidateOptions performs the same option validation without persisting; UpdateOptionsBulk writes the complete related set in one database transaction. Locked model pricing changes are ignored with warnings while unlocked entries still apply. Inspect warnings and locked_models and never unlock pricing unless explicitly requested by the administrator.")
	case "UpdateAdvancedSecuritySettings":
		c.body["required"] = []string{"enabled", "on_prompt", "action", "rules"}
		c.notes = append(c.notes, "This replaces the full advanced-security policy. Read GetAdminSecurityPolicy first and preserve unrelated rules. rules must be a JSON object or array; enabled and on_prompt must be explicit booleans.")
	case "AdminCreateSubscriptionPlan", "AdminUpdateSubscriptionPlan":
		c.body["required"] = []string{"plan"}
		c.notes = append(c.notes, "plan is a nested object. title is required; price_amount is real fiat in currency CNY or USD, between 0 and 9999. total_amount is internal quota units. Read the existing plan before updating and preserve unrelated billing, duration and reset settings. Payment compliance must already be enabled.")
	case "CreateUser":
		c.body["required"] = []string{"username", "password"}
		c.notes = append(c.notes, "Create only a user with a lower role than the caller. Server-generated and read-only fields should be omitted. Administrator permissions are separately checked by the server.")
	case "SaveMerchantStoreProduct":
		if properties, ok := c.body["properties"].(map[string]any); ok {
			properties["test_mode"] = map[string]any{"type": "boolean"}
			properties["visibility"] = map[string]any{"type": "string", "enum": []string{"public", "registered", "private"}}
			properties["purchase_login_required"] = map[string]any{"type": "boolean"}
		}
		c.notes = append(c.notes, "visibility is public, registered or private; private is owner-only and administrators cannot bypass it. Legacy test_mode is an alias for private/public and contradictory values are rejected. Omitted access fields preserve edits; a new product requires purchase login by default. Explicit null is rejected. Guest purchasing requires the access capability and cannot combine with account-only pickup. Saving creates a draft and invalidates the prior AI review. Public sale requires review and current merchant-authored terms; normal inventory and financial checks remain active.")
	case "SaveMyMerchantStoreTerms":
		c.body["required"] = []string{"content", "expected_version"}
		c.body["additionalProperties"] = false
		c.notes = append(c.notes, "Only the authenticated merchant's terms are changed. Content must be nonempty UTF-8 text of at most 65536 bytes. Copy expected_version from GetMyMerchantStoreTerms, using an empty string only when unconfigured. A changed content version invalidates prior agreement for new orders; existing orders retain their frozen text and version.")
	case "AcceptMerchantStoreGuestDisclaimer":
		c.body["required"] = []string{"version", "accepted"}
		c.body["additionalProperties"] = false
		if fields, ok := c.body["properties"].(map[string]any); ok {
			fields["accepted"] = map[string]any{"type": "boolean", "const": true}
		}
		c.notes = append(c.notes, "Requires the exact X-Store-Guest bearer. Read the current platform explanation first and submit explicit agreement. This agreement is distinct from merchant terms and applies only to this guest identity.")
	case "SetMerchantStoreProductSaleLimit":
		c.body["required"] = []string{"sale_limit"}
		if properties, ok := c.body["properties"].(map[string]any); ok {
			properties["sale_limit"] = map[string]any{"anyOf": []any{
				map[string]any{"type": "integer", "minimum": 0, "maximum": int64(9007199254740991)},
				map[string]any{"type": "null"},
			}}
		}
		c.notes = append(c.notes, "sale_limit is an explicit cumulative product sales ceiling, including paid obligations and outstanding reservations. null removes the ceiling; zero stops new orders. Lowering it preserves existing orders. Omitting the field is rejected, and saving product content never changes this separate limit.")
	case "RequestMerchantStoreRefund", "ProactivelyRefundMerchantStoreOrder":
		_ = g.load("model")
		c.body, _ = g.schema(definition{ast.NewIdent("MerchantStoreRefundInput"), &source{pkg: "model"}}, nil, map[string]bool{})
		c.body["required"] = []string{"request_key", "reason", "mode"}
		c.body["additionalProperties"] = false
		c.hasBody, c.unknown = true, false
		c.notes = append(c.notes, "mode is full, quantity or amount. full omits quantity/stock_ids/amount fields. quantity requires quantity and optional exact stock_ids; amount uses exactly one positive integer amount_quota (balance) or amount_minor (verified native payment). Read the authorized refund view for remaining limits and max_quantity. Keep request_key and exact body on retry. Approval for external payments means awaiting_provider, never completed without provider evidence. Original fee is retained.")
	case "DecideMerchantStoreRefund":
		_ = g.load("model")
		c.body, _ = g.schema(definition{ast.NewIdent("MerchantStoreRefundDecision"), &source{pkg: "model"}}, nil, map[string]bool{})
		c.body["required"] = []string{"decision"}
		c.body["additionalProperties"] = false
		c.hasBody, c.unknown = true, false
		c.notes = append(c.notes, "decision is approve or reject. Only the original seller or a current root administrator can decide requested refunds. Approved external payments retain a reservation until verified provider reconciliation; seller approval alone does not return money.")
	case "ReconcileMerchantStoreRefund":
		c.body = map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
		c.hasBody, c.unknown = true, false
		c.notes = append(c.notes, "Only the original seller or a current root administrator may wake/query this approved immutable refund. Send no amount, payout destination or completion proof. A single durable submission fence is committed before the first provider POST; unknown operations are only queried and never re-submitted. A ticket acceptance does not mean money was refunded.")
	case "GetMerchantStorePickupRefunds", "RequestMerchantStorePickupRefund":
		_ = g.load("model")
		c.body, _ = g.schema(definition{ast.NewIdent("MerchantStoreRefundPickupProof"), &source{pkg: "model"}}, nil, map[string]bool{})
		c.body["required"] = []string{"order_id", "token"}
		if name == "RequestMerchantStorePickupRefund" {
			input, _ := g.schema(definition{ast.NewIdent("MerchantStoreRefundInput"), &source{pkg: "model"}}, nil, map[string]bool{})
			input["required"] = []string{"request_key", "reason", "mode"}
			input["additionalProperties"] = false
			c.body["properties"].(map[string]any)["input"] = input
			c.body["required"] = []string{"order_id", "token", "input"}
		}
		c.body["additionalProperties"] = false
		c.hasBody, c.unknown = true, false
		c.notes = append(c.notes, "Private pickup proof authorizes only this order. Respect its login and pickup-code requirements. Reading or requesting a refund does not reveal card plaintext or mark the order claimed. The beneficiary is the original buyer; never send another payout account.")
	case "SetMerchantStoreProductListed":
		c.body["required"] = []string{"listed"}
		if properties, ok := c.body["properties"].(map[string]any); ok {
			properties["listed"] = map[string]any{"type": "boolean"}
		}
		c.notes = append(c.notes, "listed=false takes an approved listing off the shelf without deleting its inventory or orders. listed=true republishes only an unchanged approved off-shelf listing. Edited drafts still require review. The field must be an explicit boolean; null and omission are rejected.")
	case "SetMerchantStoreProductRemainingQuota":
		c.body["required"] = []string{"available_count"}
		if properties, ok := c.body["properties"].(map[string]any); ok {
			properties["available_count"] = map[string]any{"anyOf": []any{
				map[string]any{"type": "integer", "minimum": 0, "maximum": int64(9007199254740991)},
				map[string]any{"type": "null"},
			}}
		}
		c.notes = append(c.notes, "available_count is the remaining quantity quota, including unpaid reservations. The server adds actual paid obligations under the product lock to preserve historical sales. Paid orders reduce this remaining quota; cancellation releases held slots and importing inventory does not raise it. null makes sales unlimited; zero stops new orders while retaining fulfillment of existing orders. The field must be explicit. Only the owner or an administrator may change it; another seller's test product remains owner-only.")
	}
}
