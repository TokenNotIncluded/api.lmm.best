/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

import {
  LMM_ISSUER,
  LMM_SOURCE,
  OAUTH_PROMPT,
  PRICING_EXAMPLE,
  PRICING_PROMPT,
} from './integration-prompts'
import { RequestBuilder } from './request-builder'

export function IntegrationPrompt({
  prompt,
  label,
}: {
  prompt: string
  label: string
}) {
  const { t } = useTranslation()
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })
  const [failed, setFailed] = useState(false)
  const details = useRef<HTMLDetailsElement>(null)
  const field = useRef<HTMLTextAreaElement>(null)
  const copy = async () => {
    const copied = await copyToClipboard(prompt)
    setFailed(!copied)
    if (!copied) {
      if (details.current) details.current.open = true
      field.current?.focus()
      field.current?.select()
    }
  }
  return (
    <div className='min-w-0 space-y-3'>
      <Button className='min-h-11 w-full sm:w-auto' onClick={() => void copy()}>
        {copiedText === prompt ? t('Copied') : label}
      </Button>
      <p className='text-muted-foreground text-sm leading-relaxed'>
        {t(
          'Prompts contain technical instructions in English, never credentials.'
        )}
      </p>
      {failed && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Copy failed. Select and copy the prompt below.')}
        </p>
      )}
      <details ref={details} className='group'>
        <summary className='focus-visible:outline-ring min-h-11 cursor-pointer rounded-md py-3 text-sm underline underline-offset-4 focus-visible:outline-2 focus-visible:outline-offset-2'>
          {t('Preview prompt')}
        </summary>
        <textarea
          ref={field}
          readOnly
          value={prompt}
          rows={15}
          aria-label={`${t('Integration prompt')}: ${label}`}
          className='bg-background focus-visible:outline-ring mt-2 min-h-64 w-full resize-y rounded-lg border p-3 font-mono text-xs leading-6 focus-visible:outline-2 focus-visible:outline-offset-2'
          onFocus={(event) => event.currentTarget.select()}
        />
      </details>
      <span role='status' className='sr-only'>
        {copiedText === prompt ? t('Copied to clipboard') : ''}
      </span>
    </div>
  )
}

function PricingExample() {
  const { t } = useTranslation()
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })
  const [failed, setFailed] = useState(false)
  const copy = async () => setFailed(!(await copyToClipboard(PRICING_EXAMPLE)))
  const copied = copiedText === PRICING_EXAMPLE

  return (
    <div className='bg-muted/40 mt-3 min-w-0 overflow-hidden rounded-xl border'>
      <div className='flex items-center justify-between gap-3 border-b px-4 py-2'>
        <span className='text-muted-foreground text-xs'>JavaScript</span>
        <Button
          type='button'
          variant='ghost'
          className='min-h-11 shrink-0'
          aria-label={`${t('Copy')}: ${t('Fetch pricing from your backend.')}`}
          onClick={() => void copy()}
        >
          <span aria-live='polite'>{copied ? t('Copied') : t('Copy')}</span>
        </Button>
      </div>
      <pre
        tabIndex={0}
        aria-label={t('Fetch pricing from your backend.')}
        className='focus-visible:outline-ring max-w-full overflow-x-auto p-4 font-mono text-xs leading-7 focus-visible:outline-2 focus-visible:-outline-offset-2'
      >
        <code>{PRICING_EXAMPLE}</code>
      </pre>
      {failed && (
        <p role='alert' className='text-destructive px-4 pb-4 text-sm'>
          {t('Copy failed')}
        </p>
      )}
    </div>
  )
}

function Endpoint({
  method,
  path,
  label,
}: {
  method: string
  path: string
  label: string
}) {
  return (
    <div className='grid gap-2 py-4 sm:grid-cols-[9rem_minmax(0,1fr)]'>
      <dt className='text-muted-foreground text-sm leading-6'>{label}</dt>
      <dd className='min-w-0 font-mono text-xs leading-6 sm:text-sm'>
        <span className='mr-3 font-semibold'>{method}</span>
        <span className='[overflow-wrap:anywhere]'>{path}</span>
      </dd>
    </div>
  )
}

export function DevelopersPage() {
  const { t } = useTranslation()
  return (
    <main className='mx-auto w-full max-w-6xl px-4 py-6 sm:px-8 sm:py-16 lg:py-20'>
      <h1 className='text-3xl font-semibold tracking-tight sm:text-5xl'>
        {t('Developers')}
      </h1>
      <p className='mt-2 max-w-2xl text-base leading-snug text-balance sm:mt-4 sm:text-xl'>
        {t('One base URL. Any client.')}
      </p>
      <p className='text-muted-foreground mt-2 max-w-2xl text-sm leading-relaxed sm:mt-3 sm:text-base'>
        {t('Build a request below, then copy it into your project.')}
      </p>
      <nav
        aria-label={t('Integration guide')}
        className='mt-5 flex flex-nowrap gap-x-5 gap-y-1 overflow-x-auto border-y py-2 text-sm whitespace-nowrap sm:mt-8 sm:flex-wrap sm:gap-x-8 sm:overflow-visible sm:whitespace-normal'
      >
        <a className='dev-page-link' href='#request-builder'>
          {t('Request builder')}
        </a>
        <a className='dev-page-link' href='#pricing-api'>
          {t('Pricing API')}
        </a>
        <a className='dev-page-link' href='#oauth-integration'>
          {t('OAuth integration')}
        </a>
        <a className='dev-page-link' href='/webmcp'>
          WebMCP
        </a>
      </nav>

      <section
        id='request-builder'
        aria-labelledby='request-builder-title'
        className='mt-6 scroll-mt-24 sm:mt-12'
      >
        <h2
          id='request-builder-title'
          className='text-xl font-semibold sm:text-2xl'
        >
          {t('Request builder')}
        </h2>
        <p className='text-muted-foreground mt-2 max-w-prose text-sm leading-relaxed sm:mt-3 sm:text-base'>
          {t(
            'Pick a protocol and language. The snippet updates as you type and is never sent.'
          )}
        </p>
        <RequestBuilder />
      </section>

      <section
        id='pricing-api'
        aria-labelledby='pricing-api-title'
        className='mt-9 scroll-mt-24 border-t pt-6 sm:mt-12 sm:pt-9'
      >
        <h2 id='pricing-api-title' className='text-2xl font-semibold'>
          {t('Pricing API')}
        </h2>
        <div className='mt-4 grid gap-8 lg:grid-cols-[minmax(0,1fr)_19rem] lg:gap-14'>
          <div className='order-2 min-w-0 lg:order-1'>
            <p className='text-muted-foreground mt-3 max-w-prose leading-relaxed'>
              {t('Public prices follow the site visibility settings.')}
            </p>
            <dl className='mt-5 divide-y border-y'>
              <Endpoint
                method='GET'
                path='/api/pricing'
                label={t('Public pricing')}
              />
              <Endpoint
                method='GET'
                path='/api/oauth2/catalog'
                label={t('Account catalog')}
              />
            </dl>
            <p className='mt-5 text-sm leading-relaxed'>
              {t('Missing prices are unknown, not zero.')}
            </p>
            <h3 className='mt-6 text-sm font-semibold'>
              {t('Fetch pricing from your backend.')}
            </h3>
            <PricingExample />
            <a
              className='mt-4 inline-block text-sm underline underline-offset-4'
              href={`${LMM_SOURCE}/blob/main/apps/api-go/service/oauth_catalog.go`}
              target='_blank'
              rel='noopener noreferrer'
            >
              {t('Read the source contract')}
            </a>
          </div>
          <aside
            aria-label={t('Copy pricing prompt')}
            className='order-1 border-b pb-6 lg:order-2 lg:border-b-0 lg:border-l lg:pt-1 lg:pb-0 lg:pl-7'
          >
            <IntegrationPrompt
              prompt={PRICING_PROMPT}
              label={t('Copy pricing prompt')}
            />
          </aside>
        </div>
      </section>

      <section
        id='oauth-integration'
        aria-labelledby='oauth-integration-title'
        className='mt-9 scroll-mt-24 border-t pt-6 sm:mt-14 sm:pt-9'
      >
        <h2 id='oauth-integration-title' className='text-2xl font-semibold'>
          {t('OAuth integration')}
        </h2>
        <div className='mt-4 grid gap-8 lg:grid-cols-[minmax(0,1fr)_19rem] lg:gap-14'>
          <div className='order-2 min-w-0 lg:order-1'>
            <h3 className='mt-4 font-medium'>
              {t('Registered native clients')}
            </h3>
            <p className='text-muted-foreground mt-2 leading-relaxed'>
              {t('Authorization Code with PKCE (S256).')}
            </p>
            <p className='mt-4 rounded-xl border p-4 text-sm leading-relaxed'>
              {t(
                'No OpenID Connect sign-in yet: no userinfo endpoint, ID token, or public client registration.'
              )}
            </p>
            <dl className='mt-5 divide-y border-y'>
              <Endpoint
                method='GET'
                path='/.well-known/oauth-authorization-server'
                label={t('Discovery')}
              />
              <Endpoint
                method='GET'
                path='/api/oauth2/authorize'
                label={t('Authorization')}
              />
              <Endpoint
                method='POST'
                path='/api/oauth2/token'
                label={t('Token exchange')}
              />
              <Endpoint
                method='POST'
                path='/api/oauth2/revoke'
                label={t('Token revocation')}
              />
            </dl>
            <h3 className='mt-6 text-sm font-semibold'>
              {t('Approved client settings')}
            </h3>
            <p className='text-muted-foreground mt-2 text-sm leading-relaxed'>
              {t(
                'Client ID and scope profile must come from the server registry. Never reuse another application’s client ID.'
              )}
            </p>
            <div className='mt-5 flex flex-wrap gap-x-6 gap-y-3 text-sm underline underline-offset-4'>
              <a
                href={`${LMM_ISSUER}/.well-known/oauth-authorization-server`}
                target='_blank'
                rel='noopener noreferrer'
              >
                {t('Discovery')}
              </a>
              <a
                href={`${LMM_SOURCE}/blob/main/apps/api-go/service/oauth_server.go`}
                target='_blank'
                rel='noopener noreferrer'
              >
                {t('Read the source contract')}
              </a>
            </div>
          </div>
          <aside
            aria-label={t('Copy OAuth prompt')}
            className='order-1 border-b pb-6 lg:order-2 lg:border-b-0 lg:border-l lg:pt-1 lg:pb-0 lg:pl-7'
          >
            <IntegrationPrompt
              prompt={OAUTH_PROMPT}
              label={t('Copy OAuth prompt')}
            />
          </aside>
        </div>
      </section>
    </main>
  )
}
