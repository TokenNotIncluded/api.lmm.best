/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { ChevronRight, LoaderCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { formatTimestampRelative, formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import type { TodoItem } from './api'
import { todoItemTitleKey } from './todo-labels'
import { TODO_CATEGORY_LABELS, todoTimestamp } from './todo-list-model'
import {
  todoDetailNumber,
  todoDetailString,
  todoItemCanOpen,
} from './todo-navigation'

export function TodoListRow({
  item,
  isAdmin,
  busy,
  onOpen,
}: {
  item: TodoItem
  isAdmin: boolean
  busy: boolean
  onOpen: (item: TodoItem) => void
}) {
  const { t, i18n } = useTranslation()
  const participant =
    todoDetailString(item, 'participant_username') ||
    todoDetailString(item, 'username')
  const applicantId = todoDetailNumber(item, 'user_id')
  const email = todoDetailString(item, 'email')
  const dateTime = todoTimestamp(item.updated_at)
  const canOpen = todoItemCanOpen(item, isAdmin)

  return (
    <li className='border-border border-b last:border-b-0'>
      <button
        type='button'
        className={cn(
          'focus-visible:ring-ring hover:bg-muted/50 relative flex min-h-11 w-full items-start gap-3 px-4 py-5 text-left transition-colors outline-none focus-visible:ring-2 focus-visible:ring-inset disabled:cursor-default sm:gap-4 sm:px-5',
          !item.read && 'bg-primary/[0.035]'
        )}
        onClick={() => onOpen(item)}
        disabled={!canOpen && (item.read || busy)}
        aria-busy={busy}
      >
        <span className='mt-2 flex size-2 shrink-0 items-center justify-center'>
          <span
            className={cn(
              'bg-primary size-1.5 rounded-full',
              item.read && 'invisible'
            )}
            aria-hidden='true'
          />
          <span className='sr-only'>{t(item.read ? 'Read' : 'Unread')}</span>
        </span>
        <span className='min-w-0 flex-1'>
          <span className='flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1'>
            <span
              className={cn(
                'text-sm leading-6',
                item.read ? 'font-medium' : 'font-semibold'
              )}
            >
              {t(todoItemTitleKey(item.title))}
            </span>
            {dateTime ? (
              <time
                className='text-muted-foreground shrink-0 text-xs font-normal'
                dateTime={dateTime}
                title={formatTimestampToDate(item.updated_at)}
              >
                {formatTimestampRelative(
                  item.updated_at,
                  'seconds',
                  i18n.language
                )}
              </time>
            ) : null}
          </span>
          {item.summary ? (
            <span className='text-muted-foreground mt-1 line-clamp-2 text-sm leading-6 break-words'>
              {item.summary}
            </span>
          ) : null}
          <span className='text-muted-foreground mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs leading-5'>
            <span>
              {t(TODO_CATEGORY_LABELS[item.category] ?? 'Notification')}
            </span>
            {participant ? (
              <span className='break-all'>@{participant}</span>
            ) : null}
            {applicantId ? (
              <span>
                {t('User ID')} {applicantId}
              </span>
            ) : null}
            {email ? <span className='break-all'>{email}</span> : null}
          </span>
        </span>
        <span
          className='mt-1 flex size-4 shrink-0 items-center justify-center'
          aria-hidden='true'
        >
          {busy ? (
            <LoaderCircle className='size-4 animate-spin motion-reduce:animate-none' />
          ) : canOpen ? (
            <ChevronRight className='text-muted-foreground size-4' />
          ) : null}
        </span>
      </button>
    </li>
  )
}
