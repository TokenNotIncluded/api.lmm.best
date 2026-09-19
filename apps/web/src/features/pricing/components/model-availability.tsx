/*
Copyright (C) 2026 LIghtJUNction

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
*/
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { getPerfMetrics } from '@/features/performance-metrics/api'
import { useAuthStore } from '@/stores/auth-store'

import { getRecentModelObservation } from '../lib/model-availability'
import { getAvailableGroups } from '../lib/model-helpers'
import type { PricingModel } from '../types'

export function ModelAvailability({
  model,
  usableGroup,
}: {
  model: PricingModel
  usableGroup: Record<string, { desc: string; ratio: number }>
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const metrics = useQuery({
    queryKey: ['perf-metrics', model.model_name],
    queryFn: () => getPerfMetrics(model.model_name, 24),
    staleTime: 60_000,
    refetchInterval: 60_000,
    retry: false,
  })
  const observation = getRecentModelObservation(
    metrics.data?.data?.groups ?? [],
    metrics.dataUpdatedAt
  )
  const status = metrics.isLoading
    ? t('Loading status')
    : metrics.isError || metrics.data?.success === false
      ? t('Model status could not be loaded')
      : observation.state === 'successful'
        ? t('Recent calls succeeded')
        : observation.state === 'failures'
          ? t('Recent calls include failures')
          : t('No recent model status')
  const groups = getAvailableGroups(model, usableGroup)
  const access = !user
    ? t('Sign in to check model access')
    : user.developer_access_granted !== true
      ? t('API access is required')
      : groups.length > 0
        ? t('Your account has an eligible model group')
        : t('No eligible model group for this account')
  return (
    <section
      className='space-y-2 border-b pb-4'
      aria-label={t('Model availability')}
    >
      <p className='text-sm font-medium'>{access}</p>
      {(!user || user.developer_access_granted !== true) && (
        <a
          className='text-primary text-sm underline underline-offset-4'
          href='/getting-started'
        >
          {t('Getting started')}
        </a>
      )}
      <p className='text-sm' role='status'>
        {status}
      </p>
      {observation.latestAt && (
        <p className='text-muted-foreground text-xs'>
          {t('Latest observed request interval')}:{' '}
          <time dateTime={new Date(observation.latestAt * 1000).toISOString()}>
            {new Date(observation.latestAt * 1000).toLocaleString()}
          </time>
        </p>
      )}
      <p className='text-muted-foreground text-xs'>
        {t(
          'Recent means within one hour. Historical calls across groups do not guarantee your next request; key restrictions and balance still apply.'
        )}
      </p>
      {(metrics.isError || metrics.data?.success === false) && (
        <Button
          size='sm'
          variant='outline'
          onClick={() => void metrics.refetch()}
          disabled={metrics.isFetching}
        >
          {t('Retry')}
        </Button>
      )}
    </section>
  )
}
