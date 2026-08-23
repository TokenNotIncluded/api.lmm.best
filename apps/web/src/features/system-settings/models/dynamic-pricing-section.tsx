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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw, ShieldCheck, TriangleAlert } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import dayjs from '@/lib/dayjs'

import { getDynamicPricingStatus, updateDynamicPricingSetting } from '../api'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import type { DynamicPricingSettingUpdate } from '../types'
import { safeNumberFieldProps } from '../utils/numeric-field'

const REFRESH_INTERVAL_MS = 3_000

const schema = z.object({
  enabled: z.boolean(),
  min_factor: z.number().min(1),
  max_factor: z.number().min(1),
  base_price_usd_per_million: z.number().positive(),
  cost_floor_factor: z.number().min(1),
})

type DynamicPricingFormValues = z.infer<typeof schema>

type DynamicPricingDefaults = {
  GroupRatio: string
  'dynamic_pricing_setting.enabled': boolean
  'dynamic_pricing_setting.min_factor': number
  'dynamic_pricing_setting.base_price_usd_per_million': number
  'dynamic_pricing_setting.cost_floor_factor': number
  'dynamic_pricing_setting.max_factor': number
  'dynamic_pricing_setting.channel_costs': string
}

function parseGroupCostMultipliers(value: string): Record<string, number> {
  try {
    const parsed = JSON.parse(value) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {}
    }
    return Object.fromEntries(
      Object.entries(parsed).filter(
        (entry): entry is [string, number] =>
          typeof entry[1] === 'number' &&
          Number.isFinite(entry[1]) &&
          entry[1] >= 0 &&
          entry[0].trim() !== ''
      )
    )
  } catch {
    return {}
  }
}

function parseChannelCosts(value: string): Record<string, number> {
  try {
    const parsed = JSON.parse(value) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {}
    }
    return Object.fromEntries(
      Object.entries(parsed).filter(
        (entry): entry is [string, number] =>
          typeof entry[1] === 'number' &&
          Number.isFinite(entry[1]) &&
          entry[1] > 0
      )
    )
  } catch {
    return {}
  }
}

function buildFormValues(
  defaults: DynamicPricingDefaults
): DynamicPricingFormValues {
  return {
    enabled: defaults['dynamic_pricing_setting.enabled'],
    min_factor: defaults['dynamic_pricing_setting.min_factor'],
    max_factor: defaults['dynamic_pricing_setting.max_factor'],
    base_price_usd_per_million:
      defaults['dynamic_pricing_setting.base_price_usd_per_million'],
    cost_floor_factor: defaults['dynamic_pricing_setting.cost_floor_factor'],
  }
}

function factorText(factors: number[]) {
  if (factors.length === 0) return '1.000×'
  const minimum = Math.min(...factors)
  const maximum = Math.max(...factors)
  if (Math.abs(maximum - minimum) < 0.0005) return `${maximum.toFixed(3)}×`
  return `${minimum.toFixed(3)}× – ${maximum.toFixed(3)}×`
}

export function DynamicPricingSection({
  defaultValues,
}: {
  defaultValues: DynamicPricingDefaults
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const formDefaults = useMemo(
    () => buildFormValues(defaultValues),
    [defaultValues]
  )
  const form = useForm<DynamicPricingFormValues>({
    resolver: zodResolver(schema),
    defaultValues: formDefaults,
  })
  const [channelCosts, setChannelCosts] = useState<Record<string, string>>({})

  useEffect(() => {
    form.reset(formDefaults)
    setChannelCosts(
      Object.fromEntries(
        Object.entries(
          parseChannelCosts(
            defaultValues['dynamic_pricing_setting.channel_costs']
          )
        ).map(([id, cost]) => [id, String(cost)])
      )
    )
  }, [defaultValues, form, formDefaults])

  const statusQuery = useQuery({
    queryKey: ['dynamic-pricing-status'],
    queryFn: getDynamicPricingStatus,
    refetchInterval: REFRESH_INTERVAL_MS,
  })
  const status = statusQuery.data?.data
  const activeChannels = useMemo(
    () => status?.safety.channels ?? [],
    [status?.safety.channels]
  )
  const groupCostMultipliers = useMemo(
    () => parseGroupCostMultipliers(defaultValues.GroupRatio),
    [defaultValues.GroupRatio]
  )

  useEffect(() => {
    if (activeChannels.length === 0) {
      return
    }
    setChannelCosts((current) => {
      const next = { ...current }
      let changed = false
      for (const channel of activeChannels) {
        const key = String(channel.id)
        if (next[key] === undefined && channel.configured) {
          next[key] = String(channel.cost)
          changed = true
        }
      }
      return changed ? next : current
    })
  }, [activeChannels])

  const updateMutation = useMutation({
    mutationFn: async (request: DynamicPricingSettingUpdate) => {
      const response = await updateDynamicPricingSetting(request)
      if (!response.success) {
        throw new Error(response.message || t('Failed to update setting'))
      }
      return response
    },
    onSuccess: (response) => {
      queryClient.setQueryData(['dynamic-pricing-status'], response)
      queryClient.invalidateQueries({ queryKey: ['system-options'] })
      toast.success(t('Setting updated successfully'))
    },
    onError: (error: Error) => {
      toast.error(error.message || t('Failed to update setting'))
    },
  })

  const onSubmit = async (values: DynamicPricingFormValues) => {
    if (values.min_factor > values.max_factor) {
      toast.error(t('Minimum multiplier cannot exceed the dynamic ceiling.'))
      return
    }
    const normalizedCosts = parseChannelCosts(
      JSON.stringify(
        Object.fromEntries(
          Object.entries(channelCosts)
            .filter(([, raw]) => raw.trim() !== '')
            .map(([id, raw]) => [id, Number(raw)])
        )
      )
    )
    const invalidChannel = activeChannels.find((channel) => {
      const raw = channelCosts[String(channel.id)]?.trim() ?? ''
      return raw !== '' && (!Number.isFinite(Number(raw)) || Number(raw) <= 0)
    })
    if (invalidChannel) {
      toast.error(
        t('Enter a positive conservative cost for channel {{channel}}.', {
          channel: invalidChannel.name,
        })
      )
      return
    }
    const missingChannels = activeChannels.filter(
      (channel) => normalizedCosts[String(channel.id)] === undefined
    )
    if (values.enabled && missingChannels.length > 0) {
      toast.error(
        t(
          'Configure costs for every active channel before enabling: {{channels}}',
          {
            channels: missingChannels.map((channel) => channel.name).join(', '),
          }
        )
      )
      return
    }

    await updateMutation.mutateAsync({
      ...values,
      channel_costs: normalizedCosts,
    })
  }

  const enabled = form.watch('enabled')
  const modelEntries = Object.entries(status?.models ?? {}).sort(([a], [b]) =>
    a.localeCompare(b)
  )
  let currentFactors = [1]
  if (status?.enabled) {
    if (modelEntries.length > 0) {
      currentFactors = modelEntries.flatMap(([, model]) => [
        model.request_factor_min,
        model.request_factor_max,
      ])
    } else {
      currentFactors = [status.preview_factor]
    }
  }
  const safetyReady = status?.safety.ready ?? false
  const missingChannels = status?.safety.missing_channels ?? []
  const livePricingEnabled = status?.enabled === true

  return (
    <SettingsSection title={t('Dynamic Profit Pricing')}>
      <p className='text-muted-foreground mb-4 text-sm'>
        {t(
          'Group Pricing stores the base cost multiplier. This page computes the live profit multiplier on top of that cost.'
        )}
      </p>
      <div className='grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(20rem,0.7fr)]'>
        <Form {...form}>
          <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
            <SettingsPageFormActions
              onSave={form.handleSubmit(onSubmit)}
              isSaving={updateMutation.isPending}
            />

            <FormField
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Enable dynamic profit pricing')}</FormLabel>
                    <FormDescription>
                      {t(
                        'The final charge is the group cost multiplier multiplied by this dynamic profit multiplier.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />

            <FormField
              control={form.control}
              name='min_factor'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Minimum profit multiplier')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      step={0.01}
                      {...safeNumberFieldProps(field)}
                      disabled={!enabled}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'The profit multiplier never falls below this value while dynamic pricing is enabled.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='max_factor'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Dynamic profit ceiling')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      step={0.01}
                      {...safeNumberFieldProps(field)}
                      disabled={!enabled}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Caps the load-driven profit premium. Cost protection can still raise the effective multiplier when needed.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='base_price_usd_per_million'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {t('Reference model cost (USD / 1M tokens)')}
                  </FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0.000001}
                      step='any'
                      {...safeNumberFieldProps(field)}
                      disabled={!enabled}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Use the model cost baseline used to compare upstream cost with the configured group cost multiplier.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='cost_floor_factor'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Cost protection margin')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      step={0.01}
                      {...safeNumberFieldProps(field)}
                      disabled={!enabled}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Known upstream cost is multiplied by this margin before the cost floor is compared with profit pricing.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </SettingsForm>
        </Form>

        <Card className='self-start'>
          <CardHeader>
            <CardTitle>{t('Live profit multiplier preview')}</CardTitle>
            <CardDescription>
              {t('Automatically refreshes every 3 seconds.')}
            </CardDescription>
            <CardAction className='flex items-center gap-2'>
              <Badge variant={status?.enabled ? 'default' : 'outline'}>
                {status?.enabled ? t('Enabled') : t('Disabled')}
              </Badge>
              <Button
                type='button'
                size='icon-sm'
                variant='ghost'
                aria-label={t('Refresh')}
                onClick={() => statusQuery.refetch()}
                disabled={statusQuery.isFetching}
              >
                <RefreshCw
                  className={
                    statusQuery.isFetching ? 'animate-spin' : undefined
                  }
                />
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent className='space-y-4'>
            <div>
              <div className='text-muted-foreground text-xs font-medium tracking-wide uppercase'>
                {t('Current dynamic profit multiplier')}
              </div>
              <div className='mt-1 text-3xl font-semibold tabular-nums'>
                {factorText(currentFactors)}
              </div>
            </div>
            {safetyReady ? (
              <Alert>
                <ShieldCheck />
                <AlertTitle>{t('Configured-cost coverage ready')}</AlertTitle>
                <AlertDescription>
                  {t(
                    '{{configured}} of {{active}} active channels have costs.',
                    {
                      configured: status?.safety.configured_channel_count ?? 0,
                      active: status?.safety.active_channel_count ?? 0,
                    }
                  )}
                </AlertDescription>
              </Alert>
            ) : (
              <Alert variant='destructive'>
                <TriangleAlert />
                <AlertTitle>{t('Not ready to enable safely')}</AlertTitle>
                <AlertDescription>
                  {missingChannels.length > 0
                    ? t(
                        'Configure costs for every active channel before enabling: {{channels}}',
                        {
                          channels: missingChannels
                            .map((channel) => channel.name)
                            .join(', '),
                        }
                      )
                    : status?.safety.reason ||
                      t('Live safety status is currently unavailable.')}
                </AlertDescription>
              </Alert>
            )}
          </CardContent>
        </Card>
      </div>

      <Card className='bg-muted/20 shadow-none'>
        <CardHeader>
          <CardTitle>{t('Cost × profit pricing preview')}</CardTitle>
          <CardDescription>
            {t(
              'Group Pricing supplies the cost multiplier. Dynamic pricing supplies the profit multiplier. Final billing multiplies both.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='space-y-4'>
          <div className='bg-background rounded-md border px-3 py-2 text-sm'>
            <span className='text-muted-foreground'>{t('Formula')}</span>{' '}
            <span className='font-medium'>
              {t('Final billing = group cost × dynamic profit')}
            </span>
          </div>
          <div className='overflow-x-auto rounded-lg border'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Pricing group')}</TableHead>
                  <TableHead>{t('Cost multiplier')}</TableHead>
                  <TableHead>{t('Profit multiplier')}</TableHead>
                  <TableHead>{t('Effective billing multiplier')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {Object.entries(groupCostMultipliers).length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={4}
                      className='text-muted-foreground text-center'
                    >
                      {t('No pricing groups configured')}
                    </TableCell>
                  </TableRow>
                ) : (
                  Object.entries(groupCostMultipliers)
                    .sort(([left], [right]) => left.localeCompare(right))
                    .map(([group, costMultiplier]) => {
                      const profitMultiplier = status?.enabled
                        ? status.preview_factor
                        : 1
                      return (
                        <TableRow key={group}>
                          <TableCell className='font-medium'>{group}</TableCell>
                          <TableCell className='tabular-nums'>
                            {costMultiplier.toFixed(3)}×
                          </TableCell>
                          <TableCell className='tabular-nums'>
                            {profitMultiplier.toFixed(3)}×
                          </TableCell>
                          <TableCell className='font-semibold tabular-nums'>
                            {(costMultiplier * profitMultiplier).toFixed(3)}×
                          </TableCell>
                        </TableRow>
                      )
                    })
                )}
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>

      <div className='space-y-2'>
        <div>
          <h4 className='text-sm font-medium'>
            {t('Conservative channel costs')}
          </h4>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Enter a conservative upper-bound USD cost per 1M total tokens. Upstream responses provide usage tokens, but generally not the final dollar cost; unknown-cost channels are blocked while this feature is enabled.'
            )}{' '}
            {t('Fill every active channel cost first, then enable.')}
          </p>
        </div>
        <div className='rounded-lg border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Active channel')}</TableHead>
                <TableHead className='w-56'>{t('USD / 1M tokens')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {activeChannels.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={2}
                    className='text-muted-foreground text-center'
                  >
                    {statusQuery.isLoading
                      ? t('Loading...')
                      : t('No active channels found')}
                  </TableCell>
                </TableRow>
              ) : (
                activeChannels.map((channel) => (
                  <TableRow key={channel.id}>
                    <TableCell>
                      <div className='font-medium'>{channel.name}</div>
                      <div className='text-muted-foreground text-xs'>
                        ID {channel.id}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Input
                        type='number'
                        min={0.000001}
                        step='any'
                        value={channelCosts[String(channel.id)] ?? ''}
                        onChange={(event) =>
                          setChannelCosts((current) => ({
                            ...current,
                            [String(channel.id)]: event.target.value,
                          }))
                        }
                        aria-label={t('Cost for {{channel}}', {
                          channel: channel.name,
                        })}
                      />
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      <div className='space-y-2'>
        <div className='flex items-center justify-between gap-2'>
          <h4 className='text-sm font-medium'>{t('Per-model live factors')}</h4>
          {status?.setting.tick_interval_seconds ? (
            <span className='text-muted-foreground text-xs'>
              {t('Engine tick: {{seconds}}s', {
                seconds: status.setting.tick_interval_seconds,
              })}
            </span>
          ) : null}
        </div>
        <div className='rounded-lg border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Model')}</TableHead>
                <TableHead>{t('Profit multiplier')}</TableHead>
                <TableHead>{t('Cost floor')}</TableHead>
                <TableHead>{t('Load EMA')}</TableHead>
                <TableHead>{t('Cost EMA')}</TableHead>
                <TableHead>{t('Updated')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {modelEntries.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={6}
                    className='text-muted-foreground text-center'
                  >
                    {t(
                      'No model samples yet. The configured minimum is used until the first engine tick.'
                    )}
                  </TableCell>
                </TableRow>
              ) : (
                modelEntries.map(([modelName, model]) => (
                  <TableRow key={modelName}>
                    <TableCell className='max-w-72 truncate font-medium'>
                      {modelName}
                      {model.has_unpriced_traffic ? (
                        <Badge variant='destructive' className='ml-2'>
                          {t('Unknown cost')}
                        </Badge>
                      ) : null}
                    </TableCell>
                    <TableCell className='font-medium'>
                      {livePricingEnabled
                        ? factorText([
                            model.request_factor_min,
                            model.request_factor_max,
                          ])
                        : '—'}
                    </TableCell>
                    <TableCell>
                      {livePricingEnabled
                        ? `${model.hard_cost_floor.toFixed(3)}×`
                        : '—'}
                    </TableCell>
                    <TableCell>
                      {livePricingEnabled ? model.load_ema.toFixed(3) : '—'}
                    </TableCell>
                    <TableCell>
                      {livePricingEnabled
                        ? `$${model.cost_ema.toFixed(4)}`
                        : '—'}
                    </TableCell>
                    <TableCell className='text-muted-foreground'>
                      {livePricingEnabled && model.updated_at > 0
                        ? dayjs(model.updated_at * 1000).format('HH:mm:ss')
                        : '—'}
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </div>
    </SettingsSection>
  )
}
