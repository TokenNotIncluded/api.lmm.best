/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

import { LMM_ISSUER } from './integration-prompts'
import { buildRequestBody, buildRequestSnippet } from './request-snippet'

import './request-builder.css'

type Protocol = 'chat' | 'messages' | 'gemini'
type Language = 'curl' | 'javascript' | 'python'

const PROTOCOLS: readonly {
  id: Protocol
  label: string
  path: string
  shape: 'openai' | 'anthropic' | 'gemini'
}[] = [
  {
    id: 'chat',
    label: 'Chat Completions',
    path: '/v1/chat/completions',
    shape: 'openai',
  },
  {
    id: 'messages',
    label: 'Claude Messages',
    path: '/v1/messages',
    shape: 'anthropic',
  },
  {
    id: 'gemini',
    label: 'Gemini',
    path: '/v1beta/models/{model}:generateContent',
    shape: 'gemini',
  },
]

const LANGUAGES: readonly { id: Language; label: string }[] = [
  { id: 'curl', label: 'cURL' },
  { id: 'javascript', label: 'JavaScript' },
  { id: 'python', label: 'Python' },
]

const MODEL_SUGGESTIONS = ['gpt-4o-mini', 'claude-sonnet-4', 'gemini-2.0-flash']

/**
 * Build a real request against api.lmm.best without sending it. The snippet is
 * generated from the same fields the server expects, so what a developer copies
 * is exactly what a working client sends.
 */
export function RequestBuilder() {
  const { t } = useTranslation()
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })
  const [protocol, setProtocol] = useState<Protocol>('chat')
  const [language, setLanguage] = useState<Language>('curl')
  const [model, setModel] = useState(MODEL_SUGGESTIONS[0])
  const [prompt, setPrompt] = useState('Explain LMM in one sentence.')

  const active = PROTOCOLS.find((item) => item.id === protocol) ?? PROTOCOLS[0]
  const url = `${LMM_ISSUER}${active.path.replace('{model}', encodeURIComponent(model || 'MODEL'))}`
  const body = useMemo(
    () => buildRequestBody(active.shape, model, prompt || 'Hello'),
    [active.shape, model, prompt]
  )
  const snippet = useMemo(
    () => buildRequestSnippet(language, url, body),
    [language, url, body]
  )
  const copied = copiedText === snippet

  return (
    <div className='dev-builder mt-6'>
      <div className='dev-builder-row'>
        <span className='text-muted-foreground text-xs font-medium tracking-wide uppercase'>
          {t('Protocol')}
        </span>
        <div
          role='group'
          aria-label={t('Protocol')}
          className='dev-builder-tabs'
        >
          {PROTOCOLS.map((item) => (
            <button
              key={item.id}
              type='button'
              aria-pressed={protocol === item.id}
              onClick={() => setProtocol(item.id)}
              className='dev-builder-tab'
            >
              {item.label}
            </button>
          ))}
        </div>
      </div>
      <div className='dev-builder-row'>
        <label
          className='text-muted-foreground text-xs font-medium tracking-wide uppercase'
          htmlFor='dev-builder-model'
        >
          {t('Model')}
        </label>
        <input
          id='dev-builder-model'
          list='dev-builder-models'
          value={model}
          onChange={(event) => setModel(event.target.value)}
          className='dev-builder-input'
          placeholder='gpt-4o-mini'
        />
        <datalist id='dev-builder-models'>
          {MODEL_SUGGESTIONS.map((item) => (
            <option key={item} value={item} />
          ))}
        </datalist>
      </div>
      <div className='dev-builder-row'>
        <label
          className='text-muted-foreground text-xs font-medium tracking-wide uppercase'
          htmlFor='dev-builder-prompt'
        >
          {t('Prompt')}
        </label>
        <input
          id='dev-builder-prompt'
          value={prompt}
          maxLength={200}
          onChange={(event) => setPrompt(event.target.value)}
          className='dev-builder-input'
        />
      </div>
      <div className='dev-builder-row'>
        <span className='text-muted-foreground text-xs font-medium tracking-wide uppercase'>
          {t('Language')}
        </span>
        <div
          role='group'
          aria-label={t('Language')}
          className='dev-builder-tabs'
        >
          {LANGUAGES.map((item) => (
            <button
              key={item.id}
              type='button'
              aria-pressed={language === item.id}
              onClick={() => setLanguage(item.id)}
              className='dev-builder-tab'
            >
              {item.label}
            </button>
          ))}
        </div>
      </div>
      <div className='dev-builder-output'>
        <div className='dev-builder-output-head'>
          <code>{url}</code>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void copyToClipboard(snippet)}
            className='min-h-9 shrink-0'
          >
            <span aria-live='polite'>{copied ? t('Copied') : t('Copy')}</span>
          </Button>
        </div>
        <pre>
          <code>{snippet}</code>
        </pre>
      </div>
      <p className='text-muted-foreground text-xs leading-relaxed'>
        {t(
          'Nothing is sent. Copy the request into your own client and use your own key.'
        )}
      </p>
    </div>
  )
}
