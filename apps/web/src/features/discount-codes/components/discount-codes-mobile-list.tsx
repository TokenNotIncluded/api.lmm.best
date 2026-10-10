/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { Copy, Pencil, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'

import {
  DISCOUNT_CODE_ENABLED_STATUS,
  getDiscountCodeAvailability,
} from '../availability'
import type { DiscountCode } from '../types'
import { DiscountCodeStatusBadge } from './discount-code-status-badge'

const DISABLED = 2

interface DiscountCodesMobileListProps {
  rows: DiscountCode[]
  selectedIds: Set<number>
  disabled?: boolean
  onToggleRow: (id: number, checked: boolean) => void
  onToggleStatus: (row: DiscountCode, enabled: boolean) => void
  onEdit: (row: DiscountCode) => void
  onDelete: (row: DiscountCode) => void
  onCopy: (code: string) => void
}

function formatDate(timestamp: number) {
  if (!timestamp) return '—'
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(timestamp * 1000))
}

function MobileField({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <div className='flex items-baseline justify-between gap-3 text-xs'>
      <span className='text-muted-foreground shrink-0'>{label}</span>
      <span className='min-w-0 text-right'>{children}</span>
    </div>
  )
}

/**
 * One card per code. Every column the desktop grid carried is present, so
 * nothing is only reachable on a wide screen, and the row actions keep full
 * 44px touch targets instead of icon-sized hits.
 */
export function DiscountCodesMobileList({
  rows,
  selectedIds,
  disabled = false,
  onToggleRow,
  onToggleStatus,
  onEdit,
  onDelete,
  onCopy,
}: DiscountCodesMobileListProps) {
  const { t } = useTranslation()

  return (
    <ul className='divide-border overflow-hidden rounded-lg border'>
      {rows.map((row) => {
        const selected = selectedIds.has(row.id)
        const availability = getDiscountCodeAvailability(row)
        const inactive = availability !== 'active'

        return (
          <li
            key={row.id}
            aria-disabled={inactive || undefined}
            className={cn(
              'bg-card space-y-3 border-b px-3 py-3 last:border-b-0',
              inactive && 'bg-muted/30'
            )}
          >
            <div className='flex items-start gap-3'>
              <Checkbox
                checked={selected}
                onCheckedChange={(checked) =>
                  onToggleRow(row.id, checked === true)
                }
                aria-label={`${t('Select code')} ${row.code}`}
                className='mt-0.5 shrink-0'
              />
              <div className='min-w-0 flex-1'>
                <div className='font-mono text-sm font-medium break-all'>
                  {row.code}
                </div>
                <div className='text-muted-foreground mt-0.5 truncate text-xs'>
                  {row.name}
                </div>
              </div>
              <DiscountCodeStatusBadge code={row} className='shrink-0' />
            </div>

            <div className='flex items-center justify-between gap-3 border-t pt-3'>
              <div className='flex min-w-0 items-center gap-3'>
                <span className='text-2xl leading-none font-semibold tabular-nums'>
                  {row.discount_percent}%
                </span>
                <span className='text-muted-foreground text-xs tabular-nums'>
                  {t('Used')} {row.used_count}
                  {' / '}
                  {row.max_uses > 0 ? row.max_uses : t('No maximum')}
                </span>
              </div>
              <label className='flex shrink-0 items-center gap-2'>
                <span className='sr-only'>{t('Enabled')}</span>
                <Switch
                  size='sm'
                  checked={row.status === DISCOUNT_CODE_ENABLED_STATUS}
                  disabled={disabled}
                  aria-label={`${row.code} ${t('Enabled')}`}
                  onCheckedChange={(checked) => onToggleStatus(row, checked)}
                />
              </label>
            </div>

            <div className='text-muted-foreground space-y-1 border-t pt-3'>
              <MobileField label={t('Starts')}>
                <span className='tabular-nums'>
                  {formatDate(row.starts_time)}
                </span>
              </MobileField>
              <MobileField label={t('Expires')}>
                <span className='tabular-nums'>
                  {row.expired_time
                    ? formatDate(row.expired_time)
                    : t('Never expires')}
                </span>
              </MobileField>
              {row.min_amount > 0 && (
                <MobileField label={t('Minimum amount')}>
                  <span className='tabular-nums'>{row.min_amount}</span>
                </MobileField>
              )}
            </div>

            <div className='flex items-center gap-2 border-t pt-3'>
              <Button
                variant='outline'
                className='h-11 flex-1 gap-2'
                onClick={() => onCopy(row.code)}
              >
                <Copy className='size-4' />
                {t('Copy')}
              </Button>
              <Button
                variant='outline'
                className='h-11 flex-1 gap-2'
                onClick={() => onEdit(row)}
              >
                <Pencil className='size-4' />
                {t('Edit')}
              </Button>
              <Button
                variant='outline'
                className='text-destructive h-11 flex-1 gap-2'
                onClick={() => onDelete(row)}
              >
                <Trash2 className='size-4' />
                {t('Delete')}
              </Button>
            </div>
          </li>
        )
      })}
    </ul>
  )
}

export { DISABLED as DISCOUNT_CODE_DISABLED_STATUS }
