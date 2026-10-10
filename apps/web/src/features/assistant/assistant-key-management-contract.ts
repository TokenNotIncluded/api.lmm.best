/*
Copyright (C) 2026 LIghtJUNction
*/
import { z } from 'zod'

const safeInteger = z.number().int().refine(Number.isSafeInteger)
const metadataString = z.string().max(512)
const confirmationTokenSchema = z
  .string()
  .min(1)
  .max(512)
  .refine((value) => value === value.trim())
const tokenStatusSchema = z.union([
  z.literal(1),
  z.literal(2),
  z.literal(3),
  z.literal(4),
])

const keyActionSchema = z.strictObject({
  type: z.literal('api_key_action'),
  action: z.enum(['delete', 'disable']),
  confirmation_token: confirmationTokenSchema,
  requires_confirmation: z.literal(true),
  expires_in_seconds: safeInteger.positive(),
  token: z.strictObject({
    id: safeInteger.positive(),
    name: metadataString,
    status: tokenStatusSchema,
    group: metadataString,
    created_time: safeInteger.nonnegative(),
    accessed_time: safeInteger.nonnegative(),
    expired_time: safeInteger.min(-1),
  }),
  two_factor_required: z.boolean(),
  ui_path: z.literal('/keys'),
})

const receiptSchema = z.strictObject({
  id: safeInteger.positive(),
  name: metadataString,
  group: metadataString,
  action: z.enum(['delete', 'disable']),
  status: tokenStatusSchema.optional(),
})

export type AssistantKeyManagementAction = z.infer<typeof keyActionSchema>
export type AssistantKeyManagementReceipt = z.infer<typeof receiptSchema>

/** Validate the original payload before any fields can be silently dropped. */
export function parseAssistantKeyManagementAction(
  value: unknown
): AssistantKeyManagementAction | undefined {
  const result = keyActionSchema.safeParse(value)
  return result.success ? result.data : undefined
}

export function parseAssistantKeyManagementReceipt(
  value: unknown,
  expected: AssistantKeyManagementAction
): AssistantKeyManagementReceipt | undefined {
  const result = receiptSchema.safeParse(value)
  if (!result.success) return undefined
  const receipt = result.data
  if (
    receipt.id !== expected.token.id ||
    receipt.name !== expected.token.name ||
    receipt.group !== expected.token.group ||
    receipt.action !== expected.action ||
    (receipt.action === 'disable' && receipt.status !== 2)
  ) {
    return undefined
  }
  return receipt
}
