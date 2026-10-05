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
import { ErrorState } from '@/components/error-state'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { getServerErrorToastId } from '@/lib/server-error-message'

import { getSystemOptions, resetModelRatios, updateSystemOption } from '../api'
import { SettingsPageTitleStatusPortal } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { positiveIntegerSchema } from '../utils/numeric-field'
import { showOptionUpdateToast } from '../utils/option-update-toast'
import { getSettingsErrorMessage } from '../utils/settings-error-message'
import { GroupRatioForm } from './group-ratio-form'
import { isValidGroupWarnings } from './group-warning-validation'
import {
  getModelPricingConfig,
  MODEL_PRICING_QUERY_KEY,
  updateModelPricingConfig,
  useModelPricingConfig,
} from './model-pricing-api'
import {
  acceptModelPricingSave,
  modelPricingFormSnapshot,
  type ModelPricingSaveReceipt,
} from './model-pricing-save'
import { ModelPricingUnitsContext } from './model-pricing-units'
import { ModelRatioForm } from './model-ratio-form'
import { ToolPriceSettings } from './tool-price-settings'
import { UpstreamRatioSync } from './upstream-ratio-sync'
import { useGroupRatioSettings } from './use-group-ratio-settings'
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
  modelDefaults: legacyModelDefaults,
  groupDefaults,
  toolPricesDefault: _toolPricesDefault,
  titleKey = 'Pricing Ratios',
  visibleTabs = ['models', 'groups', 'tool-prices', 'upstream-sync'],
}: RatioSettingsCardProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [exposeRatioEnabled, setExposeRatioEnabled] = useState(
    legacyModelDefaults.ExposeRatioEnabled
  )
  useEffect(() => {
    setExposeRatioEnabled(legacyModelDefaults.ExposeRatioEnabled)
  }, [legacyModelDefaults.ExposeRatioEnabled])
  const needsUsdPricing = visibleTabs.some((tab) => tab !== 'groups')
  const pricingQuery = useModelPricingConfig(needsUsdPricing)
  const modelPricingBaseline = useRef(pricingQuery.data)
  const modelDefaults = useMemo<ModelFormValues>(() => {
    const values = pricingQuery.data?.values
    return {
      ModelPrice: values?.ModelPrice || '{}',
      ModelRatio: values?.ModelRatio || '{}',
      CacheRatio: values?.CacheRatio || '{}',
      CreateCacheRatio: values?.CreateCacheRatio || '{}',
      CompletionRatio: values?.CompletionRatio || '{}',
      ImageRatio: values?.ImageRatio || '{}',
      AudioRatio: values?.AudioRatio || '{}',
      AudioCompletionRatio: values?.AudioCompletionRatio || '{}',
      BillingMode: values?.['billing_setting.billing_mode'] || '{}',
      BillingExpr: values?.['billing_setting.billing_expr'] || '{}',
      ExposeRatioEnabled: exposeRatioEnabled,
    }
  }, [pricingQuery.data, exposeRatioEnabled])

  const resetMutation = useMutation({
    mutationFn: resetModelRatios,
    onSuccess: async (data) => {
      if (data.success) {
        setConfirmOpen(false)
        try {
          await reloadModelValues()
          showOptionUpdateToast(data, t('Model prices reset successfully'))
        } catch (error) {
          toast.warning(
            t('Settings saved, but refreshing failed: {{reason}}', {
              reason: getSettingsErrorMessage(
                error,
                t('Failed to load settings')
              ),
            }),
            { id: getServerErrorToastId(error) }
          )
        }
      } else {
        toast.error(data.message || t('Failed to reset model ratios'))
      }
    },
    onError: (error: Error) => {
      toast.error(
        getSettingsErrorMessage(error, t('Failed to reset model ratios')),
        {
          id: getServerErrorToastId(error),
        }
      )
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
  const isModelFormDirty = modelForm.formState.isDirty

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
    // A background refresh cannot replace a draft's values or CAS revision.
    if (isModelFormDirty) return
    modelPricingBaseline.current = pricingQuery.data
    const unchanged = Object.entries(modelDefaults).every(
      ([key, value]) =>
        (typeof value === 'boolean' ? value : normalizeJsonString(value)) ===
        modelNormalizedDefaults.current[key as keyof ModelFormValues]
    )
    if (!unchanged) applyModelDefaults(modelDefaults)
  }, [applyModelDefaults, modelDefaults, pricingQuery.data, isModelFormDirty])

  const acceptSavedModelValues = useCallback(
    (
      config: ModelPricingSaveReceipt['data'],
      exposure = modelNormalizedDefaults.current.ExposeRatioEnabled
    ) => {
      modelPricingBaseline.current = config
      queryClient.setQueryData(MODEL_PRICING_QUERY_KEY, config)
      setExposeRatioEnabled(exposure)
      applyModelDefaults(modelPricingFormSnapshot(config, exposure))
    },
    [applyModelDefaults, queryClient]
  )

  const reloadModelValues = useCallback(async () => {
    await Promise.all([
      queryClient.cancelQueries({ queryKey: ['system-options'] }),
      queryClient.cancelQueries({ queryKey: MODEL_PRICING_QUERY_KEY }),
    ])
    const [response, config] = await Promise.all([
      getSystemOptions({ silent: true }),
      getModelPricingConfig({ silent: true }),
    ])
    if (!response.success) {
      throw new Error(response.message || t('Failed to load settings'))
    }
    const exposure = response.data.find(
      ({ key }) => key === 'ExposeRatioEnabled'
    )?.value
    acceptSavedModelValues(
      config,
      exposure === undefined
        ? modelNormalizedDefaults.current.ExposeRatioEnabled
        : exposure === 'true'
    )
    queryClient.setQueryData(['system-options'], response)
  }, [acceptSavedModelValues, queryClient, t])

  const modelUpdateMutation = useMutation({
    retry: false,
    mutationFn: async (values: Record<string, string>) => {
      const { ExposeRatioEnabled, ...prices } = values
      const pricesChanged = Object.keys(prices).length > 0
      const config = pricesChanged
        ? modelPricingBaseline.current
        : pricingQuery.data
      if (!config) {
        throw new Error(t('Failed to load USD model prices'))
      }
      await queryClient.cancelQueries({ queryKey: MODEL_PRICING_QUERY_KEY })
      const response: ModelPricingSaveReceipt = pricesChanged
        ? await updateModelPricingConfig(config, prices, false, {
            silent: true,
          })
        : { success: true as const, message: '', data: config }
      // Apply the server's locked-model filtering and actual stored values as
      // soon as the price POST commits, before any independent follow-up.
      if (pricesChanged) acceptSavedModelValues(response.data)
      let acceptedExposure = modelNormalizedDefaults.current.ExposeRatioEnabled
      let visibilityError: unknown
      if (ExposeRatioEnabled !== undefined) {
        try {
          const exposeResponse = await updateSystemOption(
            {
              key: 'ExposeRatioEnabled',
              value: ExposeRatioEnabled,
            },
            { silent: true }
          )
          if (!exposeResponse.success) {
            throw new Error(
              exposeResponse.message || t('Failed to update setting')
            )
          }
          acceptedExposure = ExposeRatioEnabled === 'true'
        } catch (error) {
          if (!pricesChanged) throw error
          visibilityError = error
        }
      }
      return { ...response, acceptedExposure, visibilityError }
    },
    onSuccess: async (response) => {
      const { refreshError } = await acceptModelPricingSave(
        response,
        (config) => acceptSavedModelValues(config, response.acceptedExposure),
        reloadModelValues
      )
      const warnings = response.warnings ? [...response.warnings] : []
      if (response.visibilityError) {
        warnings.push(
          t(
            'Model prices saved, but updating price visibility failed: {{reason}}',
            {
              reason: getSettingsErrorMessage(
                response.visibilityError,
                t('Failed to update setting')
              ),
            }
          )
        )
      }
      if (refreshError) {
        warnings.push(
          t('Settings saved, but refreshing failed: {{reason}}', {
            reason: getSettingsErrorMessage(
              refreshError,
              t('Failed to load settings')
            ),
          })
        )
      }
      if (warnings.length) {
        toast.warning(warnings.join('\n'), {
          id:
            getServerErrorToastId(refreshError) ??
            getServerErrorToastId(response.visibilityError),
        })
      } else {
        showOptionUpdateToast(response, t('Setting updated successfully'))
      }
    },
    onError: (error: Error) => {
      toast.error(
        getSettingsErrorMessage(error, t('Failed to update setting')),
        {
          id: getServerErrorToastId(error) ?? undefined,
        }
      )
    },
  })

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

  const { saveGroupRatios, isSaving: isGroupSaving } = useGroupRatioSettings(
    groupForm,
    groupDefaults
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
    if (tab !== 'groups' && !pricingQuery.data) {
      return pricingQuery.isError ? (
        <ErrorState
          title={t('Failed to load USD model prices')}
          description={getSettingsErrorMessage(
            pricingQuery.error,
            t('Failed to load USD model prices')
          )}
          onRetry={() => {
            void pricingQuery.refetch()
          }}
        />
      ) : (
        <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
      )
    }
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
          isSaving={isGroupSaving}
        />
      )
    }
    if (tab === 'tool-prices') {
      return (
        <ToolPriceSettings
          defaultValue={
            pricingQuery.data?.values['tool_price_setting.prices'] || '{}'
          }
          pricingConfig={pricingQuery.data}
        />
      )
    }
    return <UpstreamRatioSync />
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
    <ModelPricingUnitsContext
      value={pricingQuery.data?.credits_per_usd ?? Number.NaN}
    >
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
    </ModelPricingUnitsContext>
  )
}
