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
import { useCallback, useEffect, useRef } from 'react'
import type { UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { getSystemOptions, updateSystemOptions } from '../api'
import { getOptionValue } from '../hooks/use-system-options'
import { showOptionUpdateToast } from '../utils/option-update-toast'
import {
  changedGroupRatioOptions,
  type GroupRatioOptionValues,
} from './group-ratio-option-values'
import {
  formatGroupRatioValues,
  mergeGroupRatioDraft,
  normalizeGroupRatioValues,
} from './group-ratio-save-state'

type SaveRequest = {
  updates: Record<string, string>
  submitted: GroupRatioOptionValues
}

const formKeyMap: Record<string, string> = {
  'group_ratio_setting.group_special_usable_group': 'GroupSpecialUsableGroup',
  'group_ratio_setting.group_warnings': 'GroupWarnings',
}

export function useGroupRatioSettings(
  form: UseFormReturn<GroupRatioOptionValues>,
  defaults: GroupRatioOptionValues
) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const saved = useRef(normalizeGroupRatioValues(defaults))
  // Prop identity is not a server revision. Keep this separate from saved so
  // stale props cannot undo an acknowledged save when its refresh fails.
  const received = useRef(saved.current)
  const saving = useRef(false)

  const applyDefaults = useCallback(
    (incoming: GroupRatioOptionValues, baseline = saved.current) => {
      const normalized = normalizeGroupRatioValues(incoming)
      const merged = mergeGroupRatioDraft(form.getValues(), baseline, incoming)
      const normalizedMerged = normalizeGroupRatioValues(merged)
      saved.current = normalized
      form.reset(formatGroupRatioValues(incoming))
      const keys = Object.keys(merged) as Array<keyof GroupRatioOptionValues>
      for (const key of keys) {
        if (normalizedMerged[key] !== normalized[key]) {
          form.setValue(key, merged[key], {
            shouldDirty: true,
            shouldValidate: true,
          })
        }
      }
    },
    [form]
  )

  const mutation = useMutation({
    mutationFn: async ({ updates }: SaveRequest) => {
      await queryClient.cancelQueries({ queryKey: ['system-options'] })
      const response = await updateSystemOptions(updates)
      if (!response.success) {
        throw new Error(response.message || t('Failed to update setting'))
      }
      return response
    },
    onSuccess: async (response, { submitted }) => {
      // The write succeeded. A subsequent read failure is not a failed save and
      // must neither restore the old prices nor cause the same write on retry.
      applyDefaults(submitted, submitted)
      try {
        await queryClient.cancelQueries({ queryKey: ['system-options'] })
        const options = await getSystemOptions()
        if (!options.success) {
          throw new Error(options.message || t('Failed to load settings'))
        }
        const acceptedOptions = options.data.map((option) => ({
          ...option,
          key: formKeyMap[option.key] ?? option.key,
        }))
        applyDefaults(getOptionValue(acceptedOptions, submitted), submitted)
        queryClient.setQueryData(['system-options'], options)
      } catch {
        toast.warning(t('Setting updated successfully'), {
          description: t('Failed to load settings'),
        })
        return
      }
      showOptionUpdateToast(response, t('Setting updated successfully'))
    },
    onError: (error: Error) => {
      toast.error(error.message || t('Failed to update setting'))
    },
  })

  useEffect(() => {
    if (mutation.isPending) return
    const incoming = normalizeGroupRatioValues(defaults)
    const changes = changedGroupRatioOptions(incoming, received.current)
    if (Object.keys(changes).length === 0) return
    received.current = incoming
    applyDefaults(incoming)
  }, [applyDefaults, defaults, mutation.isPending])

  const { mutateAsync, isPending } = mutation
  const saveGroupRatios = useCallback(
    async (values: GroupRatioOptionValues) => {
      // The ref also covers repeated clicks before React renders isPending.
      if (saving.current) return
      const submitted = normalizeGroupRatioValues(values)
      const updates = changedGroupRatioOptions(submitted, saved.current)
      if (Object.keys(updates).length === 0) {
        toast.info(t('No changes to save'))
        return
      }
      saving.current = true
      try {
        await mutateAsync({ updates, submitted })
      } catch {
        // onError displays the failure. Keep the draft and saved baseline for
        // a real retry, without leaking a rejected promise from handleSubmit.
      } finally {
        saving.current = false
      }
    },
    [mutateAsync, t]
  )

  return { saveGroupRatios, isSaving: isPending }
}
