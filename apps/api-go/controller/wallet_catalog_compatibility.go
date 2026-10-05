package controller

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

const walletMCPTopupDescription = "Open the official wallet with a bounded whole legacy batch amount prefilled. The wallet converts this compatibility amount to credits. The user chooses a payment method and confirms there; this is not a payment-provider checkout, successful payment or balance credit. The MCP call is free."

type walletCatalogDescriptionCompatibility struct {
	tool, property, legacy, current string
}

// These three text-only corrections describe the existing LEGACY batch top-up
// and raw-quota transfer inputs. Their types, bounds, permissions and behavior
// have not changed. Retain the exact pre-currency immutable catalog metadata so
// its grants and the previous binary's deployment verifier remain valid. This
// profile does not alter hashes, IDs, schemas or authority outside these texts.
// Public MCP and market presentation use the accurate current descriptions.
// A future contract change must remove this profile and create a new version;
// unexpected descriptions/tools fail closed instead of being overwritten.
var walletLegacyCatalogDescriptions = [...]walletCatalogDescriptionCompatibility{
	{"wallet.topup_link", "", "Open the official wallet with a bounded whole platform-credit amount prefilled. The user chooses a payment method and confirms there; this is not a payment-provider checkout, successful payment or balance credit. The MCP call is free.", walletMCPTopupDescription},
	{"wallet.topup_link", "amount", "Positive whole platform-credit amount, at most 1000000. This only prefills the official wallet; the user must choose and confirm payment there.", "Positive whole legacy batch amount, at most 1000000. This is not raw wallet credits or USD. This only prefills the official wallet; the user must choose and confirm payment there."},
	{"wallet.transfer.create", "quota", "Positive integer wallet quota units to hold for the recipient. Inspect wallet.balance for quota_per_platform_credit. The exact amount requires user confirmation.", "Positive integer wallet credits to hold for the recipient. One credit is one raw quota unit. The exact amount requires user confirmation."},
}

func walletCatalogInputDescription(raw json.RawMessage, property, from, to string) (json.RawMessage, error) {
	// RawMessage preserves all other schema values, including exact numbers.
	var schema, properties, field map[string]json.RawMessage
	if json.Unmarshal(raw, &schema) != nil || schema == nil ||
		json.Unmarshal(schema["properties"], &properties) != nil || properties == nil ||
		json.Unmarshal(properties[property], &field) != nil || field == nil {
		return nil, model.ErrToolMarketConflict
	}
	var description string
	if json.Unmarshal(field["description"], &description) != nil || description != from {
		return nil, model.ErrToolMarketConflict
	}
	field["description"], _ = json.Marshal(to)
	properties[property], _ = json.Marshal(field)
	schema["properties"], _ = json.Marshal(properties)
	return json.Marshal(schema)
}

func walletCatalogToolDescriptions(tool model.ToolMarketToolInput, legacy bool) (model.ToolMarketToolInput, error) {
	tool.InputSchema = append(json.RawMessage(nil), tool.InputSchema...)
	tool.OutputSchema = append(json.RawMessage(nil), tool.OutputSchema...)
	tool.Permissions = append([]string(nil), tool.Permissions...)
	for _, correction := range walletLegacyCatalogDescriptions {
		if tool.Name != correction.tool {
			continue
		}
		from, to := correction.current, correction.legacy
		if !legacy {
			from, to = to, from
		}
		if correction.property == "" {
			if tool.Description != from {
				return tool, model.ErrToolMarketConflict
			}
			tool.Description = to
			continue
		}
		var err error
		tool.InputSchema, err = walletCatalogInputDescription(tool.InputSchema, correction.property, from, to)
		if err != nil {
			return tool, err
		}
	}
	return tool, nil
}

func walletLegacyCatalogCompatibility(input model.ToolMarketBuiltinServiceInput) (model.ToolMarketBuiltinServiceInput, error) {
	if input.Key != "wallet" {
		return input, nil
	}
	expected := map[string]bool{"wallet.balance": true, "wallet.topup_link": true, "wallet.transfers.list": true, "wallet.transfer.create": true, "wallet.transfer.cancel": true}
	if len(input.Tools) != len(expected) {
		return input, model.ErrToolMarketConflict
	}
	input.Tools = append([]model.ToolMarketToolInput(nil), input.Tools...)
	for i, tool := range input.Tools {
		if !expected[tool.Name] {
			return input, model.ErrToolMarketConflict
		}
		delete(expected, tool.Name)
		var err error
		input.Tools[i], err = walletCatalogToolDescriptions(tool, true)
		if err != nil {
			return input, err
		}
	}
	return input, nil
}

func walletCurrentToolPresentation(tool model.ToolMarketToolVersion) (model.ToolMarketToolVersion, error) {
	presentation, err := walletCatalogToolDescriptions(model.ToolMarketToolInput{Name: tool.Name, Description: tool.Description, InputSchema: json.RawMessage(tool.InputSchema)}, false)
	if err != nil {
		return tool, err
	}
	tool.Description, tool.InputSchema = presentation.Description, string(presentation.InputSchema)
	tool.BillingRules = slices.Clone(tool.BillingRules)
	tool.AvailableMeteringMetrics = slices.Clone(tool.AvailableMeteringMetrics)
	return tool, nil
}

// Presentation is a detached copy of the exact authorized builtin version.
// Stored immutable schema/digest/version, pricing and grants are never updated.
func walletCurrentCatalogPresentation(ctx context.Context, detail *model.ToolMarketDetail) (*model.ToolMarketDetail, error) {
	if detail == nil || detail.Service.ID != model.ToolMarketBuiltinServiceID("wallet") || detail.Service.OwnerID != 0 || detail.Version.ExecutionType != "builtin" {
		return detail, nil
	}
	if detail.Version.ServiceID != detail.Service.ID || detail.Version.Endpoint != "" || detail.Service.LiveVersionID != detail.Version.ID ||
		marketBuiltinVersionCurrent(ctx, "wallet", detail.Version.ID) != nil {
		return nil, model.ErrToolMarketConflict
	}
	presentation := *detail
	presentation.AllowedUsers = slices.Clone(detail.AllowedUsers)
	presentation.Tools = append([]model.ToolMarketToolVersion(nil), detail.Tools...)
	for i, tool := range presentation.Tools {
		if tool.VersionID != detail.Version.ID {
			return nil, model.ErrToolMarketConflict
		}
		var err error
		presentation.Tools[i], err = walletCurrentToolPresentation(tool)
		if err != nil {
			return nil, err
		}
	}
	return &presentation, nil
}
