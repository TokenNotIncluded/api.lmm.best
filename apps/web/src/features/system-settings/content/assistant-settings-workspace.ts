/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type { AssistantSettingsFormValues } from './assistant-settings-schema'
import { mergeAssistantToolPolicy } from './assistant-tool-policy'

export const ASSISTANT_SETTINGS_GROUPS = [
  { id: 'model', label: 'Assistant connection' },
  { id: 'conversation', label: 'Assistant conversation' },
  { id: 'tools', label: 'Assistant tools' },
  { id: 'runtime', label: 'Assistant runtime' },
  { id: 'moderation', label: 'Content safety review' },
  { id: 'review', label: 'Assistant review' },
  { id: 'retention', label: 'Assistant data' },
] as const
export type AssistantSettingsGroup =
  (typeof ASSISTANT_SETTINGS_GROUPS)[number]['id']

export function getAssistantSettingsGroup(
  field: string
): AssistantSettingsGroup {
  if (/Moderation/.test(field)) return 'moderation'
  if (/Retention/.test(field)) return 'retention'
  if (/Gift/.test(field)) return 'tools'
  if (/Review|Registration|Approval/.test(field)) return 'review'
  if (/Search|ToolPolicy|Skills|SkillFiles/.test(field)) return 'tools'
  if (/Persona|SystemPrompt|PreConversation/.test(field)) return 'conversation'
  if (/AgentLoop|MaxSteps|Timeout|Cache/.test(field)) return 'runtime'
  return 'model'
}

/** Refresh untouched fields but retain local edits; the server remains the save baseline. */
export function rebaseAssistantDraft(
  previous: AssistantSettingsFormValues,
  draft: AssistantSettingsFormValues,
  incoming: AssistantSettingsFormValues
): AssistantSettingsFormValues {
  return Object.fromEntries(
    Object.entries(incoming).map(([name, value]) => {
      const key = name as keyof AssistantSettingsFormValues
      if (
        key === 'AssistantToolPolicy' &&
        typeof previous[key] === 'string' &&
        typeof draft[key] === 'string' &&
        typeof value === 'string'
      ) {
        return [
          name,
          mergeAssistantToolPolicy(previous[key], draft[key], value),
        ]
      }
      return [name, draft[key] !== previous[key] ? draft[key] : value]
    })
  ) as AssistantSettingsFormValues
}
