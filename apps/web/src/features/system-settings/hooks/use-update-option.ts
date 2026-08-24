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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import i18next from 'i18next'
import { toast } from 'sonner'

import { updateSystemOption, updateSystemOptions } from '../api'
import type { UpdateOptionRequest } from '../types'

// Configuration keys that require status refresh
const STATUS_RELATED_KEYS = new Set([
  'HeaderNavModules',
  'SidebarModulesAdmin',
  'Notice',
  'LogConsumeEnabled',
  'QuotaPerUnit',
  'USDExchangeRate',
  'DisplayInCurrencyEnabled',
  'DisplayTokenStatEnabled',
  'general_setting.quota_display_type',
  'general_setting.custom_currency_symbol',
  'general_setting.custom_currency_exchange_rate',
  'oidc.display_name',
  'OAuthRegisterEnabled',
  'AssistantEnabled',
  'AssistantGroup',
  'AssistantModel',
  'AssistantReasoningEffort',
  'AssistantStreamEnabled',
  'AssistantTemperature',
  'AssistantMaxTokens',
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
  'AssistantSkills',
  'AssistantSkillFiles',
  'AssistantReviewEnabled',
  'AssistantReviewWindowDays',
  'AssistantReviewIntervalHours',
  'AssistantReviewProbability',
  'AssistantReviewGroup',
  'AssistantReviewModel',
  'AssistantReviewReasoningEffort',
  'AssistantReviewGroupPolicies',
  'AssistantRetentionEnabled',
  'AssistantActiveRetentionDays',
  'AssistantArchivedRetentionDays',
  'AssistantSecurityRetentionDays',
  'AssistantRetentionIntervalHours',
])

function invalidateOptionQueries(
  queryClient: ReturnType<typeof useQueryClient>,
  keys: Iterable<string>
) {
  queryClient.invalidateQueries({ queryKey: ['system-options'] })
  if ([...keys].some((key) => STATUS_RELATED_KEYS.has(key))) {
    queryClient.invalidateQueries({ queryKey: ['status'] })
    try {
      window.localStorage.removeItem('status')
    } catch {
      /* empty */
    }
  }
}

export function useUpdateOption() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (request: UpdateOptionRequest) => updateSystemOption(request),
    onSuccess: (data, variables) => {
      if (data.success) {
        invalidateOptionQueries(queryClient, [variables.key])
        toast.success(i18next.t('Setting updated successfully'))
      } else {
        toast.error(data.message || i18next.t('Failed to update setting'))
      }
    },
    onError: (error: Error) => {
      toast.error(error.message || i18next.t('Failed to update setting'))
    },
  })
}

export function useUpdateOptions() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (values: Record<string, string>) => updateSystemOptions(values),
    onSuccess: (data, variables) => {
      if (data.success) {
        invalidateOptionQueries(queryClient, Object.keys(variables))
        toast.success(i18next.t('Setting updated successfully'))
      } else {
        toast.error(data.message || i18next.t('Failed to update setting'))
      }
    },
    onError: (error: Error) => {
      toast.error(error.message || i18next.t('Failed to update setting'))
    },
  })
}
