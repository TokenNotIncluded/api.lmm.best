/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { z } from 'zod'

const byteLength = (text: string) => new TextEncoder().encode(text).length
const hasControl = (text: string, includeSpace = false) =>
  Array.from(text).some((character) => {
    const code = character.codePointAt(0) ?? 0
    return code <= (includeSpace ? 32 : 31) || code === 127
  })
const bytes = (maximum: number) =>
  z.string().refine((value) => byteLength(value) <= maximum)
const identifier = z.string().regex(/^[A-Za-z0-9_.-]{1,64}$/)
const publicModelsSchema = z
  .array(bytes(256).refine((value) => value.length > 0 && !hasControl(value)))
  .min(1)
  .max(200)
  .refine((models) => new Set(models).size === models.length)
const revision = z.number().int().nonnegative().safe()
const userID = z.number().int().positive().safe()
export const protocolSchema = z.enum(['openai_chat', 'openai_responses'])
export type HoneypotProtocol = z.infer<typeof protocolSchema>
export const upstreamProtocolSchema = z.enum([
  'openai_chat',
  'openai_responses',
  'anthropic_messages',
  'gemini',
])
export const modeSchema = z.enum(['fixed_text', 'custom_upstream'])

export function isNumericLoopbackURL(value: string): boolean {
  if (
    byteLength(value) > 2048 ||
    /[\\?#@]/.test(value) ||
    hasControl(value, true)
  ) {
    return false
  }
  if (
    !/^http:\/\/(?:127\.\d{1,3}\.\d{1,3}\.\d{1,3}|\[::1\])(?::\d{1,5})?(?:\/.*)?$/.test(
      value
    )
  ) {
    return false
  }
  try {
    const url = new URL(value)
    return (
      url.protocol === 'http:' &&
      (url.hostname === '[::1]' ||
        (/^127\.\d+\.\d+\.\d+$/.test(url.hostname) &&
          url.hostname.split('.').every((part) => Number(part) <= 255)))
    )
  } catch {
    return false
  }
}

export const limitsSchema = z
  .object({
    requests_per_minute_per_user: z.number().int().min(1).max(100000),
    requests_per_minute_global: z.number().int().min(1).max(100000),
    max_concurrent_per_user: z.number().int().min(1).max(256),
    max_concurrent_global: z.number().int().min(1).max(256),
    max_input_bytes: z.number().int().min(1).max(1048576),
    max_output_bytes: z.number().int().min(1).max(1048576),
    max_output_tokens: z.number().int().min(1).max(65536),
    output_bytes_per_second: z.number().int().min(0).max(1048576),
    total_timeout_seconds: z.number().int().min(1).max(240),
  })
  .strict()
  .superRefine((value, context) => {
    if (value.requests_per_minute_per_user > value.requests_per_minute_global) {
      context.addIssue({
        code: 'custom',
        path: ['requests_per_minute_per_user'],
        message: 'Per-user limit exceeds global limit',
      })
    }
    if (value.max_concurrent_per_user > value.max_concurrent_global) {
      context.addIssue({
        code: 'custom',
        path: ['max_concurrent_per_user'],
        message: 'Per-user concurrency exceeds global concurrency',
      })
    }
  })
export type HoneypotLimits = z.infer<typeof limitsSchema>

export const upstreamSchema = z
  .object({
    base_url: z.string().refine(isNumericLoopbackURL),
    upstream_protocol: upstreamProtocolSchema,
    model: bytes(256).refine((value) => byteLength(value) > 0),
    credential_reference: z.string().min(1),
  })
  .strict()
export type HoneypotUpstream = z.infer<typeof upstreamSchema>

export const policySchema = z
  .discriminatedUnion('mode', [
    z
      .object({
        id: identifier,
        name: z.string().min(1).max(128),
        mode: z.literal('fixed_text'),
        public_models: publicModelsSchema,
        fixed_text: bytes(16384),
        clients: z.object({}).strict(),
        limits: limitsSchema,
      })
      .strict(),
    z
      .object({
        id: identifier,
        name: z.string().min(1).max(128),
        mode: z.literal('custom_upstream'),
        public_models: publicModelsSchema,
        clients: z
          .object({
            openai_chat: upstreamSchema,
            openai_responses: upstreamSchema,
          })
          .strict(),
        limits: limitsSchema,
      })
      .strict(),
  ])
  .superRefine((value, context) => {
    if (
      value.mode === 'fixed_text' &&
      byteLength(value.fixed_text) > value.limits.max_output_bytes
    ) {
      context.addIssue({
        code: 'custom',
        path: ['fixed_text'],
        message: 'Fixed text exceeds output limit',
      })
    }
  })
export type HoneypotPolicy = z.infer<typeof policySchema>

export const configSchema = z
  .object({ version: z.literal(1), policies: z.array(policySchema).max(32) })
  .strict()
  .superRefine((value, context) => {
    const ids = value.policies.map((policy) => policy.id)
    if (new Set(ids).size !== ids.length) {
      context.addIssue({ code: 'custom', message: 'Duplicate policy IDs' })
    }
    if (byteLength(JSON.stringify(value)) > 65536) {
      context.addIssue({
        code: 'custom',
        message: 'Configuration is too large',
      })
    }
  })
export type HoneypotConfig = z.infer<typeof configSchema>

export const policiesSchema = z.object({
  version: z.literal(1),
  revision,
  config: configSchema,
  capabilities: z.object({
    client_protocols: z.array(protocolSchema),
    upstream_protocols: z.array(upstreamProtocolSchema),
    modes: z.array(modeSchema),
    numeric_loopback_only: z.literal(true),
    credential_references: z.array(z.string().min(1)),
  }),
})
export type HoneypotPolicies = z.infer<typeof policiesSchema>

export const userViewSchema = z
  .object({
    user_id: userID,
    username: z.string(),
    email: z.string(),
    active: z.boolean(),
    platform_role: z.enum(['user', 'admin', 'superadmin']),
    revision,
    marked: z.boolean(),
    policy_id: identifier.nullable(),
  })
  .superRefine((user, context) => {
    if (user.marked !== (user.policy_id !== null)) {
      context.addIssue({ code: 'custom', message: 'Inconsistent mark state' })
    }
  })
export type HoneypotUser = z.infer<typeof userViewSchema>
export const userListSchema = z.object({
  version: z.literal(1),
  users: z.array(userViewSchema),
  next_after: userID.nullable(),
})
export const actionSchema = z.enum(['mark', 'unmark', 'convert_disabled'])
export type HoneypotAction = z.infer<typeof actionSchema>
export const operationIDSchema = z.string().regex(/^[A-Za-z0-9_.:-]{8,128}$/)
const reasonSchema = z.string().min(1).max(1000)

export const previewRequestSchema = z
  .object({
    operation_id: operationIDSchema,
    reason: reasonSchema,
    action: actionSchema,
    policy_id: identifier.optional(),
    expected_policy_revision: revision,
    targets: z
      .array(
        z.object({ user_id: userID, expected_revision: revision }).strict()
      )
      .min(1)
      .max(200),
  })
  .strict()
  .superRefine((value, context) => {
    if (
      new Set(value.targets.map((target) => target.user_id)).size !==
      value.targets.length
    ) {
      context.addIssue({ code: 'custom', message: 'Duplicate users' })
    }
    if ((value.action === 'unmark') !== (value.policy_id === undefined)) {
      context.addIssue({
        code: 'custom',
        path: ['policy_id'],
        message: 'Policy does not match action',
      })
    }
  })
export type HoneypotPreviewRequest = z.infer<typeof previewRequestSchema>

export const previewSchema = z.object({
  version: z.literal(1),
  preview_id: z.string().min(1),
  preview_hash: z.string().regex(/^[a-f0-9]{64}$/),
  expires_at: z.number().int().positive().safe(),
  operation_id: operationIDSchema,
  action: actionSchema,
  policy_id: identifier.nullable(),
  policy_revision: revision,
  targets: z
    .array(
      z.object({
        user_id: userID,
        username: z.string(),
        active: z.boolean(),
        expected_revision: revision,
        before: z.object({
          marked: z.boolean(),
          policy_id: identifier.nullable(),
        }),
        after: z.object({
          marked: z.boolean(),
          policy_id: identifier.nullable(),
          active: z.boolean(),
        }),
      })
    )
    .min(1)
    .max(200),
})
export type HoneypotPreview = z.infer<typeof previewSchema>
const resultBase = {
  version: z.literal(1),
  status: z.literal('applied'),
  operation_id: operationIDSchema,
  policy_revision: revision,
  users: z.array(userViewSchema),
}
export const resultSchema = z.discriminatedUnion('kind', [
  z.object({ ...resultBase, kind: z.literal('users') }),
  z.object({
    ...resultBase,
    kind: z.literal('policies'),
    applied_config_hash: z.string().regex(/^[a-f0-9]{64}$/),
  }),
])
export type HoneypotResult = z.infer<typeof resultSchema>

function canonicalJSON(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(',')}]`
  if (typeof value === 'object' && value !== null) {
    const object = value as Record<string, unknown>
    return `{${Object.keys(object)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${canonicalJSON(object[key])}`)
      .join(',')}}`
  }
  const serialized = JSON.stringify(value)
  if (serialized === undefined) throw new Error('Invalid configuration value')
  return serialized
}

export async function hashHoneypotConfig(
  config: HoneypotConfig
): Promise<string> {
  const bytes = new TextEncoder().encode(
    canonicalJSON(configSchema.parse(config))
  )
  const digest = await crypto.subtle.digest('SHA-256', bytes)
  return Array.from(new Uint8Array(digest), (byte) =>
    byte.toString(16).padStart(2, '0')
  ).join('')
}

export const DEFAULT_LIMITS: HoneypotLimits = {
  requests_per_minute_per_user: 10,
  requests_per_minute_global: 100,
  max_concurrent_per_user: 1,
  max_concurrent_global: 8,
  max_input_bytes: 65536,
  max_output_bytes: 16384,
  max_output_tokens: 1024,
  output_bytes_per_second: 0,
  total_timeout_seconds: 30,
}

export function validatePolicyCapabilities(
  config: HoneypotConfig,
  policies: HoneypotPolicies
): boolean {
  return config.policies.every(
    (policy) =>
      policies.capabilities.modes.includes(policy.mode) &&
      (policy.mode === 'fixed_text' ||
        Object.entries(policy.clients).every(
          ([protocol, upstream]) =>
            policies.capabilities.client_protocols.includes(
              protocol as HoneypotProtocol
            ) &&
            policies.capabilities.upstream_protocols.includes(
              upstream.upstream_protocol
            ) &&
            policies.capabilities.credential_references.includes(
              upstream.credential_reference
            )
        ))
  )
}

export function previewMatchesRequest(
  preview: HoneypotPreview,
  request: HoneypotPreviewRequest
): boolean {
  const targets = new Map(
    request.targets.map((target) => [target.user_id, target.expected_revision])
  )
  return (
    preview.operation_id === request.operation_id &&
    preview.action === request.action &&
    preview.policy_id === (request.policy_id ?? null) &&
    preview.policy_revision === request.expected_policy_revision &&
    preview.targets.length === targets.size &&
    new Set(preview.targets.map((target) => target.user_id)).size ===
      targets.size &&
    preview.targets.every(
      (target) =>
        targets.get(target.user_id) === target.expected_revision &&
        target.after.marked === (request.action !== 'unmark') &&
        target.after.policy_id === (request.policy_id ?? null) &&
        target.after.active ===
          (request.action === 'convert_disabled' ? true : target.active)
    )
  )
}
