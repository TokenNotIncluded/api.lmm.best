/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useNavigate } from '@tanstack/react-router'
import {
  ArrowDownUp,
  ChevronLeft,
  ChevronRight,
  Inbox,
  RefreshCw,
} from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type DragEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import type { TodoItem } from './api'
import { HumanSupportDialog } from './human-support-dialog'
import { TodoBurst } from './todo-burst'
import {
  TODO_CATEGORY_LABELS,
  todoPageCount,
  visibleTodoCategories,
} from './todo-list-model'
import { TodoListRow } from './todo-list-row'
import {
  todoDetailNumber,
  todoDetailString,
  todoItemCanOpen,
  todoSecurityReviewDestination,
} from './todo-navigation'
import {
  applyTodoOrder,
  mergeTodoOrder,
  readTodoOrder,
  writeTodoOrder,
} from './todo-order'
import { useTodoFeed } from './use-todo-feed'

export function UnifiedTodoList() {
  const user = useAuthStore((state) => state.auth.user)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  if (!user) return null
  return (
    <UnifiedTodoListContent
      key={`${user.id}:${sessionId ?? ''}:${user.role}`}
    />
  )
}

function UnifiedTodoListContent() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const feed = useTodoFeed()
  const { query, view, isAdmin } = feed
  const user = useAuthStore((state) => state.auth.user)
  const [supportItem, setSupportItem] = useState<TodoItem | null>(null)
  const [navigationFailed, setNavigationFailed] = useState(false)
  // Ordering is a per-browser view preference; the feed itself stays read-only.
  const [order, setOrder] = useState<string[]>(() => readTodoOrder(user?.id))
  const [sortable, setSortable] = useState(false)
  const [dragging, setDragging] = useState<string | null>(null)
  const [celebration, setCelebration] = useState(0)
  const dragSource = useRef<string | null>(null)
  const categories = query.data?.categories ?? []
  const visibleCategories = visibleTodoCategories(
    categories,
    view.category,
    isAdmin
  )
  const total =
    query.data?.category === view.category
      ? query.data.total
      : categories.find((item) => item.key === view.category)?.total
  const pages = todoPageCount(total ?? 0, query.data?.page_size ?? 50)
  const loadingRows = query.isLoading || query.isPlaceholderData
  const items = query.data?.items
  const displayItems = useMemo(
    () => (items ? applyTodoOrder(items, order) : []),
    [items, order]
  )
  const canSort = displayItems.length > 1

  // Reordering is scoped to the visible page, so a category switch starts over.
  const categoryKey = `${view.category}:${view.page}`
  useEffect(() => {
    setSortable(false)
    setDragging(null)
    dragSource.current = null
  }, [categoryKey])

  const navigateToItem = async (item: TodoItem) => {
    const securityDestination = todoSecurityReviewDestination(item)
    if (securityDestination) {
      await navigate({ to: securityDestination })
    } else if (
      item.category === 'developer_access' ||
      item.category === 'account_action'
    ) {
      await navigate({
        to: '/todos',
        search: { todo: item.category, request: item.source_id },
      })
    } else if (item.category === 'security_incident') {
      await navigate({
        to: '/users',
        search: {
          page: 1,
          pageSize: undefined,
          filter: todoDetailString(item, 'username'),
          status: [],
          role: [],
          group: '',
          l0Only: false,
        },
      })
    } else {
      const projectId = todoDetailNumber(item, 'project_id')
      if (projectId) {
        await navigate({
          to: '/challenges/$challengeId',
          params: { challengeId: String(projectId) },
        })
      }
    }
  }

  const openItem = (item: TodoItem) => {
    if (!feed.isCurrentSession()) return
    // Read receipts are best-effort metadata; they must not gate the task.
    void feed.markRead(item).then((marked) => {
      if (marked && feed.isCurrentSession()) setCelebration((n) => n + 1)
    })
    if (!todoItemCanOpen(item, isAdmin)) return
    setNavigationFailed(false)
    if (item.category === 'human_support') {
      setSupportItem(item)
      return
    }
    void navigateToItem(item).catch(() => {
      if (feed.isCurrentSession()) setNavigationFailed(true)
    })
  }

  /**
   * Clears the unread dot without navigating anywhere. Returns the receipt state
   * so the row can celebrate a real confirmation rather than an optimistic click.
   */
  const markReadOnly = (item: TodoItem) =>
    feed.markRead(item).then((marked) => {
      if (marked && feed.isCurrentSession()) setCelebration((n) => n + 1)
      return marked
    })

  const markAllRead = () => {
    void feed.markAllRead().then((marked) => {
      if (marked && feed.isCurrentSession()) setCelebration((n) => n + 1)
    })
  }

  const commitOrder = (ids: string[]) => {
    const merged = mergeTodoOrder(ids, order)
    setOrder(merged)
    writeTodoOrder(user?.id, merged)
  }

  const moveItem = (sourceId: string, targetId: string) => {
    if (sourceId === targetId) return
    const ids = displayItems.map((item) => item.id)
    const from = ids.indexOf(sourceId)
    const to = ids.indexOf(targetId)
    if (from < 0 || to < 0) return
    const next = [...ids]
    const [moved] = next.splice(from, 1)
    next.splice(to, 0, moved)
    commitOrder(next)
  }

  const nudgeItem = (item: TodoItem, direction: -1 | 1) => {
    const ids = displayItems.map((entry) => entry.id)
    const from = ids.indexOf(item.id)
    const to = from + direction
    if (from < 0 || to < 0 || to >= ids.length) return
    const next = [...ids]
    const [moved] = next.splice(from, 1)
    next.splice(to, 0, moved)
    commitOrder(next)
  }

  const resetOrder = () => {
    setOrder([])
    writeTodoOrder(user?.id, [])
  }

  return (
    <section
      aria-label={t('To-dos')}
      aria-busy={query.isFetching}
      className='relative min-w-0'
    >
      <div className='border-border mb-5 overflow-x-auto border-b'>
        <div
          role='group'
          aria-label={t('To-dos')}
          className='flex min-w-max gap-1'
        >
          {visibleCategories.map((key) => {
            const unread =
              key === 'all'
                ? query.data?.total_unread_count
                : categories.find((item) => item.key === key)?.unread
            return (
              <button
                key={key}
                type='button'
                aria-pressed={view.category === key}
                className={cn(
                  'focus-visible:ring-ring text-muted-foreground hover:text-foreground relative flex min-h-11 shrink-0 items-center gap-2 border-b-2 border-transparent px-3 py-3 text-sm transition-colors outline-none focus-visible:ring-2 focus-visible:ring-inset',
                  view.category === key &&
                    'border-primary text-foreground font-medium'
                )}
                onClick={() => feed.selectCategory(key)}
              >
                {t(TODO_CATEGORY_LABELS[key])}
                {unread ? (
                  <span className='bg-muted text-foreground rounded-md px-1.5 py-0.5 text-xs tabular-nums'>
                    <span className='sr-only'>{t('Unread')} </span>
                    {unread}
                  </span>
                ) : null}
              </button>
            )
          })}
        </div>
      </div>

      <div className='mb-4 flex flex-wrap items-center justify-between gap-3'>
        <div className='flex flex-wrap items-baseline gap-x-3 gap-y-1'>
          <h3 className='text-sm font-semibold'>
            {t(TODO_CATEGORY_LABELS[view.category])}
          </h3>
          <span className='text-muted-foreground text-xs tabular-nums'>
            {t('Total')}: {total ?? '—'}
          </span>
          {sortable ? (
            <span className='text-muted-foreground text-xs'>
              {t('Saved in this browser')}
            </span>
          ) : null}
        </div>
        <div className='flex items-center gap-2'>
          {canSort ? (
            <Button
              type='button'
              variant={sortable ? 'secondary' : 'ghost'}
              size='sm'
              className='min-h-11'
              aria-pressed={sortable}
              onClick={() => setSortable((value) => !value)}
            >
              <ArrowDownUp aria-hidden='true' className='size-4' />
              {sortable ? t('Done sorting') : t('Sort list')}
            </Button>
          ) : null}
          {order.length && !sortable ? (
            <Button
              type='button'
              variant='ghost'
              size='sm'
              className='min-h-11'
              onClick={resetOrder}
            >
              {t('Reset order')}
            </Button>
          ) : null}
          {(query.data?.total_unread_count ?? 0) > 0 ? (
            <div className='relative'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                className='min-h-11'
                disabled={feed.pendingReads.size > 0}
                onClick={markAllRead}
              >
                {t('Mark all as read')}
              </Button>
              <TodoBurst burstKey={celebration || null} />
            </div>
          ) : null}
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='min-h-11 min-w-11'
            aria-label={t('Refresh')}
            title={t('Refresh')}
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            <RefreshCw
              aria-hidden='true'
              className={cn(
                'size-4',
                query.isFetching && 'animate-spin motion-reduce:animate-none'
              )}
            />
          </Button>
        </div>
      </div>

      {query.isError ? (
        <div
          role='alert'
          className='border-destructive/30 bg-destructive/5 mb-4 flex flex-wrap items-center justify-between gap-3 rounded-lg border px-4 py-3 text-sm'
        >
          <p>{t('Failed to load to-dos')}</p>
          <Button
            variant='outline'
            size='sm'
            className='min-h-11'
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            {t('Retry')}
          </Button>
        </div>
      ) : null}
      {feed.failedRead || navigationFailed ? (
        <div
          role='alert'
          className='border-destructive/30 bg-destructive/5 mb-4 flex flex-wrap items-center justify-between gap-3 rounded-lg border px-4 py-3 text-sm'
        >
          <p>{t('Operation failed')}</p>
          {feed.failedRead ? (
            <Button
              variant='outline'
              size='sm'
              className='min-h-11'
              disabled={feed.pendingReads.size > 0}
              onClick={feed.retryRead}
            >
              {t('Retry')}
            </Button>
          ) : null}
        </div>
      ) : null}

      <div className='border-border overflow-hidden rounded-xl border'>
        {loadingRows ? (
          <div role='status' className='divide-border divide-y'>
            <span className='sr-only'>{t('Loading')}</span>
            {[0, 1, 2, 3].map((key) => (
              <div
                key={key}
                aria-hidden='true'
                className='space-y-3 px-5 py-6 motion-safe:animate-pulse'
              >
                <div className='bg-muted h-4 w-1/3 rounded' />
                <div className='bg-muted h-3 w-3/4 rounded' />
                <div className='bg-muted h-3 w-1/2 rounded' />
              </div>
            ))}
          </div>
        ) : displayItems.length ? (
          <ul className='m-0 list-none p-0'>
            {displayItems.map((item, index) => (
              <TodoListRow
                key={item.id}
                item={item}
                isAdmin={isAdmin}
                busy={
                  feed.pendingReads.has(item.id) || feed.pendingReads.has('all')
                }
                onOpen={openItem}
                onMarkRead={markReadOnly}
                position={{ index, count: displayItems.length }}
                onMove={nudgeItem}
                isDragging={dragging === item.id}
                drag={{
                  draggable: sortable,
                  onDragStart: (
                    event: DragEvent<HTMLElement>,
                    row: TodoItem
                  ) => {
                    event.dataTransfer.effectAllowed = 'move'
                    dragSource.current = row.id
                    setDragging(row.id)
                  },
                  onDragOver: (event: DragEvent<HTMLElement>) => {
                    event.preventDefault()
                    event.dataTransfer.dropEffect = 'move'
                  },
                  onDrop: (event: DragEvent<HTMLElement>, row: TodoItem) => {
                    event.preventDefault()
                    const source = dragSource.current
                    if (source) moveItem(source, row.id)
                    dragSource.current = null
                    setDragging(null)
                  },
                  onDragEnd: () => {
                    dragSource.current = null
                    setDragging(null)
                  },
                }}
              />
            ))}
          </ul>
        ) : !query.isError ? (
          <div className='px-6 py-16 text-center'>
            <span
              aria-hidden='true'
              className='bg-muted/60 relative mx-auto mb-5 flex size-16 items-center justify-center rounded-2xl'
            >
              <Inbox className='text-muted-foreground size-7' />
              <span className='bg-success/70 absolute -top-1 -right-1 size-3 rounded-full' />
              <span className='bg-primary/60 absolute -bottom-1 -left-1 size-2 rounded-full' />
            </span>
            <p className='text-sm font-medium'>{t('No pending to-dos')}</p>
            <p className='text-muted-foreground mx-auto mt-2 max-w-sm text-sm leading-6'>
              {t(
                'Submitted challenge work and account requests will appear here.'
              )}
            </p>
            <p className='text-muted-foreground mx-auto mt-2 max-w-sm text-xs leading-5'>
              {t('New reviews and account requests appear here.')}
            </p>
          </div>
        ) : null}
        {pages > 1 || view.page > 1 ? (
          <div className='border-border flex flex-wrap items-center justify-between gap-3 border-t px-4 py-3'>
            <span
              className='text-muted-foreground text-xs tabular-nums'
              aria-live='polite'
            >
              {t('Page')} {view.page}
              {total !== undefined ? ` / ${pages}` : ''}
            </span>
            <div className='flex items-center gap-2'>
              <Button
                variant='outline'
                size='sm'
                className='min-h-11'
                aria-label={t('Previous page')}
                disabled={view.page <= 1 || query.isFetching}
                onClick={() => feed.selectPage(view.page - 1)}
              >
                <ChevronLeft className='size-4' aria-hidden='true' />
                {t('Previous')}
              </Button>
              <Button
                variant='outline'
                size='sm'
                className='min-h-11'
                aria-label={t('Next page')}
                disabled={
                  view.page >= pages || query.isFetching || query.isError
                }
                onClick={() => feed.selectPage(view.page + 1)}
              >
                {t('Next')}
                <ChevronRight className='size-4' aria-hidden='true' />
              </Button>
            </div>
          </div>
        ) : null}
      </div>
      {supportItem && isAdmin ? (
        <HumanSupportDialog
          item={
            query.data?.items.find((item) => item.id === supportItem.id) ??
            supportItem
          }
          onClose={() => setSupportItem(null)}
        />
      ) : null}
    </section>
  )
}
