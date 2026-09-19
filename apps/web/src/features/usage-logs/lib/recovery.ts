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
import { redactAssistantMessageForRequest } from '@/features/assistant/assistant-message-safety'

import type { LogOtherData } from '../types'

// Error payloads can echo request bodies under arbitrary field names. Export
// only diagnostic metadata; never attempt to prove arbitrary prose is secret-free.
export function safeLogDiagnostic(text: string): string {
  if (text.length > 12000) return '[UNSTRUCTURED_ERROR_OMITTED]'
  const metadata = new Set([
    'request_id',
    'model',
    'status_code',
    'error_code',
    'code',
    'type',
    'status',
    'error_type',
  ])
  try {
    const parsed: unknown = JSON.parse(text)
    const collect = (value: unknown): Record<string, unknown> => {
      if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
      const result: Record<string, unknown> = {}
      for (const [key, item] of Object.entries(value)) {
        if (
          metadata.has(key) &&
          (typeof item === 'string' || typeof item === 'number')
        ) {
          result[key] =
            typeof item === 'string'
              ? redactAssistantMessageForRequest(
                  item
                    .slice(0, 200)
                    .replaceAll(/https?:\/\/[^\s<>"']+/gi, '[REDACTED_URL]')
                ).content
              : item
        } else if (key === 'error' && item && typeof item === 'object') {
          result.error = collect(item)
        }
      }
      return result
    }
    const result = collect(parsed)
    if (Object.keys(result).length) return JSON.stringify(result, null, 2)
  } catch {
    // Do not copy an unparseable body, including truncated or multiline JSON.
  }
  return '[UNSTRUCTURED_ERROR_OMITTED]'
}

export function logRecovery(other: LogOtherData | null) {
  switch (other?.error_code) {
    case 'insufficient_user_quota':
      return {
        message:
          'Account balance or plan quota is insufficient. Check your billing source.',
        action: 'Check balance and plan',
        target: 'plan' as const,
      }
    case 'pre_consume_token_quota_failed':
      return {
        message:
          'The API key quota could not be reserved. Check the key limit and availability.',
        action: 'Check API key',
        target: 'api-key' as const,
      }
  }
  switch (other?.status_code) {
    case 401:
    case 403:
      return {
        message:
          'Authentication or access was rejected. Check the API key and model permissions.',
        action: 'Check API key',
        target: 'api-key' as const,
      }
    case 404:
      return {
        message:
          'The endpoint or model was not found. Check the client configuration and model access.',
        action: 'Check client configuration',
        target: 'client-setup' as const,
      }
    case 429:
      return {
        message:
          'A request limit was reached. This record does not identify whether the limit belongs to your account or the upstream provider.',
        action: 'Diagnose request limit',
        target: 'usage' as const,
      }
    default:
      return {
        message:
          'Review the request details with the assistant before retrying.',
        action: 'Ask AI assistant',
        target: 'usage' as const,
      }
  }
}
