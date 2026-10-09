/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { redactAssistantMessageForRequest } from '@/features/assistant/assistant-message-safety'
import { AssistantSupportControls } from '@/features/assistant/assistant-support-controls'
import { useAssistantSupport } from '@/features/assistant/use-assistant-support'
import { useAuthStore } from '@/stores/auth-store'

export function SupportConversation() {
  const user = useAuthStore((state) => state.auth.user)
  const sid = useAuthStore((state) => state.auth.session?.sid)
  return user ? <Conversation key={`${user.id}:${sid}`} /> : null
}

function Conversation() {
  const { t } = useTranslation()
  const support = useAssistantSupport(null, true)
  const [message, setMessage] = useState('')
  const [error, setError] = useState(false)
  async function perform(action: () => Promise<unknown>) {
    setError(false)
    try {
      return (await action()) !== undefined
    } catch {
      setError(true)
      return false
    }
  }
  return (
    <section
      className='space-y-3'
      aria-label={t('Human technical support')}
      data-testid='support-conversation'
    >
      <h2 className='text-base font-semibold'>
        {t('Human technical support')}
      </h2>
      <AssistantSupportControls
        request={support.request}
        eligible={support.eligible}
        busy={support.busy}
        error={Boolean(support.error)}
        onCreate={(input) => perform(() => support.create(input))}
        onClose={() => void perform(support.close)}
        onRetry={() => void support.refresh()}
      />
      {error && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Unable to update human support')}
        </p>
      )}
      {support.request && (
        <>
          <div
            className='max-h-80 space-y-3 overflow-y-auto'
            role='log'
            aria-label={t('Conversation')}
          >
            {support.messages?.map((item) => (
              <p
                key={item.id}
                className='text-sm break-words whitespace-pre-wrap'
              >
                <span className='text-muted-foreground'>
                  {item.role === 'user' ? t('You') : t('Support')} ·{' '}
                </span>
                {item.content}
              </p>
            ))}
          </div>
          {support.active && (
            <form
              className='space-y-2'
              onSubmit={(event) => {
                event.preventDefault()
                const safe =
                  redactAssistantMessageForRequest(message).content.trim()
                if (!safe || support.busy) return
                void perform(() => support.send(safe)).then((sent) => {
                  if (sent) {
                    setMessage((current) =>
                      current === message ? '' : current
                    )
                  }
                })
              }}
            >
              <Textarea
                aria-label={t('Message')}
                value={message}
                maxLength={4000}
                onChange={(event) => setMessage(event.target.value)}
              />
              <Button type='submit' disabled={support.busy || !message.trim()}>
                {t('Send')}
              </Button>
            </form>
          )}
        </>
      )}
    </section>
  )
}
