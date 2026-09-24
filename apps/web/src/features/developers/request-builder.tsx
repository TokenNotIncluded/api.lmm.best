/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { getPricing } from '@/features/pricing/api'
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

const MODEL_OPTIONS: Readonly<
  Record<
    Protocol,
    {
      endpoint: string
      fallback: readonly string[]
      preferred: readonly string[]
    }
  >
> = {
  chat: {
    endpoint: 'openai',
    fallback: ['gpt-5.6-sol', 'gpt-5.6-luna'],
    preferred: ['gpt-6-astra', 'gpt-5.6-sol', 'gpt-5.6-luna'],
  },
  messages: {
    endpoint: 'anthropic',
    fallback: ['claude-opus-4-6', 'claude-sonnet-4-6'],
    preferred: [
      'claude-opus-5',
      'claude-opus-4-8',
      'claude-opus-4-6',
      'claude-sonnet-4-6',
    ],
  },
  gemini: {
    endpoint: 'gemini',
    fallback: ['gemini-3.1-pro-preview', 'gemini-3-flash-preview'],
    preferred: [
      'gemini-3.1-pro-preview',
      'gemini-3-flash-preview',
      'gemini-3-pro-preview',
    ],
  },
}

function sortModels(
  models: string[],
  preferred: readonly string[]
): string[] {
  const rank = new Map(preferred.map((name, index) => [name, index]))
  return models.sort((left, right) => {
    const leftRank = rank.get(left) ?? Number.MAX_SAFE_INTEGER
    const rightRank = rank.get(right) ?? Number.MAX_SAFE_INTEGER
    if (leftRank !== rightRank) return leftRank - rightRank
    return left.localeCompare(right, undefined, {
      numeric: true,
      sensitivity: 'base',
    })
  })
}

/**
 * Build a real request against api.lmm.best without sending the prompt. Model
 * choices come from the live public pricing catalogue, filtered by endpoint
 * compatibility, so the examples do not age into hard-coded legacy models.
 */
export function RequestBuilder() {
  const { t } = useTranslation()
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })
  const [protocol, setProtocol] = useState<Protocol>('chat')
  const [language, setLanguage] = useState<Language>('curl')
  const [model, setModel] = useState(MODEL_OPTIONS.chat.fallback[0])
  const [prompt, setPrompt] = useState('Explain LMM in one sentence.')

  const pricingQuery = useQuery({
    queryKey: ['pricing'],
    queryFn: ({ signal }) => getPricing(signal),
    staleTime: 5 * 60_000,
  })

  const modelOptions = useMemo(() => {
    const config = MODEL_OPTIONS[protocol]
    const liveModels =
      pricingQuery.data?.data
        .filter((item) =>
          item.supported_endpoint_types?.includes(config.endpoint)
        )
        .map((item) => item.model_name.trim())
        .filter(Boolean) ?? []

    const uniqueModels = [...new Set(liveModels)]
    return uniqueModels.length > 0
      ? sortModels(uniqueModels, config.preferred)
      : [...config.fallback]
  }, [pricingQuery.data?.data, protocol])

  useEffect(() => {
    if (!modelOptions.includes(model)) {
      setModel(modelOptions[0])
    }
  }, [model, modelOptions])

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
        <select
          id='dev-builder-model'
          value={model}
          onChange={(event) => setModel(event.target.value)}
          className='dev-builder-input'
        >
          {modelOptions.map((item) => (
            <option key={item} value={item}>
              {item}
            </option>
          ))}
        </select>
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
        {pricingQuery.isError
          ? t(
              'Your prompt is never sent. The live model catalog is unavailable, so fallback models are shown.'
            )
          : t(
              'Your prompt is never sent. Model choices come from the live public catalog.'
            )}
      </p>
    </div>
  )
}
