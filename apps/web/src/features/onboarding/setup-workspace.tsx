/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { Link } from '@tanstack/react-router'
import { ArrowUpRight, Check, KeyRound } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
import { PiOAuthGuide } from '@/features/guide/pi-oauth-guide'
import { ApiBaseUrl } from '@/features/keys/components/api-base-url'
import { openTestKeyWindow } from '@/features/test-key/bookmarklet'
import { BookmarkletInstall } from '@/features/test-key/bookmarklet-install'
import { useTestKeyCopy } from '@/features/test-key/copy'
import { getOnboardingState } from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

export function SetupWorkspace() {
  const { t } = useTranslation()
  const q = useTestKeyCopy()
  const user = useAuthStore((state) => state.auth.user)
  const onboarding = getOnboardingState(user)
  return (
    <div className='mx-auto w-full max-w-4xl space-y-8 pb-10 sm:space-y-10'>
      <header className='pt-2 sm:pt-6'>
        <p className='text-muted-foreground mb-3 text-xs font-medium'>
          {t(
            onboarding.stage === 'complete'
              ? 'Setup complete'
              : 'API access enabled'
          )}
        </p>
        <h2 className='text-3xl font-semibold tracking-tight sm:text-4xl'>
          {q('setupTitle')}
        </h2>
        <p className='text-muted-foreground mt-3 max-w-xl text-sm leading-6'>
          {q('setupBody')}
        </p>
      </header>
      <ol className='divide-y rounded-xl border px-5 sm:px-7'>
        <li className='grid gap-3 py-6 sm:grid-cols-[2rem_1fr] sm:gap-5'>
          <span
            className='text-muted-foreground font-mono text-sm'
            aria-hidden='true'
          >
            01
          </span>
          <div>
            <h3 className='flex items-center gap-2 font-medium'>
              {q('keyStep')}
              {onboarding.credentialComplete && (
                <Check size={16} aria-label={t('Completed')} />
              )}
            </h3>
            <p className='text-muted-foreground mt-2 text-sm leading-6'>
              {q('keyBody')}
            </p>
            <div className='mt-4 flex flex-wrap gap-2'>
              <Button className='min-h-11' onClick={openTestKeyWindow}>
                <KeyRound aria-hidden='true' />
                {q('title')}
              </Button>
              <Button
                className='min-h-11'
                variant='outline'
                render={<Link to='/keys' />}
              >
                {t('API Keys')}
              </Button>
            </div>
          </div>
        </li>
        <li className='grid gap-3 py-6 sm:grid-cols-[2rem_1fr] sm:gap-5'>
          <span
            className='text-muted-foreground font-mono text-sm'
            aria-hidden='true'
          >
            02
          </span>
          <div className='min-w-0'>
            <h3 className='mb-4 font-medium'>{q('addressStep')}</h3>
            <ApiBaseUrl />
          </div>
        </li>
        <li className='grid gap-3 py-6 sm:grid-cols-[2rem_1fr] sm:gap-5'>
          <span
            className='text-muted-foreground font-mono text-sm'
            aria-hidden='true'
          >
            03
          </span>
          <div>
            <h3 className='font-medium'>{q('clientStep')}</h3>
            <p className='text-muted-foreground mt-2 text-sm leading-6'>
              {q('clientBody')}
            </p>
            <div className='mt-4 flex flex-wrap gap-2'>
              <Button
                className='min-h-11'
                variant='outline'
                render={<Link to='/guide' />}
              >
                {t('Setup guide')}
                <ArrowUpRight aria-hidden='true' />
              </Button>
              <Button
                className='min-h-11'
                variant='ghost'
                render={<Link to='/pricing' />}
              >
                {t('Models and pricing')}
              </Button>
            </div>
          </div>
        </li>
      </ol>
      <BookmarkletInstall compact />
      <details className='border-y py-2'>
        <summary className='min-h-11 cursor-pointer py-3 text-sm font-medium'>
          {t('Sign in with OAuth')}
        </summary>
        <div className='pt-3 pb-5'>
          <PiOAuthGuide />
        </div>
      </details>
      <footer className='flex flex-wrap items-center justify-between gap-3'>
        <div className='flex flex-wrap items-center gap-1'>
          <span className='text-muted-foreground text-sm'>{q('help')}</span>
          <Button
            variant='link'
            onClick={() => requestAssistantOpen('client-setup')}
          >
            {t('Ask AI assistant')}
          </Button>
        </div>
        <Button variant='ghost' render={<Link to='/dashboard' />}>
          {t('Dashboard')}
          <ArrowUpRight aria-hidden='true' />
        </Button>
      </footer>
    </div>
  )
}
