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
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import type { OnChangeFn, SortingState } from '@tanstack/react-table'
import { SearchX, UserPlus } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTablePage,
  useDataTable,
} from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { useMediaQuery } from '@/hooks'
import { useTableUrlState } from '@/hooks/use-table-url-state'

import { getUsers, searchUsers } from '../api'
import {
  USER_STATUS,
  getUserStatusOptions,
  getUserRoleOptions,
  isUserDeleted,
} from '../constants'
import type { User, UserSortBy } from '../types'
import { DataTableBulkActions } from './data-table-bulk-actions'
import { useUsersColumns } from './users-columns'
import { UsersFilters, UsersSort, UsersGroupFilter } from './users-filters'
import { UsersMobileBulkBar } from './users-mobile-bulk-bar'
import { UsersMobileList } from './users-mobile-list'
import { useUsers } from './users-provider'

import '../users-mobile.css'

const route = getRouteApi('/_authenticated/users/')

const USER_SORTABLE_COLUMNS = new Set<UserSortBy>([
  'id',
  'username',
  'quota',
  'group',
  'created_at',
  'last_login_at',
  'topup_quota',
  'topup_money',
  'assistant_violations',
  'risk_score',
  'transferred_quota',
  'received_quota',
  'checkin_quota',
  'used_quota',
  'request_count',
])

function isDisabledUserRow(user: User) {
  return isUserDeleted(user) || user.status === USER_STATUS.DISABLED
}

export function UsersTable() {
  const { t } = useTranslation()
  const columns = useUsersColumns()
  const { refreshTrigger, setOpen, setCurrentRow } = useUsers()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const sorting = useMemo<SortingState>(
    () => [{ id: search.sortBy, desc: search.sortOrder === 'desc' }],
    [search.sortBy, search.sortOrder]
  )
  const resetFilters = () =>
    navigate({
      search: {
        pageSize: search.pageSize,
        l0Only: false,
        sortBy: 'id',
        sortOrder: 'desc',
        risk: 'all',
        transfers: 'all',
        usage: 'all',
        funding: 'all',
        checkin: 'all',
      },
    })
  const activityFilters = {
    risk_min:
      search.risk === 'high' ? 0.8 : search.risk === 'medium' ? 0.4 : undefined,
    risk_max:
      search.risk === 'low'
        ? 0.399
        : search.risk === 'medium'
          ? 0.799
          : undefined,
    transfers: search.transfers === 'all' ? undefined : search.transfers,
    usage: search.usage === 'all' ? undefined : search.usage,
    funding: search.funding === 'all' ? undefined : search.funding,
    checkin: search.checkin === 'all' ? undefined : search.checkin,
  }
  const activityCount =
    [
      search.risk,
      search.transfers,
      search.usage,
      search.funding,
      search.checkin,
    ].filter((value) => value !== 'all').length + Number(search.l0Only)
  const l0Only = search.l0Only
  const {
    globalFilter,
    onGlobalFilterChange,
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search,
    navigate,
    pagination: { defaultPage: 1, defaultPageSize: isMobile ? 10 : 20 },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [
      { columnId: 'status', searchKey: 'status', type: 'array' },
      { columnId: 'role', searchKey: 'role', type: 'array' },
      { columnId: 'group', searchKey: 'group', type: 'string' },
    ],
  })
  const statusFilter =
    (columnFilters.find((filter) => filter.id === 'status')?.value as
      | string[]
      | undefined) ?? []
  const roleFilter =
    (columnFilters.find((filter) => filter.id === 'role')?.value as
      | string[]
      | undefined) ?? []
  const groupFilter =
    (columnFilters.find((filter) => filter.id === 'group')?.value as string) ??
    ''

  const sortParams = useMemo(() => {
    const activeSort = sorting[0]
    if (
      !activeSort ||
      !USER_SORTABLE_COLUMNS.has(activeSort.id as UserSortBy)
    ) {
      return {}
    }

    return {
      sort_by: activeSort.id as UserSortBy,
      sort_order: activeSort.desc ? 'desc' : 'asc',
    } as const
  }, [sorting])

  const handleSortingChange: OnChangeFn<SortingState> = (updater) => {
    const next = typeof updater === 'function' ? updater(sorting) : updater
    const active = next[0]
    navigate({
      search: (previous) => ({
        ...previous,
        page: undefined,
        sortBy:
          active && USER_SORTABLE_COLUMNS.has(active.id as UserSortBy)
            ? (active.id as UserSortBy)
            : 'id',
        sortOrder: active?.desc === false ? 'asc' : 'desc',
      }),
    })
  }

  // Fetch data with React Query
  const { data, isLoading, isFetching, isError, refetch } = useQuery({
    queryKey: [
      'users',
      pagination.pageIndex + 1,
      pagination.pageSize,
      globalFilter,
      statusFilter,
      roleFilter,
      groupFilter,
      l0Only,
      sortParams,
      activityFilters,
      refreshTrigger,
    ],
    queryFn: async () => {
      const hasFilter = globalFilter?.trim()
      const hasColumnFilter =
        statusFilter.length > 0 || roleFilter.length > 0 || Boolean(groupFilter)
      const params = {
        p: pagination.pageIndex + 1,
        page_size: pagination.pageSize,
        trust_level: l0Only ? 0 : undefined,
        ...sortParams,
        ...activityFilters,
      }

      const result =
        hasFilter || hasColumnFilter
          ? await searchUsers({
              ...params,
              keyword: globalFilter,
              status: statusFilter[0] ?? '',
              role: roleFilter[0] ?? '',
              group: groupFilter,
            })
          : await getUsers(params)

      if (!result.success) {
        throw new Error(result.message || t('Failed to load users'))
      }

      return {
        items: result.data?.items || [],
        total: result.data?.total || 0,
      }
    },
    placeholderData: (previousData) => previousData,
  })

  const users = data?.items || []

  const handleL0OnlyChange = (checked: boolean) => {
    navigate({
      search: (previous) => ({
        ...previous,
        page: undefined,
        l0Only: checked,
      }),
    })
  }

  const { table } = useDataTable({
    data: users,
    columns,
    enableRowSelection: true,
    columnFilters,
    globalFilter,
    pagination,
    sorting,
    globalFilterFn: (row, _columnId, filterValue) => {
      const searchValue = String(filterValue).toLowerCase()
      const fields = [
        row.getValue('username'),
        row.original.display_name,
        row.original.email,
      ]
      return fields.some((field) =>
        String(field || '')
          .toLowerCase()
          .includes(searchValue)
      )
    },
    onPaginationChange,
    onGlobalFilterChange,
    onColumnFiltersChange,
    onSortingChange: handleSortingChange,
    manualPagination: true,
    manualFiltering: true,
    manualSorting: true,
    totalCount: data?.total || 0,
    ensurePageInRange,
  })

  const sortControls = (
    <UsersSort
      search={search}
      onChange={(patch) =>
        navigate({
          search: (previous) => ({ ...previous, ...patch, page: undefined }),
        })
      }
    />
  )

  return (
    <>
      {isError && (
        <div
          role='alert'
          className='flex items-center justify-between gap-3 rounded-md border p-3 text-sm'
        >
          {t('Failed to load users')}
          <Button variant='outline' onClick={() => refetch()}>
            {t('Retry')}
          </Button>
        </div>
      )}
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        isFetching={isFetching}
        emptyTitle={isError ? t('Failed to load users') : t('No Users Found')}
        emptyDescription={t(
          'No users available. Try adjusting your search or filters.'
        )}
        skeletonKeyPrefix='users-skeleton'
        applyHeaderSize
        emptyIcon={<SearchX className='size-6' />}
        emptyAction={
          isError ? (
            <Button onClick={() => refetch()}>{t('Retry')}</Button>
          ) : (
            <div className='flex flex-wrap items-center justify-center gap-2'>
              <Button
                variant='outline'
                className='h-11 gap-2 sm:h-9'
                onClick={() => {
                  resetFilters()
                }}
              >
                {t('Clear filters')}
              </Button>
              <Button
                className='h-11 gap-2 sm:h-9'
                onClick={() => {
                  setCurrentRow(null)
                  setOpen('create')
                }}
              >
                <UserPlus className='size-4' />
                {t('Add User')}
              </Button>
            </div>
          )
        }
        mobile={
          <>
            <UsersMobileBulkBar table={table} />
            <UsersMobileList
              table={table}
              isLoading={isLoading}
              isFetching={isFetching && !isLoading}
              emptyTitle={
                isError ? t('Failed to load users') : t('No Users Found')
              }
              emptyDescription={t(
                'No users available. Try adjusting your search or filters.'
              )}
              emptyAction={
                <div className='flex w-full flex-col gap-2'>
                  <Button
                    className='h-11 w-full gap-2'
                    onClick={() => {
                      setCurrentRow(null)
                      setOpen('create')
                    }}
                  >
                    <UserPlus className='size-4' />
                    {t('Add User')}
                  </Button>
                  <Button
                    variant='outline'
                    className='h-11 w-full'
                    onClick={() => {
                      resetFilters()
                    }}
                  >
                    {t('Clear filters')}
                  </Button>
                </div>
              }
            />
          </>
        }
        toolbarProps={{
          className: 'users-toolbar',
          // Column visibility controls apply to the desktop table, not this list.
          hideViewOptions: isMobile,
          searchPlaceholder: t('Filter by username, name or email...'),
          searchDebounceMs: 500,
          onReset: resetFilters,
          hasExpandedActiveFilters: activityCount > 0,
          hasAdditionalFilters: activityCount > 0,
          additionalFilterCount: activityCount,
          additionalFilterSummary:
            activityCount > 0 ? (
              <div className='flex flex-wrap gap-2'>
                {(
                  [
                    [
                      'risk',
                      search.risk,
                      {
                        high: 'High risk (0.8–1)',
                        medium: 'Medium risk (0.4–0.8)',
                        low: 'Low risk (0–0.4)',
                      },
                    ],
                    [
                      'transfers',
                      search.transfers,
                      {
                        sent: 'Sent transfers',
                        received: 'Received transfers',
                        none: 'No transfers',
                      },
                    ],
                    [
                      'usage',
                      search.usage,
                      { zero: 'Zero consumption', consumed: 'Has consumption' },
                    ],
                    [
                      'funding',
                      search.funding,
                      { paid: 'Has paid top-ups', unpaid: 'No paid top-ups' },
                    ],
                    [
                      'checkin',
                      search.checkin,
                      { yes: 'Has check-ins', no: 'No check-ins' },
                    ],
                  ] as const
                )
                  .filter(([, value]) => value !== 'all')
                  .map(([key, value, labels]) => (
                    <Button
                      key={key}
                      variant='outline'
                      size='sm'
                      onClick={() =>
                        navigate({
                          search: (previous) => ({
                            ...previous,
                            [key]: 'all',
                            page: undefined,
                          }),
                        })
                      }
                    >
                      {t((labels as Record<string, string>)[value])} ×
                    </Button>
                  ))}
                {search.l0Only && (
                  <Button
                    size='sm'
                    variant='outline'
                    onClick={() => handleL0OnlyChange(false)}
                  >
                    {t('Only show L0 users')} ×
                  </Button>
                )}
              </div>
            ) : undefined,
          preActions: isMobile ? undefined : sortControls,
          additionalSearch: (
            <>
              {isMobile && sortControls}
              <UsersGroupFilter
                value={groupFilter}
                onChange={(value) =>
                  table.getColumn('group')?.setFilterValue(value)
                }
              />
              <UsersFilters
                search={search}
                onChange={(patch) =>
                  navigate({
                    search: (previous) => ({
                      ...previous,
                      ...patch,
                      page: undefined,
                    }),
                  })
                }
              />
              <div className='border-input flex h-11 items-center gap-2 rounded-md border px-3 sm:h-9'>
                <Switch
                  id='users-l0-only'
                  size='sm'
                  checked={l0Only}
                  onCheckedChange={handleL0OnlyChange}
                />
                <Label
                  htmlFor='users-l0-only'
                  className='cursor-pointer whitespace-nowrap'
                >
                  {t('Only show L0 users')}
                </Label>
              </div>
            </>
          ),
          filters: [
            {
              columnId: 'status',
              title: t('Status'),
              options: getUserStatusOptions(t),
              singleSelect: true,
            },
            {
              columnId: 'role',
              title: t('Role'),
              options: getUserRoleOptions(t),
              singleSelect: true,
            },
          ],
        }}
        getRowClassName={(row, { isMobile }) =>
          isDisabledUserRow(row.original)
            ? isMobile
              ? DISABLED_ROW_MOBILE
              : DISABLED_ROW_DESKTOP
            : undefined
        }
        bulkActions={<DataTableBulkActions table={table} />}
      />
    </>
  )
}
