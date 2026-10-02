/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { Link } from '@tanstack/react-router'
import { ArrowRight, Globe2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

export function HomeDirectoryLink() {
  const { t } = useTranslation()

  return (
    <aside
      className='mx-auto w-full max-w-7xl px-4 pt-2 sm:px-6 sm:pt-4'
      aria-label={t('AI directory')}
    >
      <Link
        to='/ai-directory'
        className='border-border bg-card text-card-foreground hover:bg-accent focus-visible:ring-ring flex min-h-11 items-center gap-3 rounded-lg border px-3 py-2 transition-colors focus-visible:ring-2 focus-visible:outline-none sm:gap-4 sm:rounded-2xl sm:px-6 sm:py-4'
      >
        <Globe2
          className='text-primary size-5 shrink-0 sm:size-7'
          aria-hidden='true'
        />
        <div className='min-w-0 flex-1'>
          <strong className='block text-sm sm:text-base'>
            {t('AI directory')}
          </strong>
          <span className='text-muted-foreground mt-1 hidden text-sm sm:block'>
            {t('Explore AI websites')}
            {' · '}
            {t('Chat assistants')}
            {' · '}
            {t('Developer tools')}
          </span>
        </div>
        <ArrowRight className='size-5 shrink-0' aria-hidden='true' />
      </Link>
    </aside>
  )
}
