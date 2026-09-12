/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import type { AssistantPreConversationPreset } from './api'

// Bump both the query key and HTTP URL when reviewed starter copy changes.
export const ASSISTANT_PROMPT_PRESET_COPY_VERSION = 'natural-v2'

const requiredPresetKeys = {
  ai_recommendation: 'Help me write an L1 recommendation.',
  getting_started: 'Where should I start?',
  new_user_gift: 'How do I get the new-user gift?',
  weekly_discount: 'Any top-up discounts this week?',
} as const

const fallbackPresets: AssistantPreConversationPreset[] = Object.entries(
  requiredPresetKeys
).map(([id, prompt]) => ({ id, prompt, label: prompt }))

export function localizeAssistantPreConversationPresets(
  presets: AssistantPreConversationPreset[] | undefined,
  t: (key: string) => string
): AssistantPreConversationPreset[] {
  return (presets ?? fallbackPresets).map((preset) => {
    // Use stable IDs rather than legacy Chinese labels/prompts. This also fixes
    // old cached responses and updates immediately when the UI language changes.
    if (!Object.hasOwn(requiredPresetKeys, preset.id)) return preset
    const key = requiredPresetKeys[preset.id as keyof typeof requiredPresetKeys]
    const prompt = t(key)
    return { ...preset, label: prompt, prompt }
  })
}
