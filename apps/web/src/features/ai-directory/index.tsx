/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ArrowUpRight, Globe2, Search, Settings2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout/components/section-page-layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { getLobeIcon } from '@/lib/lobe-icon'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  AI_DIRECTORY_CATEGORIES,
  getAIDirectory,
  safeDirectoryUrl,
  type AIDirectoryCategory,
  type AIDirectoryLink,
} from './api'

import './style.css'

const iconByID: Record<string, string> = {
  chatgpt: 'OpenAI',
  gemini: 'Gemini.Color',
  grok: 'Grok',
  deepseek: 'DeepSeek.Color',
  claude: 'Claude.Color',
  perplexity: 'Perplexity.Color',
  copilot: 'Copilot.Color',
  kimi: 'Kimi.Color',
  qwen: 'Qwen.Color',
  mistral: 'Mistral.Color',
  huggingface: 'HuggingFace.Color',
}

const categoryLabels: Record<AIDirectoryCategory, string> = {
  chat: 'Chat assistants',
  research: 'Research',
  developer: 'Developer tools',
  creative: 'Creative tools',
  other: 'More websites',
}

function DirectoryItem({ item }: { item: AIDirectoryLink }) {
  const { t } = useTranslation()
  const href = safeDirectoryUrl(item.url)
  if (!href) return null
  const hostname = new URL(href).hostname.replace(/^www\./, '')
  const icon = iconByID[item.id]

  return (
    <article className='ai-directory-item'>
      <div className='ai-directory-item-top'>
        <span className='ai-directory-mark' aria-hidden='true'>
          {icon ? getLobeIcon(icon, 27) : item.name.charAt(0).toUpperCase()}
        </span>
        <div className='min-w-0 flex-1'>
          <h3 className='ai-directory-name'>{item.name}</h3>
          <span className='ai-directory-domain'>{hostname}</span>
        </div>
        <a
          className='ai-directory-visit'
          href={href}
          target='_blank'
          rel='noopener noreferrer'
          aria-label={t('Open {{name}} in a new tab', { name: item.name })}
        >
          <ArrowUpRight size={18} aria-hidden='true' />
        </a>
      </div>
      {item.summary && (
        <p className='ai-directory-summary'>{t(item.summary)}</p>
      )}
      {item.description && (
        <details className='ai-directory-details'>
          <summary>{t('Read description')}</summary>
          <p>{t(item.description)}</p>
        </details>
      )}
    </article>
  )
}

export function AIDirectory() {
  const { t } = useTranslation()
  const isOwner = useAuthStore(
    (state) => (state.auth.user?.role ?? 0) >= ROLE.SUPER_ADMIN
  )
  const [search, setSearch] = useState('')
  const [category, setCategory] = useState<AIDirectoryCategory | 'all'>('all')
  const query = useQuery({
    queryKey: ['ai-directory'],
    queryFn: getAIDirectory,
    staleTime: 60_000,
  })
  const visible = useMemo(() => {
    const term = search.trim().toLocaleLowerCase()
    return (query.data ?? []).filter((item) => {
      if (!item.enabled || !safeDirectoryUrl(item.url)) return false
      if (category !== 'all' && item.category !== category) return false
      if (!term) return true
      return [item.name, item.summary, item.description, item.url]
        .join(' ')
        .toLocaleLowerCase()
        .includes(term)
    })
  }, [category, query.data, search])
  const categoryCounts = useMemo(() => {
    const result = Object.fromEntries(
      AI_DIRECTORY_CATEGORIES.map((value) => [value, 0])
    ) as Record<AIDirectoryCategory, number>
    for (const item of query.data ?? []) {
      if (
        item.enabled &&
        safeDirectoryUrl(item.url) &&
        item.category in result
      ) {
        result[item.category]++
      }
    }
    return result
  }, [query.data])

  return (
    <SectionPageLayout className='ai-directory-page'>
      <SectionPageLayout.Title>{t('AI directory')}</SectionPageLayout.Title>
      {isOwner && (
        <SectionPageLayout.Actions>
          <Button
            variant='outline'
            size='sm'
            render={
              <Link
                to='/system-settings/site/$section'
                params={{ section: 'ai-directory' }}
              />
            }
          >
            <Settings2 data-icon='inline-start' />
            {t('Manage websites')}
          </Button>
        </SectionPageLayout.Actions>
      )}
      <SectionPageLayout.Content>
        <div className='ai-directory-shell'>
          <div className='ai-directory-intro'>
            <div>
              <h2>{t('Explore AI websites')}</h2>
              <p>
                {t(
                  'A curated starting point for the AI tools you use every day.'
                )}
              </p>
            </div>
            <div className='ai-directory-intro-symbol' aria-hidden='true'>
              <Globe2 strokeWidth={1.2} />
            </div>
          </div>

          <div className='ai-directory-tools'>
            <label className='ai-directory-search'>
              <Search size={18} aria-hidden='true' />
              <span className='sr-only'>{t('Search websites')}</span>
              <Input
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder={t('Search websites')}
                type='search'
              />
            </label>
            <div
              className='ai-directory-filters'
              role='group'
              aria-label={t('Filter websites')}
            >
              <button
                type='button'
                aria-pressed={category === 'all'}
                onClick={() => setCategory('all')}
              >
                {t('All websites')}
              </button>
              {AI_DIRECTORY_CATEGORIES.filter(
                (value) => categoryCounts[value] > 0
              ).map((value) => (
                <button
                  key={value}
                  type='button'
                  aria-pressed={category === value}
                  onClick={() => setCategory(value)}
                >
                  {t(categoryLabels[value])}
                </button>
              ))}
            </div>
          </div>

          {query.isPending && (
            <p className='ai-directory-state'>{t('Loading websites...')}</p>
          )}
          {query.isError && (
            <ErrorState
              title={t('Unable to load websites')}
              description={t('Try loading the directory again.')}
              onRetry={() => void query.refetch()}
            />
          )}
          {query.isSuccess && visible.length === 0 && (
            <div className='ai-directory-state'>
              <p>{t('No websites match your search.')}</p>
              {(search || category !== 'all') && (
                <Button
                  variant='link'
                  onClick={() => {
                    setSearch('')
                    setCategory('all')
                  }}
                >
                  {t('Clear filters')}
                </Button>
              )}
            </div>
          )}
          {query.isSuccess && visible.length > 0 && (
            <div className='ai-directory-groups'>
              {AI_DIRECTORY_CATEGORIES.map((value) => {
                const items = visible.filter((item) => item.category === value)
                if (!items.length) return null
                return (
                  <section key={value} className='ai-directory-group'>
                    <div className='ai-directory-group-heading'>
                      <h2>{t(categoryLabels[value])}</h2>
                      <span>{items.length}</span>
                    </div>
                    <div className='ai-directory-grid'>
                      {items.map((item) => (
                        <DirectoryItem item={item} key={item.id} />
                      ))}
                    </div>
                  </section>
                )
              })}
            </div>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
