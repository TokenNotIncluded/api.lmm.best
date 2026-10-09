/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { z } from 'zod'

export const OVERVIEW_LANGUAGES = [
  'en',
  'zh',
  'zh-TW',
  'fr',
  'ja',
  'ru',
  'vi',
] as const
const boundedText = (max: number, min = 1) =>
  z
    .string()
    .refine(
      (text) =>
        [...text].length >= min &&
        [...text].length <= max &&
        !text.includes('\0')
    )
const positiveID = z.number().int().positive().max(Number.MAX_SAFE_INTEGER)
const visibility = z.enum(['user', 'admin'])
const base = {
  type: z.literal('workspace_action'),
  confirmation_token: z.string().min(20).max(256),
  requires_confirmation: z.literal(true),
}
const schema = z.discriminatedUnion('tool', [
  z.object({
    ...base,
    tool: z.literal('set_overview_greeting'),
    preview: z.object({
      language: z.enum(OVERVIEW_LANGUAGES),
      template: boundedText(512, 0),
      revision: z.number().int().nonnegative(),
    }),
  }),
  z.object({
    ...base,
    tool: z.literal('create_site_issue'),
    preview: z.object({
      kind: z.enum(['bug', 'security', 'experience', 'feature']),
      title: boundedText(128),
      body: boundedText(4000),
      visibility,
    }),
  }),
  z.object({
    ...base,
    tool: z.literal('update_site_issue'),
    preview: z.object({
      issue_id: positiveID,
      revision: positiveID,
      status: z.enum([
        'open',
        'triaged',
        'in_progress',
        'resolved',
        'declined',
      ]),
      visibility,
      note: boundedText(1500, 0),
    }),
  }),
  z.object({
    ...base,
    tool: z.literal('send_invitation'),
    preview: z.object({ email: z.string().email().max(254) }),
  }),
  z.object({
    ...base,
    tool: z.literal('connect_market_tool'),
    preview: z.object({
      service_id: boundedText(128),
      tool_id: boundedText(128),
      version_id: boundedText(128),
      service_name: boundedText(200),
      tool_name: boundedText(200),
      description: boundedText(4000, 0),
      permissions: z.array(boundedText(300)).max(50),
      price_quota: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
      billing_mode: z.enum(['', 'input_tokens', 'metered']),
      input_token_price_quota: z.number().int().nonnegative().optional(),
      max_input_tokens: z.number().int().nonnegative().optional(),
      billing_rules: z
        .array(
          z.object({
            metric: boundedText(64),
            rate_quota: z.number().int().nonnegative(),
            max_quantity: z.number().int().positive(),
          })
        )
        .max(16)
        .nullable()
        .optional(),
    }),
  }),
])
export type AssistantWorkspaceAction = z.infer<typeof schema>
export function parseAssistantWorkspaceAction(
  value: unknown
): AssistantWorkspaceAction | undefined {
  const parsed = schema.safeParse(value)
  return parsed.success ? parsed.data : undefined
}
export function workspaceActionTitle(
  tool: AssistantWorkspaceAction['tool']
): string {
  return {
    set_overview_greeting: 'Edit overview greeting',
    create_site_issue: 'Create a site issue',
    update_site_issue: 'Update a site issue',
    send_invitation: 'Send an invitation',
    connect_market_tool: 'Authorize a market tool',
  }[tool]
}
