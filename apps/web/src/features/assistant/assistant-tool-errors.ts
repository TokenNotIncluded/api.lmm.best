/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
export const assistantToolErrorMessages = {
  missing_math_expression: 'A math expression is required.',
  invalid_math_expression: 'The math expression could not be evaluated.',
  tool_disabled: 'This tool is disabled in assistant settings.',
  tool_level_denied: 'Your account level does not allow this tool.',
  tool_policy_unavailable:
    'Tool settings could not be loaded. Try again later.',
  operation_dispatch_unavailable:
    'The management tool connection is unavailable. Update the server and try again.',
  invalid_arguments:
    'The tool arguments are invalid. Correct them before retrying.',
  response_limit_exceeded:
    'The result is too large. Request a smaller page or an exact item.',
  admin_access_denied:
    'A current browser session with the required administrator permission is needed.',
  session_required: 'Your login session is unavailable. Sign in again.',
  policy_changed:
    'This policy changed after it was read. Read it again before editing.',
  policy_unavailable: 'The site policy could not be loaded. Try again later.',
  not_found:
    'The requested item was not found or is not available to this tool.',
  tool_failed: 'The tool failed. No success was confirmed.',
} as const

export type AssistantToolErrorCode = keyof typeof assistantToolErrorMessages
export function parseAssistantToolErrorCode(
  value: unknown
): AssistantToolErrorCode | undefined {
  return typeof value === 'string' &&
    Object.hasOwn(assistantToolErrorMessages, value)
    ? (value as AssistantToolErrorCode)
    : undefined
}
