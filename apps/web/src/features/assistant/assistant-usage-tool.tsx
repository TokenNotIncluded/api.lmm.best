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
import {
  Alert02Icon,
  ArrowRight01Icon,
  ChartIncreaseIcon,
  ReloadIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Progress,
  ProgressLabel,
  ProgressValue,
} from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import { formatCreditBalance as formatCreditBalanceBase } from '@/features/wallet/lib/format'
import { toIntlLocale } from '@/i18n/languages'
import { getCurrencyDisplay } from '@/lib/currency'

import { getAssistantUsageData } from './api'
import { summarizeAssistantUsage } from './usage-summary'

type UsageDays = 7 | 30 | 90

const USAGE_DAY_OPTIONS: UsageDays[] = [7, 30, 90]

export function AssistantUsageTool(props: { developerAccessGranted: boolean }) {
  const { t, i18n } = useTranslation()
  const formatCreditBalance = (amount: number) =>
    formatCreditBalanceBase(amount, t('Platform'))
  const [days, setDays] = useState<UsageDays>(30)
  const usageQuery = useQuery({
    queryKey: ['assistant-usage', days],
    queryFn: () => getAssistantUsageData(days),
    enabled: props.developerAccessGranted,
    staleTime: 60_000,
    retry: false,
  })
  const quotaPerUnit = getCurrencyDisplay().config.quotaPerUnit
  const summary = useMemo(
    () => summarizeAssistantUsage(usageQuery.data ?? [], quotaPerUnit),
    [quotaPerUnit, usageQuery.data]
  )
  const compactNumber = useMemo(
    () =>
      new Intl.NumberFormat(toIntlLocale(i18n.language), {
        notation: 'compact',
        maximumFractionDigits: 1,
      }),
    [i18n.language]
  )

  if (!props.developerAccessGranted) {
    return (
      <Alert>
        <HugeiconsIcon
          icon={ChartIncreaseIcon}
          strokeWidth={2}
          aria-hidden='true'
        />
        <AlertTitle>{t('Historical Usage')}</AlertTitle>
        <AlertDescription>
          {t(
            'Current usage rates and available amounts are shown after access is activated.'
          )}
        </AlertDescription>
        <AlertAction>
          <Button
            variant='outline'
            size='sm'
            render={<Link to='/getting-started' />}
          >
            {t('Choose an activation path')}
            <HugeiconsIcon
              icon={ArrowRight01Icon}
              strokeWidth={2}
              data-icon='inline-end'
              aria-hidden='true'
            />
          </Button>
        </AlertAction>
      </Alert>
    )
  }

  let content = (
    <div className='grid gap-4'>
      <div className='grid grid-cols-3 gap-2'>
        <div className='bg-muted/40 min-w-0 rounded-lg p-3'>
          <p className='text-muted-foreground truncate text-xs'>
            {t('Total requests made')}
          </p>
          <p className='mt-1 font-mono text-lg font-semibold tabular-nums'>
            {compactNumber.format(summary.requests)}
          </p>
        </div>
        <div className='bg-muted/40 min-w-0 rounded-lg p-3'>
          <p className='text-muted-foreground truncate text-xs'>
            {t('Total tokens')}
          </p>
          <p className='mt-1 font-mono text-lg font-semibold tabular-nums'>
            {compactNumber.format(summary.tokens)}
          </p>
        </div>
        <div className='bg-muted/40 min-w-0 rounded-lg p-3'>
          <p className='text-muted-foreground truncate text-xs'>
            {t('Total consumed')}
          </p>
          <p className='mt-1 truncate font-mono text-sm font-semibold tabular-nums'>
            {formatCreditBalance(summary.creditUSD)}
          </p>
        </div>
      </div>

      <section className='grid gap-3' aria-labelledby='assistant-top-models'>
        <h3 id='assistant-top-models' className='text-sm font-medium'>
          {t('Top models')}
        </h3>
        <div className='grid gap-3'>
          {summary.models.slice(0, 5).map((model) => (
            <div key={model.model || 'unknown'} className='grid gap-1.5'>
              <Progress value={model.sharePercent}>
                <ProgressLabel className='max-w-64 truncate'>
                  {model.model || t('Unknown')}
                </ProgressLabel>
                <ProgressValue>
                  {() =>
                    `${new Intl.NumberFormat(toIntlLocale(i18n.language), {
                      maximumFractionDigits: 1,
                    }).format(model.sharePercent)}%`
                  }
                </ProgressValue>
              </Progress>
              <p className='text-muted-foreground text-xs'>
                {formatCreditBalance(model.creditUSD)} ·{' '}
                {compactNumber.format(model.requests)} {t('Requests')}
              </p>
            </div>
          ))}
        </div>
      </section>
    </div>
  )

  if (usageQuery.isLoading) {
    content = (
      <div className='grid gap-3' aria-label={t('Loading...')}>
        <div className='grid grid-cols-3 gap-2'>
          <Skeleton className='h-20' />
          <Skeleton className='h-20' />
          <Skeleton className='h-20' />
        </div>
        <Skeleton className='h-28' />
      </div>
    )
  } else if (usageQuery.isError) {
    content = (
      <Alert variant='destructive'>
        <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} aria-hidden='true' />
        <AlertTitle>{t('Failed to fetch usage')}</AlertTitle>
        <AlertDescription>
          {t('Detailed request logs for investigations.')}
        </AlertDescription>
        <AlertAction>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void usageQuery.refetch()}
          >
            <HugeiconsIcon
              icon={ReloadIcon}
              strokeWidth={2}
              data-icon='inline-start'
              aria-hidden='true'
            />
            {t('Retry')}
          </Button>
        </AlertAction>
      </Alert>
    )
  } else if (summary.requests === 0 && summary.tokens === 0) {
    content = (
      <Empty className='min-h-36 border'>
        <EmptyHeader>
          <EmptyMedia variant='icon'>
            <HugeiconsIcon
              icon={ChartIncreaseIcon}
              strokeWidth={2}
              aria-hidden='true'
            />
          </EmptyMedia>
          <EmptyTitle>{t('No recent usage')}</EmptyTitle>
          <EmptyDescription>
            {t(
              'No usage logs available. Logs will appear here once API calls are made.'
            )}
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <Card size='sm'>
      <CardHeader>
        <div className='flex items-start justify-between gap-3'>
          <div className='min-w-0'>
            <CardTitle className='flex items-center gap-2'>
              <HugeiconsIcon
                icon={ChartIncreaseIcon}
                className='size-4'
                strokeWidth={2}
                aria-hidden='true'
              />
              {t('Usage at a glance')}
            </CardTitle>
            <CardDescription>
              {t('Updated from live usage data')}
            </CardDescription>
          </div>
          <NativeSelect
            size='sm'
            value={String(days)}
            aria-label={t('Historical Usage')}
            onChange={(event) =>
              setDays(Number(event.target.value) as UsageDays)
            }
          >
            {USAGE_DAY_OPTIONS.map((option) => (
              <NativeSelectOption key={option} value={option}>
                {t('{{days}} days', { days: option })}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
      </CardHeader>
      <CardContent className='grid gap-4'>
        {content}
        <Button variant='outline' render={<Link to='/usage-logs' />}>
          {t('Open usage statistics')}
          <HugeiconsIcon
            icon={ArrowRight01Icon}
            strokeWidth={2}
            data-icon='inline-end'
            aria-hidden='true'
          />
        </Button>
      </CardContent>
    </Card>
  )
}
