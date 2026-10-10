/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  getTodos,
  markAllTodosRead,
  markTodoRead,
  type TodoCategory,
  type TodoItem,
} from './api'
import { todoPageCount } from './todo-list-model'

type ReadOperation = { kind: 'all' } | { kind: 'item'; item: TodoItem }

export function useTodoFeed() {
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const isAdmin = (user?.role ?? 0) >= ROLE.ADMIN
  const [view, setView] = useState<{ category: TodoCategory; page: number }>({
    category: 'all',
    page: 1,
  })
  const [failedRead, setFailedRead] = useState<ReadOperation | null>(null)
  const [pendingReads, setPendingReads] = useState<ReadonlySet<string>>(
    new Set()
  )
  const inFlight = useRef(new Set<string>())
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  const isCurrentSession = () => {
    const auth = useAuthStore.getState().auth
    return (
      mounted.current &&
      auth.user?.id === user?.id &&
      auth.session?.sid === sessionId &&
      auth.user?.role === user?.role
    )
  }

  const query = useQuery({
    queryKey: [
      'todos',
      user?.id,
      sessionId,
      user?.role,
      view.category,
      view.page,
    ],
    queryFn: ({ signal }) => getTodos(view.category, view.page, signal),
    enabled: Boolean(user),
    staleTime: 10_000,
    refetchInterval: 10_000,
    refetchIntervalInBackground: false,
    // The owner component is keyed by account, session and role. Old rows are
    // hidden while this placeholder keeps filter counts and controls stable.
    placeholderData: keepPreviousData,
  })
  const lastPage = todoPageCount(
    query.data?.total ?? 0,
    query.data?.page_size ?? 50
  )
  useEffect(() => {
    if (query.isSuccess && !query.isPlaceholderData && view.page > lastPage) {
      // A formerly valid page can disappear after an item is processed.
      // The destination page must not resurrect a still-fresh cached list.
      void queryClient.invalidateQueries({
        queryKey: [
          'todos',
          user?.id,
          sessionId,
          user?.role,
          view.category,
          lastPage,
        ],
        exact: true,
        refetchType: 'none',
      })
      setView((previous) => ({ ...previous, page: lastPage }))
    }
  }, [
    lastPage,
    query.isPlaceholderData,
    query.isSuccess,
    queryClient,
    sessionId,
    user?.id,
    user?.role,
    view.category,
    view.page,
  ])

  const readMutation = useMutation({
    mutationFn: (operation: ReadOperation) =>
      operation.kind === 'all'
        ? markAllTodosRead()
        : markTodoRead(operation.item),
  })

  /**
   * Returns whether the receipt was actually accepted, so callers can reward a
   * real confirmation instead of an optimistic click.
   */
  const runRead = async (operation: ReadOperation): Promise<boolean> => {
    const key = operation.kind === 'all' ? 'all' : operation.item.id
    if (
      !isCurrentSession() ||
      inFlight.current.has('all') ||
      inFlight.current.has(key) ||
      (operation.kind === 'all' && inFlight.current.size > 0)
    ) {
      return false
    }
    inFlight.current.add(key)
    setPendingReads(new Set(inFlight.current))
    try {
      await readMutation.mutateAsync(operation)
      if (!isCurrentSession()) return false
      setFailedRead((previous) => {
        const previousKey = previous?.kind === 'all' ? 'all' : previous?.item.id
        return operation.kind === 'all' || previousKey === key ? null : previous
      })
      // Include the navigation badge and all cached categories, as before.
      await queryClient.invalidateQueries({ queryKey: ['todos'], exact: false })
      return true
    } catch {
      if (isCurrentSession()) setFailedRead(operation)
      return false
    } finally {
      inFlight.current.delete(key)
      if (isCurrentSession()) setPendingReads(new Set(inFlight.current))
    }
  }

  return {
    query,
    isAdmin,
    view,
    pendingReads,
    failedRead,
    isCurrentSession,
    selectCategory: (category: TodoCategory) => setView({ category, page: 1 }),
    selectPage: (page: number) =>
      setView((previous) => ({ ...previous, page })),
    markRead: (item: TodoItem): Promise<boolean> =>
      item.read ? Promise.resolve(false) : runRead({ kind: 'item', item }),
    markAllRead: (): Promise<boolean> => runRead({ kind: 'all' }),
    retryRead: () => {
      if (failedRead) void runRead(failedRead)
    },
  }
}
