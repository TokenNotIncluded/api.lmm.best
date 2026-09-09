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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { getSystemOptions, resetModelRatios, updateSystemOptions } from '../api'
import { SettingsPageTitleStatusPortal } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { getOptionValue } from '../hooks/use-system-options'
import { positiveIntegerSchema } from '../utils/numeric-field'
import { showOptionUpdateToast } from '../utils/option-update-toast'
import { GroupRatioForm } from './group-ratio-form'
import {
  changedGroupRatioOptions,
  type GroupRatioOptionValues,
} from './group-ratio-option-values'
import { isValidGroupWarnings } from './group-warning-validation'
import { ModelRatioForm } from './model-ratio-form'
import { ToolPriceSettings } from './tool-price-settings'
import { UpstreamRatioSync } from './upstream-ratio-sync'
import {
  formatJsonForTextarea,
  type JsonValidationError,
  normalizeJsonString,
  validateJsonString,
} from './utils'

type Translate = (key: string, options?: Record<string, unknown>) => string

function formatJsonValidationError(
  t: Translate,
  error?: JsonValidationError,
  fallback = 'Invalid JSON'
) {
  if (!error) return t(fallback)

  if (error.type === 'required') return t('Value is required')
  if (error.type === 'structure') {
    return t(
      fallback === 'Invalid JSON' ? 'JSON structure is invalid' : fallback
    )
  }

  let locationMessage: string
  if (error.line && error.column) {
    locationMessage = t(
      'JSON is invalid at line {{line}}, column {{column}}.',
      {
        line: error.line,
        column: error.column,
      }
    )
  } else if (error.position !== undefined) {
    locationMessage = t('JSON is invalid at position {{position}}.', {
      position: error.position,
    })
  } else {
    locationMessage = t('JSON is invalid. Please check the syntax.')
  }

  const parts = [locationMessage]

  if (error.missingCommaLine) {
    parts.push(
      t('Check line {{line}} for a missing comma.', {
        line: error.missingCommaLine,
      })
    )
  }

  return parts.join(' ')
}

function createJsonStringField(
  t: Translate,
  options?: Parameters<typeof validateJsonString>[1]
) {
  return z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value, options)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: formatJsonValidationError(t, result.error, result.message),
      })
    }
  })
}

const createModelSchema = (t: Translate) =>
  z.object({
    ModelPrice: createJsonStringField(t),
    ModelRatio: createJsonStringField(t),
    CacheRatio: createJsonStringField(t),
    CreateCacheRatio: createJsonStringField(t),
    CompletionRatio: createJsonStringField(t),
    ImageRatio: createJsonStringField(t),
    AudioRatio: createJsonStringField(t),
    AudioCompletionRatio: createJsonStringField(t),
    ExposeRatioEnabled: z.boolean(),
    BillingMode: createJsonStringField(t),
    BillingExpr: createJsonStringField(t),
  })

const createGroupSchema = (t: Translate) =>
  z.object({
    GroupRatio: createJsonStringField(t),
    TopupGroupRatio: createJsonStringField(t),
    UserUsableGroups: createJsonStringField(t),
    GroupGroupRatio: createJsonStringField(t),
    AutoGroups: createJsonStringField(t, {
      predicate: (parsed) =>
        Array.isArray(parsed) &&
        parsed.every((item) => typeof item === 'string'),
      predicateMessage: 'Expected a JSON array of group identifiers',
    }),
    MaxTokenAutoGroups: positiveIntegerSchema(t('Enter a positive integer')),
    DefaultUseAutoGroup: z.boolean(),
    GroupSpecialUsableGroup: createJsonStringField(t),
    GroupWarnings: createJsonStringField(t, {
      predicate: isValidGroupWarnings,
    }),
  })

type ModelFormValues = z.infer<ReturnType<typeof createModelSchema>>
type GroupFormValues = z.infer<ReturnType<typeof createGroupSchema>>
type RatioTabId =
  | 'models'
  | 'unset-models'
  | 'groups'
  | 'tool-prices'
  | 'upstream-sync'

type RatioSettingsCardProps = {
  modelDefaults: ModelFormValues
  groupDefaults: GroupFormValues
  toolPricesDefault: string
  titleKey?: string
  visibleTabs?: RatioTabId[]
}

export function RatioSettingsCard({
  modelDefaults,
  groupDefaults,
  toolPricesDefault,
  titleKey = 'Pricing Ratios',
  visibleTabs = ['models', 'groups', 'tool-prices', 'upstream-sync'],
}: RatioSettingsCardProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [confirmOpen, setConfirmOpen] = useState(false)

  const resetMutation = useMutation({
    mutationFn: resetModelRatios,
    onSuccess: async (data) => {
      if (data.success) {
        await reloadModelValues()
        showOptionUpdateToast(data, t('Model prices reset successfully'))
        setConfirmOpen(false)
      } else {
        toast.error(data.message || t('Failed to reset model ratios'))
      }
    },
    onError: (error: Error) => {
      toast.error(error.message || t('Failed to reset model ratios'))
    },
  })

  const groupUpdateMutation = useMutation({
    mutationFn: async (values: Record<string, string>) => {
      const response = await updateSystemOptions(values)
      if (!response.success) {
        throw new Error(response.message || t('Failed to update setting'))
      }
      return response
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['system-options'] })
      toast.success(t('Setting updated successfully'))
    },
    onError: (error: Error) => {
      toast.error(error.message || t('Failed to update setting'))
    },
  })

  const modelNormalizedDefaults = useRef({
    ModelPrice: normalizeJsonString(modelDefaults.ModelPrice),
    ModelRatio: normalizeJsonString(modelDefaults.ModelRatio),
    CacheRatio: normalizeJsonString(modelDefaults.CacheRatio),
    CreateCacheRatio: normalizeJsonString(modelDefaults.CreateCacheRatio),
    CompletionRatio: normalizeJsonString(modelDefaults.CompletionRatio),
    ImageRatio: normalizeJsonString(modelDefaults.ImageRatio),
    AudioRatio: normalizeJsonString(modelDefaults.AudioRatio),
    AudioCompletionRatio: normalizeJsonString(
      modelDefaults.AudioCompletionRatio
    ),
    ExposeRatioEnabled: modelDefaults.ExposeRatioEnabled,
    BillingMode: normalizeJsonString(modelDefaults.BillingMode),
    BillingExpr: normalizeJsonString(modelDefaults.BillingExpr),
  })
  const [savedModelValues, setSavedModelValues] = useState(
    modelNormalizedDefaults.current
  )

  const groupNormalizedDefaults = useRef({
    GroupRatio: normalizeJsonString(groupDefaults.GroupRatio),
    TopupGroupRatio: normalizeJsonString(groupDefaults.TopupGroupRatio),
    UserUsableGroups: normalizeJsonString(groupDefaults.UserUsableGroups),
    GroupGroupRatio: normalizeJsonString(groupDefaults.GroupGroupRatio),
    AutoGroups: normalizeJsonString(groupDefaults.AutoGroups),
    MaxTokenAutoGroups: groupDefaults.MaxTokenAutoGroups,
    DefaultUseAutoGroup: groupDefaults.DefaultUseAutoGroup,
    GroupSpecialUsableGroup: normalizeJsonString(
      groupDefaults.GroupSpecialUsableGroup
    ),
    GroupWarnings: normalizeJsonString(groupDefaults.GroupWarnings),
  })
  const modelSchema = useMemo(() => createModelSchema(t), [t])
  const groupSchema = useMemo(() => createGroupSchema(t), [t])

  const modelForm = useForm<ModelFormValues>({
    resolver: zodResolver(modelSchema),
    mode: 'onChange',
    defaultValues: {
      ...modelDefaults,
      ModelPrice: formatJsonForTextarea(modelDefaults.ModelPrice),
      ModelRatio: formatJsonForTextarea(modelDefaults.ModelRatio),
      CacheRatio: formatJsonForTextarea(modelDefaults.CacheRatio),
      CreateCacheRatio: formatJsonForTextarea(modelDefaults.CreateCacheRatio),
      CompletionRatio: formatJsonForTextarea(modelDefaults.CompletionRatio),
      ImageRatio: formatJsonForTextarea(modelDefaults.ImageRatio),
      AudioRatio: formatJsonForTextarea(modelDefaults.AudioRatio),
      AudioCompletionRatio: formatJsonForTextarea(
        modelDefaults.AudioCompletionRatio
      ),
      BillingMode: formatJsonForTextarea(modelDefaults.BillingMode),
      BillingExpr: formatJsonForTextarea(modelDefaults.BillingExpr),
    },
  })

  const groupForm = useForm<GroupFormValues>({
    resolver: zodResolver(groupSchema),
    mode: 'onChange',
    defaultValues: {
      ...groupDefaults,
      GroupRatio: formatJsonForTextarea(groupDefaults.GroupRatio),
      TopupGroupRatio: formatJsonForTextarea(groupDefaults.TopupGroupRatio),
      UserUsableGroups: formatJsonForTextarea(groupDefaults.UserUsableGroups),
      GroupGroupRatio: formatJsonForTextarea(groupDefaults.GroupGroupRatio),
      AutoGroups: formatJsonForTextarea(groupDefaults.AutoGroups),
      GroupSpecialUsableGroup: formatJsonForTextarea(
        groupDefaults.GroupSpecialUsableGroup
      ),
      GroupWarnings: formatJsonForTextarea(groupDefaults.GroupWarnings),
    },
  })

  const applyModelDefaults = useCallback(
    (defaults: ModelFormValues) => {
      modelNormalizedDefaults.current = {
        ModelPrice: normalizeJsonString(defaults.ModelPrice),
        ModelRatio: normalizeJsonString(defaults.ModelRatio),
        CacheRatio: normalizeJsonString(defaults.CacheRatio),
        CreateCacheRatio: normalizeJsonString(defaults.CreateCacheRatio),
        CompletionRatio: normalizeJsonString(defaults.CompletionRatio),
        ImageRatio: normalizeJsonString(defaults.ImageRatio),
        AudioRatio: normalizeJsonString(defaults.AudioRatio),
        AudioCompletionRatio: normalizeJsonString(
          defaults.AudioCompletionRatio
        ),
        ExposeRatioEnabled: defaults.ExposeRatioEnabled,
        BillingMode: normalizeJsonString(defaults.BillingMode),
        BillingExpr: normalizeJsonString(defaults.BillingExpr),
      }
      setSavedModelValues(modelNormalizedDefaults.current)

      modelForm.reset({
        ...defaults,
        ModelPrice: formatJsonForTextarea(defaults.ModelPrice),
        ModelRatio: formatJsonForTextarea(defaults.ModelRatio),
        CacheRatio: formatJsonForTextarea(defaults.CacheRatio),
        CreateCacheRatio: formatJsonForTextarea(defaults.CreateCacheRatio),
        CompletionRatio: formatJsonForTextarea(defaults.CompletionRatio),
        ImageRatio: formatJsonForTextarea(defaults.ImageRatio),
        AudioRatio: formatJsonForTextarea(defaults.AudioRatio),
        AudioCompletionRatio: formatJsonForTextarea(
          defaults.AudioCompletionRatio
        ),
        BillingMode: formatJsonForTextarea(defaults.BillingMode),
        BillingExpr: formatJsonForTextarea(defaults.BillingExpr),
      })
    },
    [modelForm]
  )

  useEffect(() => {
    const unchanged = Object.entries(modelDefaults).every(
      ([key, value]) =>
        (typeof value === 'boolean' ? value : normalizeJsonString(value)) ===
        modelNormalizedDefaults.current[key as keyof ModelFormValues]
    )
    if (!unchanged) applyModelDefaults(modelDefaults)
  }, [applyModelDefaults, modelDefaults])

  const reloadModelValues = useCallback(async () => {
    await queryClient.cancelQueries({ queryKey: ['system-options'] })
    const response = await getSystemOptions()
    if (!response.success) {
      throw new Error(response.message || t('Failed to load settings'))
    }
    const formKeyMap: Record<string, string> = {
      'billing_setting.billing_mode': 'BillingMode',
      'billing_setting.billing_expr': 'BillingExpr',
    }
    const acceptedOptions = response.data.map((option) => ({
      ...option,
      key: formKeyMap[option.key] || option.key,
    }))
    // A locked-only update leaves the query data unchanged; reset explicitly so
    // rejected edits never become the form's saved snapshot.
    applyModelDefaults(getOptionValue(acceptedOptions, modelDefaults))
    queryClient.setQueryData(['system-options'], response)
  }, [applyModelDefaults, modelDefaults, queryClient, t])

  const modelUpdateMutation = useMutation({
    mutationFn: async (values: Record<string, string>) => {
      const response = await updateSystemOptions(values)
      if (!response.success) {
        throw new Error(response.message || t('Failed to update setting'))
      }
      return response
    },
    onSuccess: async (response) => {
      await reloadModelValues()
      showOptionUpdateToast(response, t('Setting updated successfully'))
    },
    onError: (error: Error) => {
      toast.error(error.message || t('Failed to update setting'))
    },
  })

  useEffect(() => {
    groupNormalizedDefaults.current = {
      GroupRatio: normalizeJsonString(groupDefaults.GroupRatio),
      TopupGroupRatio: normalizeJsonString(groupDefaults.TopupGroupRatio),
      UserUsableGroups: normalizeJsonString(groupDefaults.UserUsableGroups),
      GroupGroupRatio: normalizeJsonString(groupDefaults.GroupGroupRatio),
      AutoGroups: normalizeJsonString(groupDefaults.AutoGroups),
      MaxTokenAutoGroups: groupDefaults.MaxTokenAutoGroups,
      DefaultUseAutoGroup: groupDefaults.DefaultUseAutoGroup,
      GroupSpecialUsableGroup: normalizeJsonString(
        groupDefaults.GroupSpecialUsableGroup
      ),
      GroupWarnings: normalizeJsonString(groupDefaults.GroupWarnings),
    }

    groupForm.reset({
      ...groupDefaults,
      GroupRatio: formatJsonForTextarea(groupDefaults.GroupRatio),
      TopupGroupRatio: formatJsonForTextarea(groupDefaults.TopupGroupRatio),
      UserUsableGroups: formatJsonForTextarea(groupDefaults.UserUsableGroups),
      GroupGroupRatio: formatJsonForTextarea(groupDefaults.GroupGroupRatio),
      AutoGroups: formatJsonForTextarea(groupDefaults.AutoGroups),
      MaxTokenAutoGroups: groupDefaults.MaxTokenAutoGroups,
      GroupSpecialUsableGroup: formatJsonForTextarea(
        groupDefaults.GroupSpecialUsableGroup
      ),
      GroupWarnings: formatJsonForTextarea(groupDefaults.GroupWarnings),
    })
  }, [groupDefaults, groupForm])

  const saveModelRatios = useCallback(
    async (values: ModelFormValues) => {
      const normalized = {
        ModelPrice: normalizeJsonString(values.ModelPrice),
        ModelRatio: normalizeJsonString(values.ModelRatio),
        CacheRatio: normalizeJsonString(values.CacheRatio),
        CreateCacheRatio: normalizeJsonString(values.CreateCacheRatio),
        CompletionRatio: normalizeJsonString(values.CompletionRatio),
        ImageRatio: normalizeJsonString(values.ImageRatio),
        AudioRatio: normalizeJsonString(values.AudioRatio),
        AudioCompletionRatio: normalizeJsonString(values.AudioCompletionRatio),
        ExposeRatioEnabled: values.ExposeRatioEnabled,
        BillingMode: normalizeJsonString(values.BillingMode),
        BillingExpr: normalizeJsonString(values.BillingExpr),
      }

      const apiKeyMap: Record<string, string> = {
        BillingMode: 'billing_setting.billing_mode',
        BillingExpr: 'billing_setting.billing_expr',
      }

      const updates = (
        Object.keys(normalized) as Array<keyof ModelFormValues>
      ).filter(
        (key) => normalized[key] !== modelNormalizedDefaults.current[key]
      )

      if (updates.length === 0) {
        toast.info(t('No model price changes to save'))
        return
      }

      await modelUpdateMutation.mutateAsync(
        Object.fromEntries(
          updates.map((key) => [apiKeyMap[key] || key, String(normalized[key])])
        )
      )
    },
    [modelUpdateMutation, t]
  )

  const saveGroupRatios = useCallback(
    async (values: GroupFormValues) => {
      const normalized: GroupRatioOptionValues = {
        GroupRatio: normalizeJsonString(values.GroupRatio),
        TopupGroupRatio: normalizeJsonString(values.TopupGroupRatio),
        UserUsableGroups: normalizeJsonString(values.UserUsableGroups),
        GroupGroupRatio: normalizeJsonString(values.GroupGroupRatio),
        AutoGroups: normalizeJsonString(values.AutoGroups),
        MaxTokenAutoGroups: values.MaxTokenAutoGroups,
        DefaultUseAutoGroup: values.DefaultUseAutoGroup,
        GroupSpecialUsableGroup: normalizeJsonString(
          values.GroupSpecialUsableGroup
        ),
        GroupWarnings: normalizeJsonString(values.GroupWarnings),
      }

      const updates = changedGroupRatioOptions(
        normalized,
        groupNormalizedDefaults.current
      )
      if (Object.keys(updates).length === 0) {
        toast.info(t('No changes to save'))
        return
      }

      await groupUpdateMutation.mutateAsync(updates)
      groupNormalizedDefaults.current = normalized
    },
    [groupUpdateMutation, t]
  )

  const handleResetRatios = useCallback(() => {
    setConfirmOpen(true)
  }, [])

  const { mutate: resetMutate } = resetMutation
  const handleConfirmReset = useCallback(() => {
    resetMutate()
  }, [resetMutate])

  const tabLabels: Record<RatioTabId, string> = {
    models: 'Model prices',
    'unset-models': 'Unset price models',
    groups: 'Pricing groups',
    'tool-prices': 'Tool prices',
    'upstream-sync': 'Upstream price sync',
  }
  const tabsGridClass =
    {
      1: 'grid-cols-1',
      2: 'grid-cols-2',
      3: 'grid-cols-3',
      4: 'grid-cols-4',
      5: 'grid-cols-5',
    }[visibleTabs.length] ?? 'grid-cols-4'
  const defaultTab = visibleTabs[0] ?? 'models'

  const renderTabContent = (tab: RatioTabId) => {
    if (tab === 'models' || tab === 'unset-models') {
      return (
        <ModelRatioForm
          form={modelForm}
          savedValues={savedModelValues}
          onSave={saveModelRatios}
          onReset={handleResetRatios}
          isSaving={modelUpdateMutation.isPending}
          isResetting={resetMutation.isPending}
          variant={tab === 'unset-models' ? 'unset' : 'default'}
        />
      )
    }
    if (tab === 'groups') {
      return (
        <GroupRatioForm
          form={groupForm}
          onSave={saveGroupRatios}
          isSaving={groupUpdateMutation.isPending}
        />
      )
    }
    if (tab === 'tool-prices') {
      return <ToolPriceSettings defaultValue={toolPricesDefault} />
    }
    return (
      <UpstreamRatioSync
        modelRatios={{
          ModelPrice: modelDefaults.ModelPrice,
          ModelRatio: modelDefaults.ModelRatio,
          CompletionRatio: modelDefaults.CompletionRatio,
          CacheRatio: modelDefaults.CacheRatio,
          CreateCacheRatio: modelDefaults.CreateCacheRatio,
          ImageRatio: modelDefaults.ImageRatio,
          AudioRatio: modelDefaults.AudioRatio,
          AudioCompletionRatio: modelDefaults.AudioCompletionRatio,
          'billing_setting.billing_mode': modelDefaults.BillingMode,
          'billing_setting.billing_expr': modelDefaults.BillingExpr,
        }}
      />
    )
  }

  const renderTabSwitcher = () => (
    <TabsList className={`grid w-fit max-w-full ${tabsGridClass}`}>
      {visibleTabs.map((tab) => (
        <TabsTrigger key={tab} value={tab}>
          {t(tabLabels[tab])}
        </TabsTrigger>
      ))}
    </TabsList>
  )

  return (
    <>
      {visibleTabs.length === 1 ? (
        <SettingsSection title={t(titleKey)}>
          {renderTabContent(defaultTab)}
        </SettingsSection>
      ) : (
        <Tabs defaultValue={defaultTab} className='h-full min-h-0 gap-6'>
          <SettingsPageTitleStatusPortal>
            {renderTabSwitcher()}
          </SettingsPageTitleStatusPortal>

          <SettingsSection title={t(titleKey)} className='min-h-0 flex-1'>
            {visibleTabs.map((tab) => (
              <TabsContent key={tab} value={tab} className='min-h-0'>
                {renderTabContent(tab)}
              </TabsContent>
            ))}
          </SettingsSection>
        </Tabs>
      )}

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Reset all model prices?')}
        desc={t(
          'This will clear custom pricing ratios and revert to upstream defaults.'
        )}
        destructive
        isLoading={resetMutation.isPending}
        handleConfirm={handleConfirmReset}
        confirmText={t('Reset')}
      />
    </>
  )
}
