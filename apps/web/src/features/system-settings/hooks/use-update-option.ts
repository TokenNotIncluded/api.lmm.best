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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { useMutation, useQueryClient } from '@tanstack/react-query'
import i18next from 'i18next'
import { toast } from 'sonner'

import { getServerErrorToastId } from '@/lib/server-error-message'

import { updateSystemOption, updateSystemOptions } from '../api'
import { updatePublicCreditUnitOption } from '../general/public-credit-unit'
import type { UpdateOptionRequest, UpdateOptionResponse } from '../types'
import { showOptionUpdateToast } from '../utils/option-update-toast'
import { getSettingsErrorMessage } from '../utils/settings-error-message'

// Configuration keys that require status refresh
const STATUS_RELATED_KEYS = new Set([
  'HeaderNavModules',
  'SidebarModulesAdmin',
  'Notice',
  'LogConsumeEnabled',
  'QuotaPerUnit',
  'PublicCreditsPerUSD',
  'USDExchangeRate',
  'DisplayInCurrencyEnabled',
  'DisplayTokenStatEnabled',
  'general_setting.quota_display_type',
  'general_setting.custom_currency_symbol',
  'general_setting.custom_currency_exchange_rate',
  'oidc.display_name',
  'OAuthRegisterEnabled',
  'ModerationEnabled',
  'ModerationPolicyScope',
  'ModerationSafetyIdentifierEnabled',
  'ModerationGroup',
  'ModerationModel',
  'ModerationGroupPolicies',
  'AssistantModerationEnabled',
  'AssistantModerationGroup',
  'AssistantModerationModel',
  'AssistantEnabled',
  'AssistantGroup',
  'AssistantModel',
  'AssistantReasoningEffort',
  'AssistantStreamEnabled',
  'AssistantTemperature',
  'AssistantMaxTokens',
  'AssistantNewUserGiftMaxCredits',
  'AssistantAgentLoopEnabled',
  'AssistantMaxSteps',
  'AssistantTimeoutSeconds',
  'AssistantCacheEnabled',
  'AssistantCacheTTLMinutes',
  'AssistantPersona',
  'AssistantSystemPrompt',
  'AssistantSearchProvider',
  'AssistantSearchURL',
  'AssistantSearchAPIKey',
  'AssistantSearchMCPTool',
  'AssistantToolPolicy',
  'AssistantSkills',
  'AssistantSkillFiles',
  'AssistantRetentionEnabled',
  'AssistantActiveRetentionDays',
  'AssistantArchivedRetentionDays',
  'AssistantSecurityRetentionDays',
  'AssistantRetentionIntervalHours',
])

async function invalidateOptionQueries(
  queryClient: ReturnType<typeof useQueryClient>,
  keys: Iterable<string>
) {
  const changedKeys = [...keys]
  const invalidate = (queryKey: string[]) =>
    queryClient.invalidateQueries({ queryKey }, { throwOnError: true })
  const refreshes = [invalidate(['system-options'])]
  if (changedKeys.some((key) => key.startsWith('Assistant'))) {
    refreshes.push(invalidate(['assistant-status']))
  }
  if (
    changedKeys.some(
      (key) =>
        key.startsWith('Moderation') || key.startsWith('AssistantModeration')
    )
  ) {
    refreshes.push(
      invalidate(['security-policy']),
      invalidate(['admin-security-policy']),
      invalidate(['moderation-routing-models']),
      invalidate(['admin-moderation-reviews']),
      invalidate(['admin-moderation-stats'])
    )
  }
  if (changedKeys.includes('AIDirectoryLinks')) {
    refreshes.push(invalidate(['ai-directory']))
  }
  if (changedKeys.includes('About')) {
    refreshes.push(invalidate(['about-content']))
  }
  if (changedKeys.includes('RSSFeeds')) {
    refreshes.push(invalidate(['rss']))
  }
  if (changedKeys.includes('AssistantNewUserGiftMaxCredits')) {
    refreshes.push(invalidate(['assistant-new-user-gift']))
  }
  if (changedKeys.includes('AssistantPreConversationPresets')) {
    refreshes.push(invalidate(['assistant-pre-conversation-presets']))
  }
  if (changedKeys.some((key) => STATUS_RELATED_KEYS.has(key))) {
    refreshes.push(invalidate(['status']))
    try {
      window.localStorage.removeItem('status')
    } catch {
      /* empty */
    }
  }
  await Promise.all(refreshes)
}

let nextSaveToast = 0

function acceptSettingsSave(response: UpdateOptionResponse) {
  if (!response.success) {
    throw Object.assign(
      new Error(response.message || i18next.t('Failed to update setting')),
      {
        response: { data: response },
      }
    )
  }
  return response
}

function refreshAcknowledgedOptions(
  queryClient: ReturnType<typeof useQueryClient>,
  response: UpdateOptionResponse,
  keys: Iterable<string>
) {
  const id = `settings-save-${++nextSaveToast}`
  showOptionUpdateToast(response, i18next.t('Setting updated successfully'), {
    id,
  })
  // Refreshing has its own outcome. Never reject an acknowledged write or
  // make a caller repeat it because a subsequent read is unavailable.
  void invalidateOptionQueries(queryClient, keys).catch((error: unknown) => {
    toast.warning(
      i18next.t('Settings saved, but refreshing failed: {{reason}}', {
        reason: getSettingsErrorMessage(
          error,
          i18next.t('Failed to load settings')
        ),
      }),
      { id, description: response.warnings?.join('\n') }
    )
  })
}

function reportSaveFailure(error: unknown) {
  toast.error(
    getSettingsErrorMessage(error, i18next.t('Failed to update setting')),
    {
      id: getServerErrorToastId(error) ?? undefined,
    }
  )
}

export function useUpdateOption() {
  const queryClient = useQueryClient()
  return useMutation({
    retry: false,
    mutationFn: async (request: UpdateOptionRequest) =>
      acceptSettingsSave(
        request.key === 'PublicCreditsPerUSD'
          ? await updatePublicCreditUnitOption(request)
          : await updateSystemOption(request, { silent: true })
      ),
    onSuccess: (data, variables) =>
      refreshAcknowledgedOptions(queryClient, data, [variables.key]),
    onError: reportSaveFailure,
  })
}

export function useUpdateOptions() {
  const queryClient = useQueryClient()
  return useMutation({
    retry: false,
    mutationFn: async (values: Record<string, string>) =>
      acceptSettingsSave(await updateSystemOptions(values, { silent: true })),
    onSuccess: (data, variables) =>
      refreshAcknowledgedOptions(queryClient, data, Object.keys(variables)),
    onError: reportSaveFailure,
  })
}
