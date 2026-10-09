/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useMarketTranslation as useTranslation } from "./provider-i18n";

import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";

import type { ProviderPreset } from "./api";

export function ProviderPresetFields({
  presets,
  selected,
  multiplier,
  disabled,
  onSelect,
  onMultiplier,
}: {
  presets: ProviderPreset[];
  selected: string;
  multiplier: string;
  disabled: boolean;
  onSelect: (preset: ProviderPreset | undefined) => void;
  onMultiplier: (value: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <>
      <Field>
        <FieldLabel htmlFor="market-preset">{t("Provider preset")}</FieldLabel>
        <select
          id="market-preset"
          className="border-input bg-background focus-visible:ring-ring min-h-11 w-full rounded-md border px-3 text-base outline-none focus-visible:ring-2 sm:text-sm"
          value={selected}
          disabled={disabled}
          onChange={(event) =>
            onSelect(presets.find((preset) => preset.id === event.target.value))
          }
        >
          <option value="">{t("Custom MCP service")}</option>
          {presets.map((preset) => (
            <option key={preset.id} value={preset.id}>
              {preset.name}
            </option>
          ))}
        </select>
        <FieldDescription>
          {t(
            "Choose a provider, enter its API key, then review the tools and spending ceiling. Your customers sign in to LMM separately; they do not receive this key.",
          )}
        </FieldDescription>
      </Field>
      {selected && (
        <Field>
          <FieldLabel htmlFor="market-multiplier">
            {t("Price multiplier")}
          </FieldLabel>
          <Input
            id="market-multiplier"
            type="number"
            inputMode="decimal"
            min="1"
            max="100"
            step="0.01"
            required
            value={multiplier}
            disabled={disabled}
            onChange={(event) => onMultiplier(event.target.value)}
          />
          <FieldDescription>
            {t(
              "Sale price = upstream USD quote × multiplier. Calls are blocked when the quote is missing, exceeds a spending limit, or the platform fee would make the sale fall below cost.",
            )}
          </FieldDescription>
        </Field>
      )}
    </>
  );
}
