/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  CircleAlert,
  CirclePause,
  RefreshCw,
  TimerReset,
  Wallet,
} from 'lucide-react'
import { motion, useReducedMotion } from 'motion/react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { MOTION_TRANSITION } from '@/lib/motion'
import { cn } from '@/lib/utils'

import { getChannelOps, getChannels } from '../api'
import { channelsQueryKeys } from '../lib'
import { summarizeChannelHealth } from '../lib/channel-health'
import { useChannels } from './channels-provider'

/**
 * Sample size for the fleet health snapshot. This is a display-only gauge,
 * not a data source for any decision the table itself makes, so a bounded
 * sample (instead of paging through every channel) keeps it cheap while
 * still being representative for typical fleets.
 */
const HEALTH_SAMPLE_SIZE = 100

const healthQueryKey = channelsQueryKeys.list({
  scope: 'health-strip',
  page_size: HEALTH_SAMPLE_SIZE,
})

/**
 * Compact "fleet health" strip shown above the channels table: a share of
 * enabled channels plus the counts that most often need attention (never
 * tested, slow, zero balance). Purely informational — it never filters the
 * table or changes any request semantics.
 */
export function ChannelsHealthStrip() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { sensitiveVisible } = useChannels()
  const prefersReducedMotion = useReducedMotion()
  const [isRefreshing, setIsRefreshing] = useState(false)

  const sampleQuery = useQuery({
    queryKey: healthQueryKey,
    queryFn: () =>
      getChannels({ p: 1, page_size: HEALTH_SAMPLE_SIZE, id_sort: true }),
    staleTime: 60_000,
  })

  const opsQuery = useQuery({
    queryKey: ['channel-ops'],
    queryFn: getChannelOps,
    retry: false,
    staleTime: 5 * 60 * 1000,
  })

  const items = sampleQuery.data?.data?.items ?? []
  const total = sampleQuery.data?.data?.total ?? items.length
  const health = summarizeChannelHealth(items)
  const retryTimes = opsQuery.data?.data?.retry_times

  if (sampleQuery.isLoading || health.sampled === 0) {
    return null
  }

  const handleRefresh = async () => {
    setIsRefreshing(true)
    try {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: healthQueryKey }),
        queryClient.invalidateQueries({ queryKey: ['channel-ops'] }),
      ])
    } finally {
      setIsRefreshing(false)
    }
  }

  const isSampled = health.sampled < total
  const healthPercent = health.healthPercent ?? 0

  return (
    <div className='bg-card/50 flex flex-wrap items-center gap-x-5 gap-y-2 rounded-xl border px-3.5 py-2.5 text-sm'>
      <Tooltip>
        <TooltipTrigger render={<div className='flex items-center gap-2' />}>
          <div
            className='relative size-9 shrink-0 rounded-full'
            style={{
              background: `conic-gradient(var(--success) ${healthPercent}%, var(--muted) ${healthPercent}% 100%)`,
            }}
            role='img'
            aria-label={t('{{value}}% of sampled channels are enabled', {
              value: healthPercent,
            })}
          >
            <div className='bg-card absolute inset-[3px] flex items-center justify-center rounded-full text-[11px] font-semibold tabular-nums'>
              {healthPercent}%
            </div>
          </div>
          <div className='leading-tight'>
            <div className='font-medium'>{t('Fleet health')}</div>
            <div className='text-muted-foreground text-xs'>
              {isSampled
                ? t('{{sampled}} of {{total}} channels sampled', {
                    sampled: health.sampled,
                    total,
                  })
                : t('{{count}} channels', { count: health.sampled })}
            </div>
          </div>
        </TooltipTrigger>
        <TooltipContent side='bottom' className='max-w-xs'>
          {t(
            'Enabled vs. disabled/unknown, from a snapshot of the fleet. Refresh to resample.'
          )}
        </TooltipContent>
      </Tooltip>

      <div className='flex flex-wrap items-center gap-3 text-xs'>
        {health.neverTested > 0 && (
          <span className='text-muted-foreground flex items-center gap-1'>
            <CirclePause className='size-3.5' aria-hidden='true' />
            {t('{{count}} never tested', { count: health.neverTested })}
          </span>
        )}
        {health.slowOverThreshold > 0 && (
          <span className='flex items-center gap-1 text-(--warning)'>
            <TimerReset className='size-3.5' aria-hidden='true' />
            {t('{{count}} slow (>5s)', { count: health.slowOverThreshold })}
          </span>
        )}
        {health.zeroBalance > 0 && (
          <span className='flex items-center gap-1 text-(--destructive)'>
            <Wallet className='size-3.5' aria-hidden='true' />
            {sensitiveVisible
              ? t('{{count}} at zero balance', { count: health.zeroBalance })
              : t('Some at zero balance')}
          </span>
        )}
        {health.autoDisabled > 0 && (
          <span className='flex items-center gap-1 text-(--warning)'>
            <CircleAlert className='size-3.5' aria-hidden='true' />
            {t('{{count}} auto-disabled', { count: health.autoDisabled })}
          </span>
        )}
        {health.averageResponseTimeMs != null && (
          <span className='text-muted-foreground'>
            {t('Avg response')}: {health.averageResponseTimeMs}ms
          </span>
        )}
        {typeof retryTimes === 'number' && (
          <span className='text-muted-foreground'>
            {t('Max Retries')}: {retryTimes}
          </span>
        )}
      </div>

      <Tooltip>
        <TooltipTrigger
          render={
            <button
              type='button'
              onClick={handleRefresh}
              disabled={isRefreshing || sampleQuery.isFetching}
              aria-label={t('Refresh fleet health')}
              className='text-muted-foreground hover:text-foreground focus-visible:outline-ring ms-auto flex size-11 shrink-0 items-center justify-center rounded-md focus-visible:outline-2 focus-visible:outline-offset-2 disabled:opacity-50'
            />
          }
        >
          <motion.span
            animate={
              (isRefreshing || sampleQuery.isFetching) && !prefersReducedMotion
                ? { rotate: 360 }
                : { rotate: 0 }
            }
            transition={
              (isRefreshing || sampleQuery.isFetching) && !prefersReducedMotion
                ? { repeat: Infinity, duration: 0.7, ease: 'linear' }
                : MOTION_TRANSITION.fast
            }
            className={cn('inline-flex')}
          >
            <RefreshCw className='size-4' />
          </motion.span>
        </TooltipTrigger>
        <TooltipContent side='bottom'>
          {t('Refresh fleet health')}
        </TooltipContent>
      </Tooltip>
    </div>
  )
}
