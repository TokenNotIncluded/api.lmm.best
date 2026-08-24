/*
Copyright (C) 2023-2026 QuantumNous

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

For commercial licensing, please contact support@quantumnous.com
*/
import * as z from 'zod'

import {
  ASSISTANT_REASONING_EFFORTS,
  ASSISTANT_SEARCH_PROVIDERS,
} from '../types'

export const assistantSettingsSchema = z.object({
  AssistantEnabled: z.boolean(),
  AssistantGroup: z.string().trim().min(1).max(64),
  AssistantModel: z.string().trim().min(1).max(128),
  AssistantReasoningEffort: z.enum(ASSISTANT_REASONING_EFFORTS),
  AssistantStreamEnabled: z.boolean(),
  AssistantTemperature: z.number().min(0).max(2),
  AssistantMaxTokens: z.number().int().min(64).max(8192),
  AssistantAgentLoopEnabled: z.boolean(),
  AssistantMaxSteps: z.number().int().min(1).max(12),
  AssistantTimeoutSeconds: z.number().int().min(5).max(120),
  AssistantCacheEnabled: z.boolean(),
  AssistantCacheTTLMinutes: z.number().int().min(0).max(10080),
  AssistantPersona: z.string().max(2000),
  AssistantSystemPrompt: z.string().max(8000),
  AssistantSearchProvider: z.enum(ASSISTANT_SEARCH_PROVIDERS),
  AssistantSearchURL: z.string().max(512),
  AssistantSearchAPIKey: z.string().max(512),
  AssistantSearchMCPTool: z.string().max(128),
  AssistantSkills: z.string().max(12000),
  AssistantSkillFiles: z.string().max(400000),
  AssistantReviewEnabled: z.boolean(),
  AssistantReviewWindowDays: z.number().int().min(1).max(90),
  AssistantReviewIntervalHours: z.number().int().min(1).max(168),
  AssistantReviewProbability: z.number().min(0).max(100),
  AssistantReviewGroup: z.string().trim().min(1).max(64),
  AssistantReviewModel: z.string().trim().min(1).max(128),
  AssistantReviewReasoningEffort: z.enum(ASSISTANT_REASONING_EFFORTS),
  AssistantReviewGroupPolicies: z.string().max(20000),
  AssistantRetentionEnabled: z.boolean(),
  AssistantActiveRetentionDays: z.number().int().min(7).max(3650),
  AssistantArchivedRetentionDays: z.number().int().min(1).max(3650),
  AssistantSecurityRetentionDays: z.number().int().min(30).max(3650),
  AssistantRetentionIntervalHours: z.number().int().min(1).max(168),
})

export type AssistantSettingsFormValues = z.infer<
  typeof assistantSettingsSchema
>
