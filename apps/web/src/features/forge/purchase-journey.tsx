/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { Link } from '@tanstack/react-router'
import { ArrowRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { isConsoleActivated } from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

const STEPS = [
  [
    'Create an account',
    'Sign up in under a minute. No card needed.',
    { to: '/sign-up' as const, label: 'Create an account' },
  ],
  [
    'Top up credit',
    'Any amount works. A successful top-up unlocks developer access (L1) automatically.',
    { to: '/wallet' as const, label: 'Open wallet' },
  ],
  [
    'Copy your key',
    'Create an API key and paste it into your client, or connect with OAuth instead.',
    { to: '/keys' as const, label: 'Create an API key' },
  ],
] as const

export function PurchaseJourney() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const activated = isConsoleActivated(user)

  return (
    <div className='space-y-6'>
      {/* The single action this panel exists for: never let it hide. */}
      <Button
        size='lg'
        render={
          <Link
            to={user ? '/wallet' : '/sign-in'}
            search={user ? undefined : { redirect: '/wallet' }}
          />
        }
      >
        {!user
          ? t('Sign in to top up')
          : activated
            ? t('Top up credit')
            : t('Top up to unlock access')}
        <ArrowRight data-icon='inline-end' />
      </Button>
      <ol className='grid gap-6 md:grid-cols-3 md:gap-8'>
        {STEPS.map(([title, description, action], index) => (
          <li key={title} className='flex min-w-0 flex-col gap-3'>
            <div className='flex min-w-0 gap-4'>
              <span
                className='text-muted-foreground border-border flex size-9 shrink-0 items-center justify-center rounded-full border font-mono text-sm'
                aria-hidden='true'
              >
                {index + 1}
              </span>
              <div>
                <h3 className='text-base font-semibold'>{t(title)}</h3>
                <p className='text-muted-foreground mt-2 text-sm leading-6'>
                  {t(description)}
                </p>
              </div>
            </div>
            <Link
              to={action.to}
              className='lmm-text-link self-end font-medium tracking-tight'
            >
              {t(action.label)}
              <ArrowRight aria-hidden='true' />
            </Link>
          </li>
        ))}
      </ol>
    </div>
  )
}
