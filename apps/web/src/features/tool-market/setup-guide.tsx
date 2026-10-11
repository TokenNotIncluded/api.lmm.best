/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

export function MarketUsageGuide() {
  const { t } = useTranslation()
  const steps = [
    {
      title: t('Choose a client'),
      description: t(
        'Choose Current browser to try a tool here, or connect your MCP client.'
      ),
    },
    {
      title: t('Add and authorize tool'),
      description: t(
        'Open a service, then add and authorize a tool. Review permissions and limits; loading is included.'
      ),
    },
    {
      title: t('Run tool'),
      description: t(
        'Run it here, or refresh your MCP client tool list. A connection token alone does not authorize tools.'
      ),
    },
  ]
  return (
    <section
      aria-label={t('Use a tool in 3 steps')}
      className='space-y-3 border-b pb-5'
    >
      <h3 className='text-sm font-semibold'>{t('Use a tool in 3 steps')}</h3>
      <ol className='grid gap-4 md:grid-cols-3'>
        {steps.map((step, index) => (
          <li key={step.title} className='flex min-w-0 gap-3'>
            <span
              aria-hidden='true'
              className='bg-muted flex size-6 shrink-0 items-center justify-center rounded-full text-xs font-medium'
            >
              {index + 1}
            </span>
            <div className='min-w-0 space-y-1'>
              <p className='text-sm font-medium'>{step.title}</p>
              <p className='text-muted-foreground text-xs leading-5'>
                {step.description}
              </p>
            </div>
          </li>
        ))}
      </ol>
    </section>
  )
}

export function MarketNextStep({
  id,
  children,
}: {
  id?: string
  children: ReactNode
}) {
  const { t } = useTranslation()
  return (
    <div
      id={id}
      role='status'
      aria-live='polite'
      className='basis-full space-y-1 text-sm'
    >
      <p className='font-medium'>{t('Next step')}</p>
      <p className='text-muted-foreground leading-6'>{children}</p>
    </div>
  )
}
