/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type { Table } from '@tanstack/react-table'
import { Copy, X } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { cn } from '@/lib/utils'

import type { Redemption } from '../types'

interface RedemptionsMobileBulkBarProps {
  table: Table<Redemption>
}

/**
 * Mobile counterpart of the shared bulk-actions toolbar.
 *
 * `DataTablePage` gates the shared toolbar behind `!showMobile`, which left
 * mobile row selection with no reachable actions. This bar keeps the same
 * contract (role, keyboard handling, live region, Escape-to-clear) while
 * giving the actions full-width 44px targets the shared icon-only row cannot.
 */
export function RedemptionsMobileBulkBar({
  table,
}: RedemptionsMobileBulkBarProps) {
  const { t } = useTranslation()
  const selectedRows = table.getFilteredSelectedRowModel().rows
  const selectedCount = selectedRows.length
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })

  const contentToCopy = useMemo(
    () =>
      selectedRows
        .map((row) => `${row.original.name}\t${row.original.key}`)
        .join('\n'),
    [selectedRows]
  )
  const copied = copiedText === contentToCopy

  if (selectedCount === 0) return null

  return (
    <div
      role='toolbar'
      aria-label={t('Bulk actions for selected redemption codes')}
      className={cn(
        'fixed inset-x-2 bottom-3 z-50 rounded-2xl border p-2 shadow-xl',
        'bg-background/95 supports-[backdrop-filter]:bg-background/60 backdrop-blur-lg',
        'motion-safe:animate-in motion-safe:fade-in-0 motion-safe:slide-in-from-bottom-4 motion-safe:duration-200'
      )}
    >
      <div className='flex items-center gap-2'>
        <span
          className='bg-primary text-primary-foreground grid size-7 shrink-0 place-items-center rounded-full text-xs font-semibold tabular-nums'
          aria-hidden='true'
        >
          {selectedCount}
        </span>
        <span className='min-w-0 flex-1 truncate text-sm font-medium'>
          {t('{{count}} selected', { count: selectedCount })}
        </span>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon'
                onClick={() => table.resetRowSelection()}
                className='size-11 shrink-0'
                aria-label={t('Clear selection')}
              />
            }
          >
            <X className='size-4' />
          </TooltipTrigger>
          <TooltipContent>
            <p>{t('Clear selection (Escape)')}</p>
          </TooltipContent>
        </Tooltip>
      </div>

      <div className='mt-2 border-t pt-2'>
        <Button
          variant='outline'
          onClick={() => copyToClipboard(contentToCopy)}
          className='h-11 w-full justify-start gap-2'
        >
          <Copy className='shrink-0' />
          <span className='truncate'>
            {copied ? t('Codes copied!') : t('Copy selected codes')}
          </span>
        </Button>
      </div>
    </div>
  )
}
