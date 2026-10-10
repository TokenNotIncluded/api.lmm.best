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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckSquare, RefreshCcw } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'

import {
  fetchUpstreamRatios,
  getUpstreamChannels,
  getSystemOptions,
} from '../api'
import type {
  DifferencesMap,
  RatioType,
  TestResult,
  UpstreamChannel,
  UpstreamConfig,
} from '../types'
import { getSettingsErrorMessage } from '../utils/settings-error-message'
import { ChannelSelectorDialog } from './channel-selector-dialog'
import {
  ConflictConfirmDialog,
  type ConflictItem,
} from './conflict-confirm-dialog'
import {
  DEFAULT_ENDPOINT,
  MODELS_DEV_PRESET_ENDPOINT,
  MODELS_DEV_PRESET_ID,
  OFFICIAL_CHANNEL_ENDPOINT,
  OFFICIAL_CHANNEL_ID,
  OPENROUTER_CHANNEL_TYPE,
  OPENROUTER_ENDPOINT,
} from './constants'
import {
  MODEL_PRICING_QUERY_KEY,
  acceptModelPricingResponse,
  getModelPricingConfig,
  updateModelPricingConfig,
  type ModelPricingConfig,
} from './model-pricing-api'
import { acceptModelPricingSave } from './model-pricing-save'
import {
  applyResolutionRemovalPlan,
  applyResolutionSelection,
  applyResolutionSelections,
  buildUpstreamPricingUpdates,
  deleteResolutionField,
  getLocalSyncBillingCategory,
  getUpstreamDisplayName,
  getSyncFieldDisplayValue,
  parseSyncPricingMaps,
  type ResolutionRemovalPlan,
  type ResolutionSelection,
  type ResolutionsMap,
} from './upstream-ratio-sync-helpers'
import { UpstreamRatioSyncTable } from './upstream-ratio-sync-table'

type SyncPlan = {
  config: ModelPricingConfig
  resolutions: ResolutionsMap
  differences: DifferencesMap
}

function getDefaultEndpointForChannel(channel: UpstreamChannel): string {
  if (channel.id === MODELS_DEV_PRESET_ID) return MODELS_DEV_PRESET_ENDPOINT
  if (channel.id === OFFICIAL_CHANNEL_ID) return OFFICIAL_CHANNEL_ENDPOINT
  if (channel.type === OPENROUTER_CHANNEL_TYPE) return OPENROUTER_ENDPOINT
  return DEFAULT_ENDPOINT
}

export function UpstreamRatioSync() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [channelDialogOpen, setChannelDialogOpen] = useState(false)
  const [conflictDialogOpen, setConflictDialogOpen] = useState(false)
  const [selectedChannelIds, setSelectedChannelIds] = useState<number[]>([])
  const [channelEndpoints, setChannelEndpoints] = useState<
    Record<number, string>
  >({})
  const [differences, setDifferences] = useState<DifferencesMap>({})
  const [resolutions, setResolutions] = useState<ResolutionsMap>({})
  const [pricingConfig, setPricingConfig] = useState<ModelPricingConfig>()
  const [pendingPlan, setPendingPlan] = useState<SyncPlan>()
  const [conflictItems, setConflictItems] = useState<ConflictItem[]>([])
  const [needsRefetch, setNeedsRefetch] = useState(false)
  const [isSaving, setIsSaving] = useState(false)
  const [sourceResults, setSourceResults] = useState<TestResult[]>([])
  const fetchInFlight = useRef(false)
  const saveInFlight = useRef(false)

  const { data: channelsData } = useQuery({
    queryKey: ['upstream-channels'],
    queryFn: getUpstreamChannels,
    enabled: channelDialogOpen,
    retry: false,
  })
  const channels = useMemo(() => channelsData?.data ?? [], [channelsData?.data])
  useEffect(() => {
    if (channels.length === 0) return
    setChannelEndpoints((previous) => {
      const next = { ...previous }
      for (const channel of channels) {
        if (!next[channel.id]) {
          next[channel.id] = getDefaultEndpointForChannel(channel)
        }
      }
      return next
    })
  }, [channels])

  const fetchMutation = useMutation({
    retry: false,
    mutationFn: async (upstreams: UpstreamConfig[]) => {
      const response = await fetchUpstreamRatios(
        { upstreams, timeout: 10 },
        { silent: true }
      )
      if (!response.success) {
        throw new Error(
          response.message || t('Failed to fetch upstream prices')
        )
      }
      if (!response.data?.pricing_config) {
        throw new Error(
          t(
            'Upstream sync requires a server with USD pricing snapshots. Upgrade the server and fetch again.'
          )
        )
      }
      const config = acceptModelPricingResponse({
        success: true,
        data: response.data.pricing_config,
      })
      parseSyncPricingMaps(config)
      return { ...response.data, pricing_config: config }
    },
    onSuccess: (data) => {
      setPricingConfig(data.pricing_config)
      setDifferences(data.differences)
      setResolutions({})
      setSourceResults(data.test_results)
      setPendingPlan(undefined)
      setNeedsRefetch(false)
      const errors = data.test_results.filter(
        (result) => result.status === 'error'
      )
      if (errors.length) {
        toast.warning(
          t('Some channels failed: {{errorMsg}}', {
            errorMsg: errors
              .map(
                (result) =>
                  `${getUpstreamDisplayName(result.name)}: ${result.error}`
              )
              .join(', '),
          })
        )
      } else {
        toast.success(
          t(
            Object.keys(data.differences).length
              ? 'Upstream prices fetched successfully'
              : 'No price differences found'
          )
        )
      }
    },
    onError: (error) => {
      // A failed refresh must not leave an older fetch snapshot available to save.
      setNeedsRefetch(true)
      toast.error(
        getSettingsErrorMessage(error, t('Failed to fetch upstream prices'))
      )
    },
    onSettled: () => {
      fetchInFlight.current = false
    },
  })

  const syncMutation = useMutation({
    retry: false,
    mutationFn: async (plan: SyncPlan) => {
      const updates = buildUpstreamPricingUpdates(plan.config, plan.resolutions)
      if (!Object.keys(updates).length) {
        throw new Error(t('No price differences found'))
      }
      await queryClient.cancelQueries({ queryKey: MODEL_PRICING_QUERY_KEY })
      return updateModelPricingConfig(plan.config, updates, false, {
        silent: true,
      })
    },
  })

  const performSync = async (plan: SyncPlan): Promise<boolean> => {
    if (saveInFlight.current || fetchInFlight.current || needsRefetch) {
      return false
    }
    saveInFlight.current = true
    setIsSaving(true)
    const toastId = toast.loading(t('Syncing prices, please wait...'))
    try {
      const receipt = await syncMutation.mutateAsync(plan)
      const locked = new Set(receipt.locked_models ?? [])
      const { refreshError } = await acceptModelPricingSave(
        receipt,
        (accepted) => {
          queryClient.setQueryData(MODEL_PRICING_QUERY_KEY, accepted)
          setPricingConfig(accepted)
          const acceptedMaps = parseSyncPricingMaps(accepted)
          setDifferences((previous) => {
            const next = { ...previous }
            for (const [model, fields] of Object.entries(plan.resolutions)) {
              if (locked.has(model) || !next[model]) continue
              const remaining = { ...next[model] }
              for (const field of Object.keys(fields)) {
                delete remaining[field as RatioType]
              }
              for (const field of Object.keys(remaining) as RatioType[]) {
                const diff = remaining[field]
                if (diff) {
                  remaining[field] = {
                    ...diff,
                    current: acceptedMaps[field][model] ?? null,
                  }
                }
              }
              if (Object.keys(remaining).length) next[model] = remaining
              else delete next[model]
            }
            return next
          })
          setResolutions(
            Object.fromEntries(
              Object.entries(plan.resolutions).filter(([model]) =>
                locked.has(model)
              )
            )
          )
        },
        async () => {
          // Keep refresh outside POST handling: a read failure is never a failed save.
          const [fresh, options] = await Promise.all([
            getModelPricingConfig({ silent: true }),
            getSystemOptions({ silent: true }),
          ])
          if (!options.success) throw new Error(options.message)
          queryClient.setQueryData(MODEL_PRICING_QUERY_KEY, fresh)
          queryClient.setQueryData(['system-options'], options)
        }
      )
      const messages = [...(receipt.warnings ?? [])]
      if (locked.size && !messages.length) {
        messages.push(
          t('Skipped locked models: {{models}}', {
            models: [...locked].join(', '),
          })
        )
      }
      if (refreshError) {
        messages.push(
          t('Settings saved, but refreshing failed: {{reason}}', {
            reason: getSettingsErrorMessage(
              refreshError,
              t('Failed to fetch upstream prices')
            ),
          })
        )
      }
      if (messages.length) toast.warning(messages.join('\n'), { id: toastId })
      else toast.success(t('Prices synced successfully'), { id: toastId })
      setPendingPlan(undefined)
      setConflictDialogOpen(false)
      return true
    } catch (error) {
      setNeedsRefetch(true)
      toast.error(
        t(getSettingsErrorMessage(error, t('Failed to sync prices'))),
        { id: toastId }
      )
      return false
    } finally {
      saveInFlight.current = false
      setIsSaving(false)
    }
  }

  const handleConfirmChannelSelection = (selectedIds: number[]) => {
    if (fetchInFlight.current || saveInFlight.current) return
    const selected = channels.filter((channel) =>
      selectedIds.includes(channel.id)
    )
    if (!selected.length) {
      toast.warning(t('Please select at least one channel'))
      return
    }
    fetchInFlight.current = true
    fetchMutation.mutate(
      selected.map((channel) => ({
        id: channel.id,
        name: channel.name,
        base_url: channel.base_url,
        endpoint:
          channelEndpoints[channel.id] || getDefaultEndpointForChannel(channel),
      }))
    )
  }

  const handleSelectValue = useCallback(
    (
      model: string,
      ratioType: RatioType,
      value: number | string,
      sourceName: string
    ) => {
      if (
        Object.values(differences[model] ?? {}).some(
          (field) => field?.confidence?.[sourceName] === false
        )
      ) {
        toast.warning(t('This data may be unreliable, use with caution'))
      }
      setResolutions((previous) =>
        applyResolutionSelection(previous, differences, {
          model,
          ratioType,
          value,
          sourceName,
        })
      )
    },
    [differences, t]
  )
  const handleSelectValues = useCallback(
    (selections: ResolutionSelection[]) => {
      setResolutions((previous) =>
        applyResolutionSelections(previous, differences, selections)
      )
    },
    [differences]
  )
  const handleUnselectValue = useCallback(
    (model: string, ratioType: RatioType) => {
      setResolutions((previous) =>
        deleteResolutionField(previous, model, ratioType)
      )
    },
    []
  )
  const handleUnselectValues = useCallback((plan: ResolutionRemovalPlan) => {
    setResolutions((previous) => applyResolutionRemovalPlan(previous, plan))
  }, [])

  const handleApplySync = () => {
    if (
      !pricingConfig ||
      needsRefetch ||
      saveInFlight.current ||
      fetchInFlight.current
    ) {
      return
    }
    const plan = { config: pricingConfig, resolutions, differences }
    try {
      buildUpstreamPricingUpdates(plan.config, plan.resolutions)
      const maps = parseSyncPricingMaps(plan.config)
      const conflicts: ConflictItem[] = []
      const description = (
        category: 'price' | 'ratio' | 'tiered',
        values: Record<string, number | string>
      ) => {
        if (category === 'price') {
          return `${t('Fixed price')}: ${getSyncFieldDisplayValue('model_price', values.model_price, plan.config)}`
        }
        if (category === 'tiered') {
          return `${t('Expression billing')}: ${values.billing_expr ?? '-'}`
        }
        return `${t('Model ratio')}: ${getSyncFieldDisplayValue('model_ratio', values.model_ratio ?? '-', plan.config)}\n${t('Completion ratio')}: ${values.completion_ratio ?? '-'}`
      }
      for (const [model, selected] of Object.entries(resolutions)) {
        const local = getLocalSyncBillingCategory(maps, model)
        const next =
          selected.billing_expr !== undefined
            ? 'tiered'
            : selected.model_price !== undefined
              ? 'price'
              : 'ratio'
        if (local && local !== next) {
          const values = Object.fromEntries(
            Object.entries(maps).map(([field, value]) => [field, value[model]])
          )
          const sources = Object.entries(selected).flatMap(([field, value]) => {
            const upstreams =
              differences[model]?.[field as RatioType]?.upstreams ?? {}
            return Object.entries(upstreams)
              .filter(([, candidate]) => candidate === value)
              .map(([name]) => name)
          })
          conflicts.push({
            channel: [...new Set(sources)].join(', '),
            model,
            current: description(local, values),
            newVal: description(next, selected),
          })
        }
      }
      if (conflicts.length) {
        setPendingPlan(plan)
        setConflictItems(conflicts)
        setConflictDialogOpen(true)
      } else {
        void performSync(plan)
      }
    } catch (error) {
      toast.error(t(getSettingsErrorMessage(error, t('Failed to sync prices'))))
    }
  }

  const isLoading = fetchMutation.isPending || isSaving
  const skipped = sourceResults.flatMap((result) =>
    Object.entries(result.skipped_models ?? {}).map(([model, reason]) => ({
      source: getUpstreamDisplayName(result.name),
      model,
      reason,
    }))
  )
  return (
    <div className='flex h-full min-h-0 flex-col gap-4'>
      <div className='flex shrink-0 flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
        <div className='flex flex-col gap-2 sm:flex-row'>
          <Button
            onClick={() => setChannelDialogOpen(true)}
            disabled={isLoading || conflictDialogOpen}
          >
            <RefreshCcw className='mr-2 h-4 w-4' />
            {t('Select Sync Channels')}
          </Button>
          <Button
            variant='secondary'
            onClick={handleApplySync}
            disabled={
              !Object.keys(resolutions).length ||
              isLoading ||
              needsRefetch ||
              conflictDialogOpen
            }
          >
            {syncMutation.isPending && (
              <span className='mr-2 h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent' />
            )}
            <CheckSquare className='mr-2 h-4 w-4' />
            {t('Apply Sync')}
          </Button>
        </div>
      </div>
      {needsRefetch && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Fetch upstream prices again before applying sync. Your selections have been kept.'
          )}
        </p>
      )}
      {skipped.length > 0 && (
        <details className='text-muted-foreground shrink-0 text-sm'>
          <summary className='cursor-pointer'>
            {t('Skipped pricing entries ({{count}})', {
              count: skipped.length,
            })}
          </summary>
          <p className='mt-2'>
            {t(
              'Some models were skipped because their complete pricing could not be imported. Existing prices are unchanged.'
            )}
          </p>
          <ul className='mt-2 max-h-40 overflow-y-auto break-words'>
            {skipped.map((entry) => (
              <li key={`${entry.source}:${entry.model}`}>
                {entry.source} · {entry.model}: {entry.reason}
              </li>
            ))}
          </ul>
        </details>
      )}
      <div className='min-h-0 flex-1'>
        <UpstreamRatioSyncTable
          pricingConfig={pricingConfig}
          differences={differences}
          resolutions={resolutions}
          isDisabled={isLoading || conflictDialogOpen || needsRefetch}
          isSyncing={fetchMutation.isPending}
          onSelectValue={handleSelectValue}
          onSelectValues={handleSelectValues}
          onUnselectValue={handleUnselectValue}
          onUnselectValues={handleUnselectValues}
        />
      </div>
      <ChannelSelectorDialog
        open={channelDialogOpen}
        onOpenChange={setChannelDialogOpen}
        channels={channels}
        selectedChannelIds={selectedChannelIds}
        onSelectedChannelIdsChange={setSelectedChannelIds}
        channelEndpoints={channelEndpoints}
        onChannelEndpointsChange={setChannelEndpoints}
        onConfirm={handleConfirmChannelSelection}
      />
      <ConflictConfirmDialog
        open={conflictDialogOpen}
        onOpenChange={(open) => {
          if (!saveInFlight.current) setConflictDialogOpen(open)
        }}
        conflicts={conflictItems}
        onConfirm={() => {
          if (pendingPlan) void performSync(pendingPlan)
        }}
        isLoading={isSaving}
      />
    </div>
  )
}
