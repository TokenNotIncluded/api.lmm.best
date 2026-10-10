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
import { RefreshCw } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { summarizeAsyncLogs } from '../lib/async-task-logs'
import { useTaskLogsTranslation } from '../task-logs-i18n'

interface TaskLogsStatusProps {
  rows: readonly unknown[]
  page: number
  total: number
  autoRefresh: boolean
  onAutoRefreshChange: (enabled: boolean) => void
  onRefresh: () => void
  isFetching: boolean
  updatedAt: number
}

export function TaskLogsStatus({
  rows,
  page,
  total,
  autoRefresh,
  onAutoRefreshChange,
  onRefresh,
  isFetching,
  updatedAt,
}: TaskLogsStatusProps) {
  const { t, i18n } = useTaskLogsTranslation()
  const language = i18n.resolvedLanguage || i18n.language
  const locale =
    language === 'zhCN' ? 'zh-CN' : language === 'zhTW' ? 'zh-TW' : language
  let updatedTime = ''
  if (updatedAt > 0) {
    try {
      updatedTime = new Date(updatedAt).toLocaleTimeString(locale)
    } catch {
      updatedTime = new Date(updatedAt).toLocaleTimeString()
    }
  }
  const summary = summarizeAsyncLogs(rows)
  const counts = [
    ['Queued', summary.queued],
    ['In Progress', summary.running],
    ['Success', summary.success],
    ['Failed', summary.failed],
    ['Other', summary.other],
  ] as const

  return (
    <div className='flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 text-xs'>
      <div className='text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 tabular-nums'>
        <span>
          {t('Total')}: {total}
        </span>
        <span>
          {t('This page only')} · {page}:
        </span>
        {counts.map(
          ([label, count]) =>
            count > 0 && (
              <span key={label}>
                {t(label)}{' '}
                <strong
                  className={cn(
                    'text-foreground font-medium',
                    label === 'Failed' && 'console-status-danger-text'
                  )}
                >
                  {count}
                </strong>
              </span>
            )
        )}
      </div>
      <div className='flex flex-wrap items-center gap-2'>
        {updatedAt > 0 && (
          <time
            className='text-muted-foreground tabular-nums'
            dateTime={new Date(updatedAt).toISOString()}
          >
            {t('Last updated')}: {updatedTime}
          </time>
        )}
        <Button
          type='button'
          variant={autoRefresh ? 'secondary' : 'ghost'}
          size='sm'
          aria-pressed={autoRefresh}
          title={t(
            'Refreshes every 5 seconds while listed tasks are pending. Pauses in the background.'
          )}
          onClick={() => onAutoRefreshChange(!autoRefresh)}
        >
          {t('Auto refresh')}
        </Button>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          onClick={onRefresh}
          disabled={isFetching}
        >
          <RefreshCw
            className={cn('size-3.5', isFetching && 'motion-safe:animate-spin')}
          />
          {t('Refresh')}
        </Button>
      </div>
    </div>
  )
}
