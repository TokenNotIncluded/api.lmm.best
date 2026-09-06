/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

const STEPS = [
  [
    'Account and access',
    'Create an account and request developer access. Approval is required before purchasing.',
  ],
  [
    'Compare before you commit',
    'After approval, compare model rates and the plans available to your account.',
  ],
  [
    'Choose your first top-up',
    'Review the credit, payment currency and final amount, then connect your client.',
  ],
] as const

export function PurchaseJourney() {
  const { t } = useTranslation()

  return (
    <ol className='grid gap-6 md:grid-cols-3 md:gap-8'>
      {STEPS.map(([title, description], index) => (
        <li key={title} className='flex min-w-0 gap-4'>
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
        </li>
      ))}
    </ol>
  )
}
