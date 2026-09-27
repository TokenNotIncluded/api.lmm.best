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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

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
import { UsersMobileBulkBar } from './users-mobile-bulk-bar'
import { UsersMobileList } from './users-mobile-list'
import { useUsers } from './users-provider'

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
])

function isDisabledUserRow(user: User) {
  return isUserDeleted(user) || user.status === USER_STATUS.DISABLED
}

export function UsersTable() {
  const { t } = useTranslation()
  const columns = useUsersColumns()
  const { refreshTrigger, setOpen, setCurrentRow } = useUsers()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const [sorting, setSorting] = useState<SortingState>([])
  const search = route.useSearch()
  const navigate = route.useNavigate()
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
    setSorting(updater)
    if (pagination.pageIndex > 0) {
      onPaginationChange({ ...pagination, pageIndex: 0 })
    }
  }

  // Fetch data with React Query
  const { data, isLoading, isFetching } = useQuery({
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
        toast.error(
          result.message || `Failed to ${hasFilter ? 'search' : 'load'} users`
        )
        return { items: [], total: 0 }
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

  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={isLoading}
      isFetching={isFetching}
      emptyTitle={t('No Users Found')}
      emptyDescription={t(
        'No users available. Try adjusting your search or filters.'
      )}
      skeletonKeyPrefix='users-skeleton'
      applyHeaderSize
      emptyIcon={<SearchX className='size-6' />}
      emptyAction={
        <div className='flex flex-wrap items-center justify-center gap-2'>
          <Button
            variant='outline'
            className='h-11 gap-2 sm:h-9'
            onClick={() => {
              onGlobalFilterChange?.('')
              onColumnFiltersChange?.([])
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
      }
      mobile={
        <>
          <UsersMobileList
            table={table}
            isLoading={isLoading}
            isFetching={isFetching && !isLoading}
            emptyTitle={t('No Users Found')}
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
                    onGlobalFilterChange?.('')
                    onColumnFiltersChange?.([])
                  }}
                >
                  {t('Clear filters')}
                </Button>
              </div>
            }
          />
          {/* DataTablePage gates the shared bulk-actions toolbar behind
              !showMobile, so mobile selection needs its own bar. */}
          <UsersMobileBulkBar table={table} />
        </>
      }
      toolbarProps={{
        searchPlaceholder: t('Filter by username, name or email...'),
        searchDebounceMs: 500,
        additionalSearch: (
          <div className='border-input flex h-9 items-center gap-2 rounded-md border px-3'>
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
  )
}
