/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import {
  ArrowUpRight,
  CheckCircle2,
  Info,
  RefreshCw,
  Search,
} from 'lucide-react'
import { motion, useReducedMotion } from 'motion/react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { RepositoryLink } from '@/features/repositories/repository-link'
import { cn } from '@/lib/utils'

import {
  getWebMcpContext,
  listWebMcpTools,
  type WebMcpToolSummary,
} from './index'
import { groupTools, isReadOnly } from './tool-groups'

type Filter = 'all' | 'readonly' | 'interactive' | 'confirm'

function ToolRow({ tool }: { tool: WebMcpToolSummary }) {
  const { t } = useTranslation()
  const readOnly = isReadOnly(tool)
  return (
    <div
      className={cn(
        'group flex flex-col gap-2 rounded-lg border border-border/60 p-3 transition-colors',
        'hover:border-primary/40 hover:bg-muted/40'
      )}
    >
      <div className='flex items-start gap-2'>
        <code className='min-w-0 flex-1 text-xs font-medium break-all'>
          {tool.name}
        </code>
        <CopyButton
          value={tool.name}
          tooltip={t('Copy tool name')}
          successTooltip={t('Copied')}
          className='-mt-1 -mr-1 size-11 sm:size-8'
          iconClassName='size-3.5'
        />
      </div>
      <p className='text-muted-foreground text-sm leading-relaxed'>
        {t(tool.title)}
      </p>
      <Badge
        variant={readOnly ? 'secondary' : 'outline'}
        className={cn(
          'w-fit text-[0.6875rem]',
          readOnly
            ? 'text-success'
            : tool.consequential
              ? 'text-warning'
              : 'text-muted-foreground'
        )}
      >
        {readOnly
          ? t('Read-only')
          : tool.consequential
            ? t('Asks for confirmation')
            : t('Page interaction')}
      </Badge>
    </div>
  )
}

export function WebMcpPage() {
  const { t } = useTranslation()
  const shouldReduce = useReducedMotion()
  const [supported, setSupported] = useState(() => !!getWebMcpContext())
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<Filter>('all')
  const tools = useMemo(() => listWebMcpTools(), [])
  const groups = useMemo(() => groupTools(tools, query, t), [tools, query, t])
  const visible = useMemo(
    () =>
      groups
        .map((group) => ({
          ...group,
          tools: group.tools.filter((tool) =>
            filter === 'all'
              ? true
              : filter === 'readonly'
                ? isReadOnly(tool)
                : filter === 'confirm'
                  ? tool.consequential
                  : !isReadOnly(tool) && !tool.consequential
          ),
        }))
        .filter((group) => group.tools.length > 0),
    [groups, filter]
  )
  const total = visible.reduce((sum, group) => sum + group.tools.length, 0)
  const readOnlyCount = tools.filter(isReadOnly).length
  const consequentialCount = tools.filter((tool) => tool.consequential).length
  const filters: Array<{ id: Filter; label: string; count: number }> = [
    { id: 'all', label: t('All'), count: tools.length },
    { id: 'readonly', label: t('Read-only'), count: readOnlyCount },
    {
      id: 'interactive',
      label: t('Page interaction'),
      count: tools.length - readOnlyCount - consequentialCount,
    },
    {
      id: 'confirm',
      label: t('Asks for confirmation'),
      count: consequentialCount,
    },
  ]

  return (
    <main className='mx-auto w-full max-w-5xl px-5 py-12 sm:px-8 sm:py-16'>
      <header>
        <h1 className='text-4xl font-semibold tracking-tight sm:text-5xl'>
          WebMCP
        </h1>
        <p className='mt-3 max-w-2xl text-lg leading-relaxed'>
          {t('Let your browser agent work with LMM.')}
        </p>
        <div className='mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 text-sm'>
          <span
            className='inline-flex items-center gap-1.5'
            role='status'
            aria-live='polite'
          >
            {supported ? (
              <CheckCircle2
                className='text-success size-4 shrink-0'
                aria-hidden='true'
              />
            ) : (
              <Info
                className='text-muted-foreground size-4 shrink-0'
                aria-hidden='true'
              />
            )}
            <span className='text-muted-foreground'>
              {supported
                ? t('WebMCP is available in this browser.')
                : t('Not available in this browser. The site still works.')}
            </span>
          </span>
          <Button
            variant='ghost'
            size='sm'
            className='min-h-11 px-2 text-xs'
            onClick={() => setSupported(!!getWebMcpContext())}
          >
            <RefreshCw className='mr-1.5 size-3.5' />
            {t('Check again')}
          </Button>
          <a
            href='https://developer.chrome.com/docs/ai/webmcp/imperative-api'
            target='_blank'
            rel='noopener noreferrer'
            className='inline-flex items-center gap-1 underline underline-offset-4'
          >
            {t('WebMCP documentation')}
            <ArrowUpRight className='size-3.5' aria-hidden='true' />
          </a>
        </div>
      </header>

      <div className='mt-8 flex flex-col gap-3 sm:flex-row sm:items-center'>
        <div className='relative flex-1'>
          <Search
            className='text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2'
            aria-hidden='true'
          />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t('Search tools')}
            aria-label={t('Search tools')}
            className='pl-9'
          />
        </div>
        <div
          className='flex flex-wrap gap-1.5'
          role='group'
          aria-label={t('Filter tools')}
        >
          {filters.map((item) => (
            <button
              key={item.id}
              type='button'
              aria-pressed={filter === item.id}
              onClick={() => setFilter(item.id)}
              className={cn(
                'inline-flex min-h-11 items-center gap-1.5 rounded-md border px-3 text-sm transition-colors',
                filter === item.id
                  ? 'border-primary/40 bg-primary/10 text-foreground'
                  : 'border-border text-muted-foreground hover:bg-muted/60'
              )}
            >
              {item.label}
              <span className='tabular-nums opacity-60'>{item.count}</span>
            </button>
          ))}
        </div>
      </div>

      <p className='text-muted-foreground mt-3 text-xs'>
        {t('{{count}} tools listed', { count: total })}
      </p>

      {visible.length === 0 ? (
        <div className='mt-8 rounded-lg border border-dashed p-8 text-center'>
          <p className='font-medium'>{t('No tool matches that search')}</p>
          <Button
            variant='outline'
            size='sm'
            className='mt-4'
            onClick={() => {
              setQuery('')
              setFilter('all')
            }}
          >
            {t('Clear filters')}
          </Button>
        </div>
      ) : (
        <div className='mt-6 space-y-8'>
          {visible.map((group) => (
            <motion.section
              key={group.key}
              initial={shouldReduce ? false : { opacity: 0, y: 8 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.25, ease: 'easeOut' }}
            >
              <h2 className='flex items-baseline gap-2 text-sm font-semibold tracking-wide uppercase'>
                {t(group.label)}
                <span className='text-muted-foreground text-xs font-normal tabular-nums'>
                  {group.tools.length}
                </span>
              </h2>
              <div className='mt-3 grid gap-3 sm:grid-cols-2'>
                {group.tools.map((tool) => (
                  <ToolRow key={tool.name} tool={tool} />
                ))}
              </div>
            </motion.section>
          ))}
        </div>
      )}

      <section className='border-border/60 bg-muted/30 mt-12 rounded-lg border p-5'>
        <h2 className='text-sm font-semibold'>{t('Clear boundaries')}</h2>
        <p className='text-muted-foreground mt-2 text-sm leading-relaxed'>
          {t(
            'API keys, passwords, payments, and script execution are not exposed through these tools.'
          )}
        </p>
      </section>

      <div className='mt-10 flex flex-wrap items-center gap-4 border-t pt-6'>
        <RepositoryLink kind='project' />
        <a className='text-sm underline underline-offset-4' href='/scripts'>
          {t('Public scripts')}
        </a>
        <a
          className='text-sm underline underline-offset-4'
          href='/games/signal#ai-guide'
        >
          {t('Let an AI play through WebMCP')}
        </a>
      </div>
    </main>
  )
}
