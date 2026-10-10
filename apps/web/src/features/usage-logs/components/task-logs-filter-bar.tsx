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
import { useIsFetching, useQueryClient } from '@tanstack/react-query'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import type { Table } from '@tanstack/react-table'
import { useId, useState, type KeyboardEvent, type ReactNode } from 'react'
import { toast } from 'sonner'

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import {
  MJ_STATUS_MAPPINGS,
  MJ_TASK_TYPE_MAPPINGS,
  TASK_ACTION_MAPPINGS,
  TASK_PLATFORMS,
  TASK_STATUS_MAPPINGS,
} from '../constants'
import { buildSearchParams } from '../lib/filter'
import { useTaskLogsTranslation } from '../task-logs-i18n'
import type { DrawingLogFilters, LogCategory, TaskLogFilters } from '../types'
import { CompactDateTimeRangePicker } from './compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from './logs-filter-toolbar'
import { useLogsViewScope } from './usage-logs-provider'

const route = getRouteApi('/_authenticated/usage-logs/$section')
type TaskLikeLogCategory = Extract<LogCategory, 'drawing' | 'task'>
type TaskLogsFilters = DrawingLogFilters | TaskLogFilters

interface TaskLogsFilterBarProps<TData> {
  table: Table<TData>
  logCategory: TaskLikeLogCategory
  stats?: ReactNode
}

function date(value: unknown): Date | undefined {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
    ? new Date(value)
    : undefined
}

export function TaskLogsFilterBar<TData>({
  table,
  logCategory,
  stats,
}: TaskLogsFilterBarProps<TData>) {
  const { t } = useTaskLogsTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const searchParams = route.useSearch()
  const { isAdminView: isAdmin } = useLogsViewScope()
  const fetchingLogs = useIsFetching({
    queryKey: ['logs', logCategory, isAdmin],
  })
  const actionListId = useId()
  const platformListId = useId()

  // No implicit date range: users must be able to find older jobs by ID.
  const currentFilters: TaskLogsFilters = {
    startTime: date(searchParams.startTime),
    endTime: date(searchParams.endTime),
    status: searchParams.status || '',
    action: searchParams.action || '',
    channel: isAdmin ? searchParams.channel || '' : '',
    ...(logCategory === 'drawing'
      ? { mjId: searchParams.filter || '' }
      : {
          taskId: searchParams.filter || '',
          platform: searchParams.platform || '',
        }),
  }
  const sourceKey = JSON.stringify([logCategory, isAdmin, currentFilters])
  const [draft, setDraft] = useState({ sourceKey, filters: currentFilters })
  const filters = draft.sourceKey === sourceKey ? draft.filters : currentFilters

  const change = (
    field: keyof (DrawingLogFilters & TaskLogFilters),
    value: Date | string | undefined
  ) => {
    setDraft({ sourceKey, filters: { ...filters, [field]: value } })
  }

  const apply = () => {
    if (
      filters.startTime &&
      filters.endTime &&
      filters.startTime > filters.endTime
    ) {
      toast.error(t('Start time must be before end time.'))
      return
    }
    const filterParams = buildSearchParams(
      { ...filters, channel: isAdmin ? filters.channel : undefined },
      logCategory
    )
    const sameFilters =
      JSON.stringify(filterParams) ===
      JSON.stringify(buildSearchParams(currentFilters, logCategory))
    void navigate({
      to: '/usage-logs/$section',
      params: { section: logCategory },
      search: { ...filterParams, page: 1, pageSize: searchParams.pageSize },
    })
    // Changed filters already produce a new query. Avoid fetching the old page.
    if (sameFilters && (searchParams.page ?? 1) === 1) {
      void queryClient.invalidateQueries({
        queryKey: ['logs', logCategory, isAdmin],
      })
    }
  }

  const reset = () => {
    setDraft({ sourceKey, filters: {} })
    void navigate({
      to: '/usage-logs/$section',
      params: { section: logCategory },
      search: { page: 1, pageSize: searchParams.pageSize },
    })
  }

  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'Enter') apply()
  }
  const filterValue =
    logCategory === 'drawing'
      ? (filters as DrawingLogFilters).mjId || ''
      : (filters as TaskLogFilters).taskId || ''
  const platform =
    logCategory === 'task' ? (filters as TaskLogFilters).platform || '' : ''
  const statusMappings =
    logCategory === 'drawing' ? MJ_STATUS_MAPPINGS : TASK_STATUS_MAPPINGS
  const actionMappings =
    logCategory === 'drawing' ? MJ_TASK_TYPE_MAPPINGS : TASK_ACTION_MAPPINGS
  const additionalFilters = [
    filterValue,
    filters.status,
    filters.action,
    platform,
    filters.channel,
  ]
  const hasFilters =
    additionalFilters.some(Boolean) || !!filters.startTime || !!filters.endTime

  const dateRangeFilter = (
    <LogsFilterField wide>
      <CompactDateTimeRangePicker
        start={filters.startTime}
        end={filters.endTime}
        onChange={({ start, end }) => {
          setDraft({
            sourceKey,
            filters: { ...filters, startTime: start, endTime: end },
          })
        }}
      />
    </LogsFilterField>
  )
  const taskIdFilter = (
    <LogsFilterField>
      <LogsFilterInput
        aria-label={t('Task ID')}
        placeholder={t(
          logCategory === 'drawing'
            ? 'Filter by MjProxy task ID'
            : 'Filter by task ID'
        )}
        value={filterValue}
        onChange={(event) =>
          change(
            logCategory === 'drawing' ? 'mjId' : 'taskId',
            event.target.value
          )
        }
        onKeyDown={onKeyDown}
      />
    </LogsFilterField>
  )
  const statusFilter = (
    <LogsFilterField>
      <Select
        value={filters.status || 'all'}
        onValueChange={(value) =>
          change('status', value === 'all' ? '' : (value ?? ''))
        }
      >
        <SelectTrigger aria-label={t('Status')} className='h-8'>
          <SelectValue>
            {filters.status
              ? t(statusMappings[filters.status]?.label || filters.status)
              : `${t('Status')}: ${t('All')}`}
          </SelectValue>
        </SelectTrigger>
        <SelectContent>
          <SelectItem value='all'>{t('All')}</SelectItem>
          {Object.entries(statusMappings).map(([value, mapping]) => (
            <SelectItem key={value} value={value}>
              {t(mapping.label)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </LogsFilterField>
  )
  const advancedFilters = (
    <>
      <LogsFilterField>
        <LogsFilterInput
          aria-label={t('Action')}
          placeholder={t('Action')}
          list={actionListId}
          value={filters.action || ''}
          onChange={(event) => change('action', event.target.value)}
          onKeyDown={onKeyDown}
        />
        <datalist id={actionListId}>
          {Object.entries(actionMappings).map(([value, mapping]) => (
            <option key={value} value={value}>
              {t(mapping.label)}
            </option>
          ))}
        </datalist>
      </LogsFilterField>
      {logCategory === 'task' && (
        <LogsFilterField>
          <LogsFilterInput
            aria-label={t('Platform')}
            placeholder={t('Platform')}
            list={platformListId}
            value={platform}
            onChange={(event) => change('platform', event.target.value)}
            onKeyDown={onKeyDown}
          />
          <datalist id={platformListId}>
            {Object.values(TASK_PLATFORMS).map((value) => (
              <option key={value} value={value} />
            ))}
          </datalist>
        </LogsFilterField>
      )}
      {isAdmin && (
        <LogsFilterField>
          <LogsFilterInput
            aria-label={t('Channel ID')}
            placeholder={t('Channel ID')}
            value={filters.channel || ''}
            onChange={(event) => change('channel', event.target.value)}
            onKeyDown={onKeyDown}
          />
        </LogsFilterField>
      )}
    </>
  )

  return (
    <LogsFilterToolbar
      table={table}
      primaryFilters={
        <>
          {dateRangeFilter}
          {taskIdFilter}
          {statusFilter}
        </>
      }
      advancedFilters={advancedFilters}
      advancedFilterCount={
        [filters.action, platform, filters.channel].filter(Boolean).length
      }
      hasAdvancedActiveFilters={
        !!filters.action || !!platform || !!filters.channel
      }
      mobilePinnedFilters={dateRangeFilter}
      mobileFilters={
        <>
          {taskIdFilter}
          {statusFilter}
          {advancedFilters}
        </>
      }
      mobileFilterCount={additionalFilters.filter(Boolean).length}
      hasActiveFilters={hasFilters}
      stats={stats}
      onSearch={apply}
      searchLoading={fetchingLogs > 0}
      onReset={reset}
    />
  )
}
