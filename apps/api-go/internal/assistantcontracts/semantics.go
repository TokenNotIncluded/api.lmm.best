package assistantcontracts

import "go/ast"

// refine records business semantics that cannot be recovered from a JSON tag.
// Keep these narrow and tied to the named controller/model implementation.
// Normal request fields continue to come from the decoder's real Go type.
func (g *generator) refine(name string, c *contract) {
	switch name {
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
		c.notes = append(c.notes, "value is converted to a string by this route. For JSON-valued options such as ModelRatio, ModelPrice and CompletionRatio, send a JSON-encoded string, never a nested object. Read existing option values, merge only requested model entries, then validate with ValidateOptions and write related options with UpdateOptionsBulk.")
	case "UpdateOptionsBulk", "ValidateOptions":
		c.body["required"] = []string{"values"}
		c.notes = append(c.notes, "values is a non-empty map of option keys to STRING values, with at most 128 entries. JSON settings must be JSON-encoded strings. Read and merge existing pricing maps before replacement. ValidateOptions performs the same option validation without persisting; UpdateOptionsBulk writes the complete related set in one database transaction.")
	case "UpdateAdvancedSecuritySettings":
		c.body["required"] = []string{"enabled", "on_prompt", "action", "rules"}
		c.notes = append(c.notes, "This replaces the full advanced-security policy. Read GetAdminSecurityPolicy first and preserve unrelated rules. rules must be a JSON object or array; enabled and on_prompt must be explicit booleans.")
	case "AdminCreateSubscriptionPlan", "AdminUpdateSubscriptionPlan":
		c.body["required"] = []string{"plan"}
		c.notes = append(c.notes, "plan is a nested object. title is required; price_amount is real fiat in currency CNY or USD, between 0 and 9999. total_amount is internal quota units. Read the existing plan before updating and preserve unrelated billing, duration and reset settings. Payment compliance must already be enabled.")
	case "CreateUser":
		c.body["required"] = []string{"username", "password"}
		c.notes = append(c.notes, "Create only a user with a lower role than the caller. Server-generated and read-only fields should be omitted. Administrator permissions are separately checked by the server.")
	}
}
