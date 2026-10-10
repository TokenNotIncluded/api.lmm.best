// Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/pkg/bountycontract"
	"github.com/google/jsonschema-go/jsonschema"
)

func bountySchemaPointer[T any](value T) *T { return &value }

// Extend the actual argument types instead of maintaining a second tool registry.
func bountyMCPInputSchema(name string) *jsonschema.Schema {
	var schema *jsonschema.Schema
	var err error
	switch bountycontract.CanonicalToolName(name) {
	case "bounties.create_draft":
		schema, err = jsonschema.For[bountyMCPDraftInput](nil)
	case "bounties.update_draft":
		schema, err = jsonschema.For[bountyMCPUpdateDraftInput](nil)
	case "bounties.submit":
		schema, err = jsonschema.For[bountyMCPSubmitInput](nil)
	default:
		return nil
	}
	if err != nil {
		// A static programmer error, just as with mcp.AddTool's inferred schema.
		panic(err)
	}
	for key, bounds := range map[string][2]int{
		"title": {4, 120}, "description": {20, 2000}, "rules": {20, 5000},
		"submission_note": {0, 2000}, "delivery_url": {0, 2048},
	} {
		if field := schema.Properties[key]; field != nil {
			field.MinLength = bountySchemaPointer(bounds[0])
			field.MaxLength = bountySchemaPointer(bounds[1])
		}
	}
	for _, key := range []string{"project_id", "reward_quota", "reward_slots"} {
		if field := schema.Properties[key]; field != nil {
			field.Minimum = bountySchemaPointer(1.0)
		}
	}
	if field := schema.Properties["reward_slots"]; field != nil {
		field.Maximum = bountySchemaPointer(100.0)
	}
	if field := schema.Properties["kind"]; field != nil {
		field.Enum = []any{bountycontract.General, bountycontract.OpenSource}
		schema.Properties["publisher_type"].Enum = []any{bountycontract.Individual, bountycontract.Company}
		schema.Properties["deadline_at"].Minimum = bountySchemaPointer(0.0)
		schema.Properties["deadline_at"].Maximum = bountySchemaPointer(float64(bountycontract.MaxDeadline))
		// Do not set a default kind: a legacy repository-only input denotes open_source.
		for _, kind := range []string{bountycontract.General, bountycontract.OpenSource} {
			condition := &jsonschema.Schema{
				If:   &jsonschema.Schema{Required: []string{"kind"}, Properties: map[string]*jsonschema.Schema{"kind": {Enum: []any{kind}}}},
				Then: &jsonschema.Schema{Properties: map[string]*jsonschema.Schema{"repository_url": {}}},
			}
			if kind == bountycontract.General {
				condition.Then.Properties["repository_url"].MaxLength = bountySchemaPointer(0)
			} else {
				condition.Then.Required = []string{"repository_url"}
				condition.Then.Properties["repository_url"].MinLength = bountySchemaPointer(1)
			}
			schema.AllOf = append(schema.AllOf, condition)
		}
	}
	if schema.Properties["delivery_url"] != nil {
		// The selected project's kind is checked by the model after authentication.
		// Discovery still tells clients that an empty evidence set is never sufficient.
		for _, field := range []string{"issue_url", "pull_request_url", "delivery_url", "submission_note"} {
			minimum := 1
			if field == "submission_note" {
				minimum = 20
			}
			schema.AnyOf = append(schema.AnyOf, &jsonschema.Schema{
				Required:   []string{field},
				Properties: map[string]*jsonschema.Schema{field: {MinLength: bountySchemaPointer(minimum)}},
			})
		}
	}
	return schema
}
