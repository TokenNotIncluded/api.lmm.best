/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { CanceledError } from 'axios'

import { useAuthStore } from '@/stores/auth-store'

import { getAssistantSystemOptions } from '../api'
import type { AssistantSettingsAuthScope } from '../types'
import {
  DEFAULT_ASSISTANT_TOOL_POLICY,
  parseAssistantToolPolicy,
} from './assistant-tool-policy'

export const ASSISTANT_TOOL_POLICY_CONFLICT_MESSAGE =
  'Tool settings changed in another session. Refresh and review your draft before saving.'
export const ASSISTANT_TOOL_POLICY_REFRESH_ERROR =
  'Unable to refresh tool settings. Your draft was kept. Try saving again.'
export const ASSISTANT_TOOL_POLICY_MERGE_NOTICE =
  'New tool settings were merged into your draft. Concurrent changes keep disabled choices; review each switch before saving.'

export function isAssistantToolPolicyConflict(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const response = (error as { response?: unknown }).response
  if (!response || typeof response !== 'object') return false
  const data = (response as { data?: unknown }).data
  return (
    data !== null &&
    typeof data === 'object' &&
    (data as { code?: unknown }).code === 'ASSISTANT_TOOL_POLICY_CONFLICT'
  )
}

export function captureAssistantSettingsAuthScope(): AssistantSettingsAuthScope {
  const auth = useAuthStore.getState().auth
  return { userId: auth.user?.id, sessionId: auth.session?.sid }
}

export async function getLatestAssistantToolPolicy(
  authScope: AssistantSettingsAuthScope
): Promise<string> {
  const response = await getAssistantSystemOptions(authScope)
  const current = captureAssistantSettingsAuthScope()
  if (
    current.userId !== authScope.userId ||
    current.sessionId !== authScope.sessionId
  ) {
    throw new CanceledError(
      'Authentication changed while tool settings were being read'
    )
  }
  const option = response.data.find(
    (item) => item.key === 'AssistantToolPolicy'
  )
  const value = option?.value ?? DEFAULT_ASSISTANT_TOOL_POLICY
  if (typeof value !== 'string' || parseAssistantToolPolicy(value) === null) {
    throw new Error(ASSISTANT_TOOL_POLICY_REFRESH_ERROR)
  }
  return value
}
