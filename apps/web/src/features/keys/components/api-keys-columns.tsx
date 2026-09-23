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
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Checkbox } from '@/components/ui/checkbox'
import { Progress } from '@/components/ui/progress'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useMediaQuery } from '@/hooks'
import { toIntlLocale } from '@/i18n/languages'
import { getUserGroups } from '@/lib/api'
import dayjs from '@/lib/dayjs'
import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import { API_KEY_STATUSES } from '../constants'
import { buildApiKeyGroupOptions, isAssistantRuntimeKey } from '../lib'
import { getQuotaProgressColor, getQuotaUsage } from '../lib/quota-usage'
import type { ApiKey, ApiKeyCreationMode } from '../types'
import { ApiKeyCreationSourceBadge } from './api-key-creation-source'
import type { ApiKeyGroupOption } from './api-key-group-combobox'
import { ApiKeyGroupQuickSwitch } from './api-key-group-quick-switch'
import { ApiKeyTimestampCell } from './api-key-timestamp-cell'
import {
  ApiKeyCell,
  ApiKeyUsedQuota,
  IpRestrictionsCell,
  ModelLimitsCell,
  UnlimitedQuotaBadge,
} from './api-keys-cells'
import { DataTableRowActions } from './data-table-row-actions'

function useGroupOptions(): {
  options: ApiKeyGroupOption[]
  isLoading: boolean
} {
  const { data, isLoading } = useQuery({
    queryKey: ['user-groups'],
    queryFn: getUserGroups,
    staleTime: 0,
    select: (res) =>
      buildApiKeyGroupOptions(res.success ? res.data : undefined),
  })

  return { options: data ?? [], isLoading }
}

export function useApiKeysColumns(
  now: number,
  creationMode: ApiKeyCreationMode
): ColumnDef<ApiKey>[] {
  const { t, i18n } = useTranslation()
  const { options: groupOptions, isLoading: groupOptionsLoading } =
    useGroupOptions()
  const groupRatios = useMemo(() => {
    const ratios: Record<string, number | string> = {}
    for (const option of groupOptions) {
      if (
        typeof option.ratio === 'number' ||
        typeof option.ratio === 'string'
      ) {
        ratios[option.value] = option.ratio
      }
    }
    return ratios
  }, [groupOptions])
  const shouldReduceMotion = useMediaQuery('(prefers-reduced-motion: reduce)')
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const justNowLabel = t('Just now')
  const staleAccessThreshold = dayjs(now).subtract(3, 'month').valueOf()
  return [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected()}
          indeterminate={table.getIsSomePageRowsSelected()}
          onCheckedChange={(value) => table.toggleAllPageRowsSelected(!!value)}
          aria-label={t('Select all')}
          className='translate-y-[2px]'
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(value) => row.toggleSelected(!!value)}
          disabled={!row.getCanSelect()}
          aria-label={t('Select row')}
          className='translate-y-[2px]'
        />
      ),
      enableSorting: false,
      enableHiding: false,
      size: 40,
    },
    {
      accessorKey: 'name',
      header: t('Name'),
      cell: ({ row }) => (
        <span className='font-medium'>{row.getValue('name')}</span>
      ),
      size: 180,
      meta: { mobileTitle: true },
    },
    {
      accessorKey: 'status',
      header: t('Status'),
      cell: ({ row }) => {
        const statusConfig = API_KEY_STATUSES[row.getValue('status') as number]
        if (!statusConfig) return null
        return (
          <StatusBadge
            label={t(statusConfig.label)}
            variant={statusConfig.variant}
            copyable={false}
            className='-ml-1.5'
          />
        )
      },
      filterFn: (row, id, value) => value.includes(String(row.getValue(id))),
      size: 120,
      meta: { mobileBadge: true },
    },
    ...(creationMode === 'automatic'
      ? [
          {
            id: 'creation_source',
            header: t('Creation source'),
            cell: ({ row }) => (
              <ApiKeyCreationSourceBadge apiKey={row.original} />
            ),
            enableSorting: false,
            size: 150,
          } satisfies ColumnDef<ApiKey>,
        ]
      : []),
    {
      id: 'key',
      accessorKey: 'key',
      header: t('API Key'),
      cell: ({ row }) => <ApiKeyCell apiKey={row.original} />,
      enableSorting: false,
      size: 260,
    },
    {
      id: 'quota',
      accessorKey: 'remain_quota',
      header: t('Quota'),
      cell: ({ row }) => {
        const apiKey = row.original
        if (isAssistantRuntimeKey(apiKey)) {
          return (
            <span className='text-muted-foreground text-xs'>
              {t('Billed to super administrator wallet')}
            </span>
          )
        }
        if (apiKey.unlimited_quota) {
          return <UnlimitedQuotaBadge used={apiKey.used_quota} />
        }

        const { used, remaining, total, remainingPercent } =
          getQuotaUsage(apiKey)

        return (
          <Tooltip>
            <TooltipTrigger render={<div className='w-[150px] space-y-1' />}>
              <div className='flex justify-between text-xs'>
                <span className='font-medium tabular-nums'>
                  {formatQuota(remaining)}
                </span>
                <span className='text-muted-foreground tabular-nums'>
                  {formatQuota(total)}
                </span>
              </div>
              <Progress
                value={remainingPercent}
                className={cn('h-1.5', getQuotaProgressColor(remainingPercent))}
              />
            </TooltipTrigger>
            <TooltipContent>
              <div className='space-y-1 text-xs'>
                <div>
                  {t('Used:')} {formatQuota(used)}
                </div>
                <div>
                  {t('Remaining:')} {formatQuota(remaining)} (
                  {remainingPercent.toFixed(1)}%)
                </div>
                <div>
                  {t('Total:')} {formatQuota(total)}
                </div>
              </div>
            </TooltipContent>
          </Tooltip>
        )
      },
      size: 170,
    },
    {
      id: 'used_quota',
      accessorKey: 'used_quota',
      header: t('Used quota'),
      cell: ({ row }) =>
        isAssistantRuntimeKey(row.original) ? (
          <span className='text-muted-foreground text-xs'>
            {t('Tracked in assistant funding')}
          </span>
        ) : (
          <ApiKeyUsedQuota used={row.original.used_quota} />
        ),
      size: 140,
    },
    {
      accessorKey: 'group',
      header: t('Group'),
      cell: ({ row }) => {
        const apiKey = row.original
        const group = row.getValue('group') as string
        return (
          <ApiKeyGroupQuickSwitch
            apiKey={apiKey}
            options={groupOptions}
            optionsLoading={groupOptionsLoading}
            ratio={groupRatios[group]}
            shouldReduceMotion={shouldReduceMotion}
          />
        )
      },
      size: 220,
      meta: { mobileHidden: true },
    },
    {
      id: 'model_limits',
      accessorKey: 'model_limits',
      header: t('Models'),
      cell: ({ row }) => <ModelLimitsCell apiKey={row.original} />,
      enableSorting: false,
      size: 160,
      meta: { mobileHidden: true },
    },
    {
      id: 'allow_ips',
      accessorKey: 'allow_ips',
      header: t('IP Restriction'),
      cell: ({ row }) => <IpRestrictionsCell apiKey={row.original} />,
      enableSorting: false,
      size: 160,
      meta: { mobileHidden: true },
    },
    {
      accessorKey: 'created_time',
      header: t('Created'),
      cell: ({ row }) => (
        <ApiKeyTimestampCell
          timestamp={row.getValue('created_time')}
          now={now}
          locale={locale}
          justNowLabel={justNowLabel}
          className='text-muted-foreground'
        />
      ),
      size: 180,
      meta: { mobileHidden: true },
    },
    {
      accessorKey: 'accessed_time',
      header: t('Last Used'),
      cell: ({ row }) => {
        const accessedTime = row.getValue('accessed_time') as number
        const isStale =
          accessedTime > 0 && accessedTime * 1000 < staleAccessThreshold

        return (
          <ApiKeyTimestampCell
            timestamp={accessedTime}
            now={now}
            locale={locale}
            justNowLabel={justNowLabel}
            className={isStale ? 'text-warning' : 'text-muted-foreground'}
          />
        )
      },
      size: 180,
      meta: { mobileHidden: true },
    },
    {
      accessorKey: 'expired_time',
      header: t('Expires'),
      cell: ({ row }) => {
        const expiredTime = row.getValue('expired_time') as number
        if (expiredTime === -1) {
          return (
            <StatusBadge
              label={t('Never')}
              variant='neutral'
              copyable={false}
              className='-ml-1.5'
            />
          )
        }
        const isExpired = expiredTime * 1000 < now
        return (
          <ApiKeyTimestampCell
            timestamp={expiredTime}
            now={now}
            locale={locale}
            justNowLabel={justNowLabel}
            className={cn(
              isExpired ? 'text-destructive' : 'text-muted-foreground'
            )}
          />
        )
      },
      size: 180,
      meta: { mobileHidden: true },
    },
    {
      id: 'actions',
      header: () => t('Actions'),
      cell: ({ row }) =>
        isAssistantRuntimeKey(row.original) ? null : (
          <DataTableRowActions row={row} />
        ),
      meta: { pinned: 'right' as const },
    },
  ]
}
