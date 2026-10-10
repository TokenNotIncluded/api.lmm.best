/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { Palette, Undo2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import appI18n from '@/i18n/config'
import { INTERFACE_LANGUAGE_OPTIONS, normalizeInterfaceLanguage } from '@/i18n/languages'
import { THEME_PRESETS } from '@/lib/theme-customization'
import { useAuthStore } from '@/stores/auth-store'

import type { AssistantUIPreferenceAction } from './assistant-ui-preferences-contract'
import { getUIPreferenceCopy } from './assistant-ui-preferences-copy'
import { assistantUIPreferenceRuntime as runtime, type UIPreferenceReceipt } from './assistant-ui-preferences-runtime'
import { useAssistantUIPreferenceAdapter } from './use-assistant-ui-preferences'

export function AssistantUIPreferencesCard({ action, disabled = false }: {
  action: AssistantUIPreferenceAction
  disabled?: boolean
}) {
  const { t, i18n } = useTranslation(undefined, { i18n: appI18n })
  const copy = getUIPreferenceCopy(normalizeInterfaceLanguage(i18n.language))
  const adapter = useAssistantUIPreferenceAdapter()
  const current = useRef({ action, adapter })
  current.current = { action, adapter }
  const [receipt, setReceipt] = useState<UIPreferenceReceipt | undefined>(undefined)
  const [undoing, setUndoing] = useState(false)
  const lifetime = useRef<AbortController | null>(null)

  useEffect(() => {
    if (disabled) return
    const controller = new AbortController()
    lifetime.current = controller
    const sameOwner = () => {
      const owner = current.current.adapter.owner()
      return owner?.userID === current.current.action.actor_user_id &&
        owner.sessionID === current.current.action.actor_session_id
    }
    const syncReceipt = () => setReceipt(sameOwner()
      ? runtime.receipt(action.action_id)
      : { status: 'unavailable', canUndo: false })
    const unsubscribe = runtime.subscribe(syncReceipt)
    const unsubscribeAuth = useAuthStore.subscribe(() => {
      if (!sameOwner()) {
        controller.abort()
        syncReceipt()
      }
    })
    // Defer one task so React StrictMode's setup/cleanup probe never starts a
    // write. Re-delivery and real remounts are deduplicated by the runtime.
    const start = setTimeout(() => {
      void runtime.run(current.current.action, current.current.adapter, controller.signal)
        .then((result) => { if (!controller.signal.aborted) setReceipt(result) })
    }, 0)
    return () => {
      clearTimeout(start)
      controller.abort()
      unsubscribe()
      unsubscribeAuth()
      if (lifetime.current === controller) lifetime.current = null
    }
  }, [action.action_id, disabled])

  async function undo() {
    if (disabled || undoing || !lifetime.current) return
    setUndoing(true)
    const signal = lifetime.current.signal
    const result = await runtime.undo(adapter, signal, action.action_id)
    if (!signal.aborted) {
      setReceipt(result)
      setUndoing(false)
    }
  }

  const status = receipt?.status ?? 'applying'
  const labels = { mode: t('Appearance'), theme: t('Theme'), language: t('Language'), currency: t('Currency') }
  const valueLabel = (key: string, value: string) => {
    if (key === 'theme') return THEME_PRESETS.find((item) => item.value === value)?.name ?? value
    if (key === 'language') return INTERFACE_LANGUAGE_OPTIONS.find((item) => item.code === value)?.label ?? value
    return t(({ light: 'Light', dark: 'Dark', system: 'System', auto: 'Automatic', CREDIT: 'Credits' } as Record<string, string>)[value] ?? value)
  }
  return (
    <section className='bg-muted/30 w-full min-w-0 space-y-3 rounded-2xl px-5 py-4' aria-label={copy.title}>
      <h3 className='flex items-center gap-2 text-sm font-medium'><Palette className='size-4' aria-hidden='true' />{copy.title}</h3>
      {Object.keys(action.preview).length > 0 && (
        <dl className='grid grid-cols-1 gap-2 text-sm sm:grid-cols-2'>
          {Object.entries(action.preview).map(([key, value]) => (
            <div key={key} className='min-w-0'>
              <dt className='text-muted-foreground text-xs'>{labels[key as keyof typeof labels]}</dt>
              <dd className='break-words'>{valueLabel(key, value)}</dd>
            </div>
          ))}
        </dl>
      )}
      <p role={status === 'failed' || status === 'unavailable' ? 'alert' : 'status'} className='text-sm'>{copy[status]}</p>
      <p className='text-muted-foreground text-xs'>{copy.scope}</p>
      {receipt?.canUndo && (
        <Button type='button' variant='ghost' size='sm' disabled={disabled || undoing} onClick={() => void undo()}>
          <Undo2 className='size-4' aria-hidden='true' />{copy.undo}
        </Button>
      )}
    </section>
  )
}
