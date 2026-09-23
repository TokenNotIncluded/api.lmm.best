/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type { Table } from '@tanstack/react-table'
import { Mail, UserRound, X } from 'lucide-react'
import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { cn } from '@/lib/utils'

import {
  collectContacts,
  collectUsernames,
  joinLines,
} from '../lib/bulk-selection'
import type { User } from '../types'

interface UsersMobileBulkBarProps {
  table: Table<User>
}

/**
 * Mobile counterpart of the shared bulk-actions toolbar.
 *
 * The shared `DataTableBulkActions` is gated behind `!showMobile` by
 * `DataTablePage`, which left mobile row selection with no reachable actions.
 * This bar keeps the same contract (role, keyboard handling, live region,
 * Escape-to-clear) while presenting full-width labelled buttons that hold the
 * 44px touch target the shared icon-only row cannot provide.
 */
export function UsersMobileBulkBar({ table }: UsersMobileBulkBarProps) {
  const { t } = useTranslation()
  const selectedRows = table.getFilteredSelectedRowModel().rows
  const selectedUsers = selectedRows.map((row) => row.original)
  const selectedCount = selectedUsers.length

  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })

  const barRef = useRef<HTMLDivElement>(null)
  const buttonsRef = useRef<NodeListOf<HTMLButtonElement> | null>(null)
  const descriptionId = useId()
  const [announcement, setAnnouncement] = useState('')

  useLayoutEffect(() => {
    buttonsRef.current =
      barRef.current?.querySelectorAll<HTMLButtonElement>(
        'button:not(:disabled)'
      ) ?? null
  })

  useEffect(() => {
    if (selectedCount > 0) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setAnnouncement(
        `${selectedCount} user${selectedCount > 1 ? 's' : ''} selected. Bulk actions toolbar is available.`
      )
      const timer = setTimeout(() => setAnnouncement(''), 3000)
      return () => clearTimeout(timer)
    }
  }, [selectedCount])

  const usernamesPayload = joinLines(collectUsernames(selectedUsers))
  const contactsPayload = joinLines(collectContacts(selectedUsers))
  const hasUsernames = usernamesPayload.length > 0
  const hasContacts = contactsPayload.length > 0

  const handleClearSelection = () => {
    table.resetRowSelection()
  }

  const handleKeyDown = (event: React.KeyboardEvent) => {
    const buttons = buttonsRef.current
    if (!buttons?.length) return

    const currentIndex = [...buttons].indexOf(
      document.activeElement as HTMLButtonElement
    )

    switch (event.key) {
      case 'ArrowRight': {
        event.preventDefault()
        const nextIndex =
          currentIndex < 0 ? 0 : (currentIndex + 1) % buttons.length
        buttons[nextIndex]?.focus()
        break
      }
      case 'ArrowLeft': {
        event.preventDefault()
        const prevIndex =
          currentIndex <= 0 ? buttons.length - 1 : currentIndex - 1
        buttons[prevIndex]?.focus()
        break
      }
      case 'Home':
        event.preventDefault()
        buttons[0]?.focus()
        break
      case 'End':
        event.preventDefault()
        buttons.item(buttons.length - 1)?.focus()
        break
      case 'Escape': {
        const target = event.target as HTMLElement
        const activeElement = document.activeElement as HTMLElement
        const isFromTooltip =
          target?.closest('[data-slot="tooltip-content"]') ||
          activeElement?.closest('[data-slot="tooltip-content"]')

        if (isFromTooltip) return

        event.preventDefault()
        handleClearSelection()
        break
      }
    }
  }

  if (selectedCount === 0) {
    return null
  }

  return (
    <>
      <div
        aria-live='polite'
        aria-atomic='true'
        className='sr-only'
        role='status'
      >
        {announcement}
      </div>

      <div
        ref={barRef}
        role='toolbar'
        aria-label={t('Bulk actions for selected users')}
        aria-describedby={descriptionId}
        tabIndex={-1}
        onKeyDown={handleKeyDown}
        className={cn(
          'fixed inset-x-2 bottom-3 z-50 rounded-2xl',
          'motion-safe:animate-in motion-safe:fade-in-0 motion-safe:slide-in-from-bottom-4 motion-safe:duration-200',
          'focus-visible:ring-ring/50 focus-visible:ring-2 focus-visible:outline-none'
        )}
      >
        <div
          className={cn(
            'rounded-2xl border p-2 shadow-xl',
            'bg-background/95 supports-[backdrop-filter]:bg-background/60 backdrop-blur-lg'
          )}
        >
          <div className='flex items-center gap-2'>
            <span
              className='bg-primary text-primary-foreground grid size-7 shrink-0 place-items-center rounded-full text-xs font-semibold tabular-nums'
              aria-hidden='true'
            >
              {selectedCount}
            </span>
            <span
              id={descriptionId}
              className='min-w-0 flex-1 truncate text-sm font-medium'
            >
              {t('{{count}} selected', { count: selectedCount })}
            </span>
            <Button
              variant='ghost'
              size='icon'
              onClick={handleClearSelection}
              className='size-11 shrink-0'
              aria-label={t('Clear selection')}
              title={t('Clear selection (Escape)')}
            >
              <X />
            </Button>
          </div>

          <Separator className='my-2' aria-hidden='true' />

          <div className='grid grid-cols-2 gap-2'>
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant='outline'
                    onClick={() => copyToClipboard(usernamesPayload)}
                    disabled={!hasUsernames}
                    className='h-11 min-w-0 justify-start gap-2'
                    aria-label={t('Copy usernames')}
                  />
                }
              >
                <UserRound className='shrink-0' />
                <span className='truncate'>
                  {copiedText === usernamesPayload && hasUsernames
                    ? t('Copied')
                    : t('Copy usernames')}
                </span>
              </TooltipTrigger>
              <TooltipContent>
                <p>{t('Copy usernames')}</p>
              </TooltipContent>
            </Tooltip>

            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant='outline'
                    onClick={() => copyToClipboard(contactsPayload)}
                    disabled={!hasContacts}
                    className='h-11 min-w-0 justify-start gap-2'
                    aria-label={t('Copy usernames and emails')}
                  />
                }
              >
                <Mail className='shrink-0' />
                <span className='truncate'>
                  {copiedText === contactsPayload && hasContacts
                    ? t('Copied')
                    : t('Copy usernames and emails')}
                </span>
              </TooltipTrigger>
              <TooltipContent>
                <p>{t('Copy usernames and emails')}</p>
              </TooltipContent>
            </Tooltip>
          </div>
        </div>
      </div>
    </>
  )
}
