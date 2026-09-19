/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useQuery } from '@tanstack/react-query'
import { ArrowUpRight, Code2, Star } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { cn } from '@/lib/utils'

import {
  fetchRepositoryStars,
  REPOSITORIES,
  repositoryUrl,
  type RepositoryKind,
} from './api'

export function RepositoryLink({
  kind,
  className,
}: {
  kind: RepositoryKind
  className?: string
}) {
  const { t, i18n } = useTranslation()
  const stars = useQuery({
    queryKey: ['github-stars', REPOSITORIES[kind]],
    queryFn: ({ signal }) => fetchRepositoryStars(kind, signal),
    staleTime: 15 * 60 * 1000,
    gcTime: 60 * 60 * 1000,
    retry: false,
  })
  const count =
    stars.data === undefined
      ? '—'
      : new Intl.NumberFormat(toIntlLocale(i18n.resolvedLanguage)).format(
          stars.data
        )
  return (
    <a
      href={repositoryUrl(kind)}
      target='_blank'
      rel='noopener noreferrer'
      className={cn(
        'inline-flex min-h-10 max-w-full flex-wrap items-center gap-x-3 gap-y-1 rounded-md border border-current/20 px-3 py-2 text-sm transition-colors hover:bg-current/5 focus-visible:outline-2 focus-visible:outline-offset-4',
        className
      )}
    >
      <Code2 className='size-4 shrink-0' aria-hidden='true' />
      <span>{t('View source on GitHub')}</span>
      <span
        className='inline-flex items-center gap-1.5 border-l border-current/20 pl-3 tabular-nums'
        title={
          stars.data === undefined
            ? t('Star count unavailable')
            : t('GitHub stars')
        }
      >
        <Star className='size-3.5' aria-hidden='true' />
        <span
          aria-label={
            stars.data === undefined
              ? t('Star count unavailable')
              : `${count} ${t('GitHub stars')}`
          }
        >
          {count}
        </span>
      </span>
      <ArrowUpRight className='size-3.5' aria-hidden='true' />
    </a>
  )
}
