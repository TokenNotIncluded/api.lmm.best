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
along with this program. If not, you have received a copy of the
License, or (at your option) any later version.
*/
import { AiChat02Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { lazy, Suspense, useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useStatus } from '@/hooks/use-status'
import { isConsoleActivated } from '@/lib/console-activation'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  consumeQueuedAssistantRequest,
  peekQueuedAssistantRequest,
  subscribeToAssistantOpen,
  type AssistantPresetId,
} from './assistant-events'
import { setAssistantRailOpen, useAssistantRailOpen } from './assistant-rail'
import { useAssistantOverlay } from './assistant-responsive'

const loadAssistantPanel = () => import('./assistant-panel')
const AssistantPanel = lazy(() =>
  loadAssistantPanel().then((module) => ({
    default: module.AssistantPanel,
  }))
)

/** Desktop rail width. Kept in one place so the animated wrapper and the
 * panel itself stay perfectly in sync while the width transition runs. */
// `w-96 max-w-[26vw]` avoids commas inside arbitrary values, which this
// Tailwind setup does not generate rules for.
const RAIL_WIDTH = 'w-96 max-w-[26vw]'

export function AssistantLauncher(props: { page?: boolean }) {
  const { t } = useTranslation()
  const { status } = useStatus()
  const user = useAuthStore((state) => state.auth.user)
  const isOverlayViewport = useAssistantOverlay()
  const [mobileOpen, setMobileOpen] = useState(false)
  const [desktopFullscreen, setDesktopFullscreen] = useState(false)
  const railOpen = useAssistantRailOpen()
  const [initialPreset, setInitialPreset] = useState<
    AssistantPresetId | undefined
  >(() =>
    props.page
      ? isConsoleActivated(user)
        ? 'client-setup'
        : 'onboarding'
      : undefined
  )
  const [initialMessage, setInitialMessage] = useState<string>()
  const [initialMessageRevision, setInitialMessageRevision] = useState(0)
  const [autoSendRequestId, setAutoSendRequestId] = useState<string>()

  const showAssistant = useCallback(
    (request: {
      id: string
      preset?: AssistantPresetId
      message?: string
      autoSend: boolean
    }) => {
      let preset = request.preset
      if (!preset && request.autoSend) {
        preset = isConsoleActivated(user) ? 'service' : 'onboarding'
      }
      setInitialPreset(preset)
      setInitialMessage(request.message)
      if (request.message?.trim()) {
        setInitialMessageRevision((revision) => revision + 1)
        setAutoSendRequestId(request.autoSend ? request.id : undefined)
      }
      if (!request.autoSend) consumeQueuedAssistantRequest(request.id)
      // Desktop: the in-flow rail opens (the panel itself keeps its
      // conversation state because it never unmounts). Mobile: overlay.
      setAssistantRailOpen(true)
      setMobileOpen(true)
    },
    [user]
  )

  // The overlay sheet and the desktop rail are exclusive by viewport; only
  // the active one mounts so a queued auto-send can never fire twice.

  const handleConversationReset = useCallback(() => {
    setInitialPreset(undefined)
    setInitialMessage(undefined)
    setInitialMessageRevision((revision) => revision + 1)
    setAutoSendRequestId(undefined)
  }, [])

  useEffect(() => {
    const queued = peekQueuedAssistantRequest()
    if (queued) showAssistant(queued)
    return subscribeToAssistantOpen(showAssistant)
  }, [showAssistant])

  const handleAutoSendConsumed = useCallback((requestId: string) => {
    consumeQueuedAssistantRequest(requestId)
    setAutoSendRequestId((current) =>
      current === requestId ? undefined : current
    )
  }, [])

  const showManualAssistant = useCallback(() => {
    showAssistant({ id: 'manual', autoSend: false })
  }, [showAssistant])

  useEffect(() => {
    const handleShortcut = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.altKey ||
        !event.shiftKey ||
        !(event.metaKey || event.ctrlKey) ||
        event.key.toLowerCase() !== 'a'
      ) {
        return
      }

      event.preventDefault()
      showManualAssistant()
    }

    window.addEventListener('keydown', handleShortcut)
    return () => window.removeEventListener('keydown', handleShortcut)
  }, [showManualAssistant])

  if (status?.assistant?.enabled === false) return null

  if (props.page) {
    return (
      <Suspense
        fallback={<aside className='bg-background hidden' aria-hidden='true' />}
      >
        <AssistantPanel
          mode='page'
          open
          initialPreset={initialPreset}
          initialMessage={initialMessage}
          initialMessageRevision={initialMessageRevision}
          autoSendRequestId={autoSendRequestId}
          onAutoSendConsumed={handleAutoSendConsumed}
          onOpenChange={() => undefined}
          onConversationReset={handleConversationReset}
        />
      </Suspense>
    )
  }

  const needsL1Unlock = user !== null && !isConsoleActivated(user)
  const visibleLabel = needsL1Unlock
    ? t('Unlock L1 with AI')
    : t('Service guide')
  const accessibleLabel = needsL1Unlock
    ? t('Unlock L1 with AI')
    : t('Open AI assistant')

  return (
    <div className='contents'>
      {/* Mobile floating pill — opens the overlay sheet. */}
      <div
        className='pointer-events-none fixed inset-x-0 bottom-0 z-40 flex min-h-14 items-center justify-center px-3 py-1.5 pb-[max(0.375rem,env(safe-area-inset-bottom))] xl:hidden'
        data-testid='assistant-mobile-launcher'
      >
        <Button
          type='button'
          variant='secondary'
          className='pointer-events-auto h-11 w-full max-w-md justify-start gap-2 rounded-full px-4 shadow-sm md:w-auto md:min-w-44'
          aria-label={accessibleLabel}
          title={accessibleLabel}
          aria-haspopup='dialog'
          aria-expanded={mobileOpen}
          aria-controls='ai-assistant-panel'
          data-testid='assistant-launcher'
          onClick={() => showAssistant({ id: 'manual', autoSend: false })}
        >
          <HugeiconsIcon
            icon={AiChat02Icon}
            strokeWidth={2}
            data-icon='inline-start'
            aria-hidden='true'
          />
          <span className='truncate text-sm font-medium'>{visibleLabel}</span>
        </Button>
      </div>

      {/* Desktop: in-flow right rail. Same stacking level as the main card —
       * opening it shrinks the content area, matching the shell's rounded
       * card language. The wrapper animates width; the inner panel keeps a
       * fixed width so text does not reflow mid-transition. */}
      <div
        className={cn(
          'hidden shrink-0 overflow-hidden transition-[width,margin,opacity] duration-300 ease-[cubic-bezier(0.32,0.72,0,1)] xl:block',
          railOpen ? cn('opacity-100', RAIL_WIDTH, 'ms-2') : 'w-0 opacity-0'
        )}
        data-testid='assistant-rail'
        data-open={railOpen}
        aria-hidden={!railOpen}
      >
        <div className={cn('h-full min-w-0', RAIL_WIDTH)}>
          <Suspense
            fallback={
              <aside
                className='bg-card h-full w-full rounded-xl border'
                aria-hidden='true'
              />
            }
          >
            <AssistantPanel
              mode='rail'
              open={railOpen}
              fullscreen={desktopFullscreen}
              initialPreset={initialPreset}
              initialMessage={initialMessage}
              initialMessageRevision={initialMessageRevision}
              autoSendRequestId={autoSendRequestId}
              onAutoSendConsumed={handleAutoSendConsumed}
              onOpenChange={setAssistantRailOpen}
              onConversationReset={handleConversationReset}
              onToggleFullscreen={() => setDesktopFullscreen((value) => !value)}
            />
          </Suspense>
        </div>
      </div>

      {/* Mobile / narrow overlay sheet. Exclusive with the desktop rail so a
       * queued auto-send can never fire in both presentations. */}
      <Suspense fallback={null}>
        {isOverlayViewport ? (
          <AssistantPanel
            mode='mobile'
            open={mobileOpen}
            initialPreset={initialPreset}
            initialMessage={initialMessage}
            initialMessageRevision={initialMessageRevision}
            autoSendRequestId={autoSendRequestId}
            onAutoSendConsumed={handleAutoSendConsumed}
            onOpenChange={setMobileOpen}
            onConversationReset={handleConversationReset}
          />
        ) : null}
      </Suspense>
    </div>
  )
}
