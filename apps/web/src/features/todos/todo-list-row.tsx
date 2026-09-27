/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { Check, ChevronRight, GripVertical, LoaderCircle } from 'lucide-react'
import { useState, type DragEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { formatTimestampRelative, formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import type { TodoItem } from './api'
import { TodoBurst } from './todo-burst'
import { todoItemTitleKey } from './todo-labels'
import { TODO_CATEGORY_LABELS, todoTimestamp } from './todo-list-model'
import {
  todoDetailNumber,
  todoDetailString,
  todoItemCanOpen,
} from './todo-navigation'

export type TodoRowDragHandlers = {
  draggable?: boolean
  onDragStart?: (event: DragEvent<HTMLElement>, item: TodoItem) => void
  onDragOver?: (event: DragEvent<HTMLElement>) => void
  onDrop?: (event: DragEvent<HTMLElement>, item: TodoItem) => void
  onDragEnd?: () => void
}

/**
 * One notification row.
 *
 * The row body opens the destination when one exists, and marking read is a
 * separate control. That split matters: the read receipt used to be welded onto
 * a row-wide button that went `disabled` as soon as the item was read, so a
 * non-admin's already-read notifications were dead buttons with no action left.
 */
export function TodoListRow({
  item,
  isAdmin,
  busy,
  onOpen,
  onMarkRead,
  position,
  onMove,
  drag,
  isDragging,
}: {
  item: TodoItem
  isAdmin: boolean
  busy: boolean
  onOpen: (item: TodoItem) => void
  /** Marks the item read; resolves `true` when the server accepted it. */
  onMarkRead: (item: TodoItem) => Promise<boolean>
  /** Index within the visible page, for keyboard reordering. */
  position?: { index: number; count: number }
  onMove?: (item: TodoItem, direction: -1 | 1) => void
  drag?: TodoRowDragHandlers
  isDragging?: boolean
}) {
  const { t, i18n } = useTranslation()
  const [dragOver, setDragOver] = useState(false)
  const [celebrated, setCelebrated] = useState(false)
  const participant =
    todoDetailString(item, 'participant_username') ||
    todoDetailString(item, 'username')
  const applicantId = todoDetailNumber(item, 'user_id')
  const email = todoDetailString(item, 'email')
  const dateTime = todoTimestamp(item.updated_at)
  const canOpen = todoItemCanOpen(item, isAdmin)
  const draggable = drag?.draggable === true

  /**
   * The single read path. Opening a row also clears it, which is why both the
   * body and the explicit button land here instead of calling the feed twice.
   */
  const markRead = () => {
    if (item.read || busy) return
    void onMarkRead(item).then((marked) => {
      if (marked) setCelebrated(true)
    })
  }

  const handleOpen = () => {
    markRead()
    onOpen(item)
  }

  return (
    <li
      className={cn(
        'border-border relative border-b last:border-b-0',
        isDragging && 'opacity-40',
        dragOver && 'ring-ring/40 ring-2 ring-inset'
      )}
      onDragOver={
        draggable
          ? (event) => {
              setDragOver(true)
              drag?.onDragOver?.(event)
            }
          : undefined
      }
      onDragLeave={draggable ? () => setDragOver(false) : undefined}
      onDrop={
        draggable
          ? (event) => {
              setDragOver(false)
              drag?.onDrop?.(event, item)
            }
          : undefined
      }
    >
      <div
        className={cn(
          'group/row relative flex items-stretch',
          !item.read && 'bg-primary/[0.035]'
        )}
      >
        {draggable && position ? (
          <div className='flex w-8 shrink-0 flex-col items-center justify-center gap-0.5'>
            <button
              type='button'
              draggable
              aria-label={t('Drag to reorder')}
              title={t('Drag to reorder')}
              className='text-muted-foreground/70 hover:text-muted-foreground focus-visible:ring-ring flex size-6 cursor-grab touch-none items-center justify-center rounded outline-none focus-visible:ring-2 active:cursor-grabbing'
              onDragStart={(event) => drag?.onDragStart?.(event, item)}
              onDragEnd={() => {
                setDragOver(false)
                drag?.onDragEnd?.()
              }}
            >
              <GripVertical aria-hidden='true' className='size-4' />
            </button>
            <button
              type='button'
              aria-label={t('Move up')}
              title={t('Move up')}
              disabled={position.index === 0}
              className='text-muted-foreground/70 hover:text-muted-foreground focus-visible:ring-ring flex size-5 items-center justify-center rounded outline-none focus-visible:ring-2 disabled:opacity-30'
              onClick={() => onMove?.(item, -1)}
            >
              <ChevronRight
                aria-hidden='true'
                className='size-3.5 -rotate-90'
              />
            </button>
            <button
              type='button'
              aria-label={t('Move down')}
              title={t('Move down')}
              disabled={position.index === position.count - 1}
              className='text-muted-foreground/70 hover:text-muted-foreground focus-visible:ring-ring flex size-5 items-center justify-center rounded outline-none focus-visible:ring-2 disabled:opacity-30'
              onClick={() => onMove?.(item, 1)}
            >
              <ChevronRight aria-hidden='true' className='size-3.5 rotate-90' />
            </button>
          </div>
        ) : null}

        <button
          type='button'
          className='focus-visible:ring-ring hover:bg-muted/50 flex min-h-11 min-w-0 flex-1 items-start gap-3 px-4 py-5 text-left transition-colors outline-none focus-visible:ring-2 focus-visible:ring-inset sm:gap-4 sm:px-5'
          onClick={handleOpen}
          aria-busy={busy}
          disabled={!canOpen && !item.read && busy}
        >
          <span className='mt-2 flex size-2 shrink-0 items-center justify-center'>
            <span
              className={cn(
                'bg-primary size-1.5 rounded-full transition-opacity motion-reduce:transition-none',
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
            ) : item.read && !canOpen ? (
              <Check className='text-success size-4' />
            ) : canOpen ? (
              <ChevronRight className='text-muted-foreground size-4 transition-transform group-hover/row:translate-x-0.5 motion-reduce:transition-none' />
            ) : null}
          </span>
        </button>

        <div className='flex shrink-0 items-center gap-1 pr-3 pl-1 sm:pr-4'>
          {!item.read ? (
            <Button
              type='button'
              variant='ghost'
              size='sm'
              className='text-muted-foreground hover:text-foreground h-9 min-h-9 px-2 text-xs'
              disabled={busy}
              onClick={markRead}
            >
              {t('Mark as read')}
            </Button>
          ) : null}
          {canOpen ? (
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='h-9 min-h-9 px-2 text-xs'
              disabled={busy}
              onClick={() => onOpen(item)}
            >
              {t('Open')}
            </Button>
          ) : null}
        </div>

        <TodoBurst burstKey={celebrated ? 1 : null} />
      </div>
    </li>
  )
}
