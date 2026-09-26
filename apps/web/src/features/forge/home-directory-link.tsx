/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { Link } from '@tanstack/react-router'
import { ArrowRight, Globe2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

export function HomeDirectoryLink() {
  const { t } = useTranslation()

  return (
    <aside
      className='mx-auto w-full max-w-7xl px-4 pt-4 sm:px-6'
      aria-label={t('AI directory')}
    >
      <Link
        to='/ai-directory'
        className='border-border bg-card text-card-foreground hover:bg-accent focus-visible:ring-ring flex items-center gap-4 rounded-2xl border px-4 py-4 transition-colors focus-visible:ring-2 focus-visible:outline-none sm:px-6'
      >
        <Globe2 className='text-primary size-7 shrink-0' aria-hidden='true' />
        <div className='min-w-0 flex-1'>
          <strong className='block text-base'>{t('AI directory')}</strong>
          <span className='text-muted-foreground mt-1 block text-sm'>
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
