/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { ArrowUpRight, CheckCircle2, Info, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { RepositoryLink } from '@/features/repositories/repository-link'

import { getWebMcpContext, WEBMCP_TOOL_DESCRIPTIONS } from './index'

export function WebMcpPage() {
  const { t } = useTranslation()
  const [supported, setSupported] = useState(() => !!getWebMcpContext())
  return (
    <main className='mx-auto w-full max-w-5xl px-5 py-12 sm:px-8 sm:py-20'>
      <a
        href='/'
        className='text-muted-foreground text-sm underline underline-offset-4'
      >
        {t('Back to home')}
      </a>
      <h1 className='mt-7 text-4xl font-semibold tracking-tight sm:text-5xl'>
        WebMCP
      </h1>
      <p className='mt-4 max-w-2xl text-xl leading-relaxed'>
        {t('Let your browser agent work with LMM.')}
      </p>
      <p className='text-muted-foreground mt-3 max-w-2xl leading-relaxed'>
        {t(
          'Discover site information, model prices, public scripts, and account status through structured browser tools.'
        )}
      </p>
      <a href='/games/signal#ai-guide' className='mt-5 inline-block underline underline-offset-4'>{t('Let an AI play through WebMCP')}</a>
      <div className='mt-10 grid gap-10 lg:grid-cols-[minmax(0,2fr)_minmax(15rem,1fr)]'>
        <section aria-labelledby='webmcp-tools-title'>
          <h2 id='webmcp-tools-title' className='text-xl font-semibold'>
            {t('Available tools')}
          </h2>
          <dl className='mt-4 divide-y border-y'>
            {WEBMCP_TOOL_DESCRIPTIONS.map(([name, description]) => (
              <div className='py-5' key={name}>
                <dt className='text-sm font-medium'>
                  <code className='break-all'>{name}</code>
                </dt>
                <dd className='text-muted-foreground mt-1.5 text-sm leading-relaxed'>
                  {t(description)}
                </dd>
              </div>
            ))}
          </dl>
        </section>
        <aside className='space-y-8'>
          <section>
            <h2 className='text-lg font-semibold'>{t('Browser support')}</h2>
            <div className='mt-4 flex items-start gap-3' role='status'>
              {supported ? (
                <CheckCircle2
                  className='text-success mt-0.5 size-5 shrink-0'
                  aria-hidden='true'
                />
              ) : (
                <Info
                  className='text-muted-foreground mt-0.5 size-5 shrink-0'
                  aria-hidden='true'
                />
              )}
              <p className='text-sm leading-relaxed'>
                {supported
                  ? t('WebMCP is available in this browser.')
                  : t(
                      'WebMCP is not available in this browser. The normal interface still works.'
                    )}
              </p>
            </div>
            <Button
              className='mt-4'
              variant='outline'
              onClick={() => setSupported(!!getWebMcpContext())}
            >
              <RefreshCw className='mr-2 size-4' />
              {t('Check browser support')}
            </Button>
          </section>
          <section className='border-t pt-6'>
            <h2 className='text-lg font-semibold'>{t('Clear boundaries')}</h2>
            <p className='text-muted-foreground mt-3 text-sm leading-relaxed'>
              {t(
                'API keys, passwords, payments, and script execution are not exposed through these tools.'
              )}
            </p>
            <a
              href='https://developer.chrome.com/docs/ai/webmcp/imperative-api'
              target='_blank'
              rel='noopener noreferrer'
              className='mt-4 inline-flex items-center gap-1.5 text-sm underline underline-offset-4'
            >
              {t('WebMCP documentation')}
              <ArrowUpRight className='size-4' aria-hidden='true' />
            </a>
          </section>
        </aside>
      </div>
      <div className='mt-10 flex flex-wrap items-center gap-4 border-t pt-6'>
        <RepositoryLink kind='project' />
        <a className='text-sm underline underline-offset-4' href='/scripts'>
          {t('Public scripts')}
        </a>
      </div>
    </main>
  )
}
