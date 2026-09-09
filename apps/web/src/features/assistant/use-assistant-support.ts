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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import {
  closeAssistantSupport,
  createAssistantSupport,
  getAssistantSupport,
  getAssistantSupportEligibility,
  getSelfAssistantSupport,
  isAssistantSupportActive,
  isAssistantSupportAIPaused,
  sendAssistantSupportMessage,
  type AssistantSupportInput,
  type AssistantSupportRequest,
} from './assistant-support-api'

export function useAssistantSupport(
  conversationId: number | null,
  visible: boolean,
  viewRevision = 0
) {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const client = useQueryClient()
  const [mutationState, setMutationState] = useState<{
    scope: string
    busy: boolean
  }>({ scope: '', busy: false })
  const [documentVisible, setDocumentVisible] = useState(
    () => document.visibilityState !== 'hidden'
  )
  const enabled = Boolean(userId) && visible && documentVisible
  const scope = `${userId}:${sessionId}:${conversationId}:${viewRevision}`
  const currentScope = useRef(scope)
  const busyRef = useRef<symbol | null>(null)
  if (currentScope.current !== scope) busyRef.current = null
  currentScope.current = scope
  const busy = mutationState.scope === scope && mutationState.busy
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    const change = () =>
      setDocumentVisible(document.visibilityState !== 'hidden')
    document.addEventListener('visibilitychange', change)
    return () => {
      mounted.current = false
      document.removeEventListener('visibilitychange', change)
    }
  }, [])
  const key = [
    'assistant-support',
    userId,
    sessionId,
    conversationId ?? 0,
    viewRevision,
  ]
  const self = useQuery({
    queryKey: key,
    queryFn: () => getSelfAssistantSupport(conversationId ?? 0),
    enabled,
    retry: false,
    staleTime: 0,
    refetchInterval: enabled ? 3000 : false,
    refetchIntervalInBackground: false,
  })
  const eligibility = useQuery({
    queryKey: ['assistant-support-eligibility', userId, sessionId],
    queryFn: getAssistantSupportEligibility,
    enabled,
    retry: false,
    staleTime: 30_000,
  })
  const request = self.data?.request ?? null
  const detailKey = ['assistant-support-detail', userId, sessionId, request?.id]
  const detail = useQuery({
    queryKey: detailKey,
    queryFn: () => {
      if (!request) throw new Error('No active support request')
      return getAssistantSupport(request.id)
    },
    enabled: enabled && Boolean(request),
    retry: false,
    staleTime: 0,
    refetchInterval:
      enabled && isAssistantSupportActive(request) ? 3000 : false,
    refetchIntervalInBackground: false,
  })
  // The detail response includes the latest status alongside the messages.
  const current =
    detail.data && detail.dataUpdatedAt > self.dataUpdatedAt
      ? detail.data.request
      : request
  const refresh = () =>
    client.invalidateQueries({ queryKey: ['assistant-support'] })
  async function mutate<T>(
    operation: () => Promise<T>,
    apply: (value: T) => void
  ): Promise<T | undefined> {
    if (busyRef.current) return
    const token = Symbol('support mutation')
    busyRef.current = token
    setMutationState({ scope, busy: true })
    const origin = scope
    const isCurrent = () =>
      mounted.current &&
      currentScope.current === origin &&
      busyRef.current === token &&
      useAuthStore.getState().auth.user?.id === userId &&
      useAuthStore.getState().auth.session?.sid === sessionId
    try {
      const value = await operation()
      if (!isCurrent()) return
      await Promise.all([
        client.cancelQueries({ queryKey: key, exact: true }),
        client.cancelQueries({ queryKey: detailKey, exact: true }),
      ])
      if (!isCurrent()) return
      apply(value)
      return value
    } catch (error) {
      if (!isCurrent()) return
      throw error
    } finally {
      if (isCurrent()) {
        busyRef.current = null
        setMutationState({ scope, busy: false })
      }
    }
  }
  function updateRequest(value: AssistantSupportRequest) {
    client.setQueryData(key, { request: value })
    client.setQueryData<
      import('./assistant-support-api').AssistantSupportDetail
    >(['assistant-support-detail', userId, sessionId, value.id], (previous) =>
      previous ? { ...previous, request: value } : undefined
    )
    void client.invalidateQueries({
      queryKey: ['assistant-support-detail', userId, sessionId, value.id],
    })
    void client.invalidateQueries({ queryKey: ['unified-todos'] })
  }
  return {
    request: current,
    messages: detail.data?.messages,
    detailRevision: detail.dataUpdatedAt,
    eligible: eligibility.data?.eligible === true,
    busy,
    active: isAssistantSupportActive(current),
    aiPaused: isAssistantSupportAIPaused(current),
    error: self.error || detail.error,
    refresh,
    create: (input: AssistantSupportInput) =>
      mutate(
        () =>
          createAssistantSupport({
            ...input,
            conversation_id: conversationId ?? 0,
          }),
        (value) => updateRequest(value.request)
      ),
    send: (content: string) =>
      current
        ? mutate(
            () => sendAssistantSupportMessage(current.id, content),
            () => {
              void client.invalidateQueries({ queryKey: detailKey })
            }
          )
        : Promise.resolve(undefined),
    close: () =>
      current
        ? mutate(
            () => closeAssistantSupport(current.id, true),
            (value) => updateRequest(value.request)
          )
        : Promise.resolve(undefined),
  }
}
