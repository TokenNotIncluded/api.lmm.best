/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'

import { Button } from '@/components/ui/button'

import { marketEndpoint } from './connection-utils'
import { useMarketTranslation as useTranslation } from './provider-i18n'

export function marketOAuthCommand(endpoint: string): string {
  const url = new URL(endpoint)
  const modes = url.searchParams.getAll('mode')
  if (
    !/^https?:\/\/[a-zA-Z0-9.:[\]-]+\/[a-zA-Z0-9/_-]+(?:\?mode=(?:compact|full))?$/.test(
      endpoint
    ) ||
    `${url.origin}${url.pathname}` !==
      marketEndpoint(url.origin, url.pathname) ||
    (url.search !== '' &&
      (modes.length !== 1 || !['compact', 'full'].includes(modes[0])))
  )
    throw new Error('Invalid MCP endpoint')
  // Quote query punctuation and IPv6 brackets rather than exposing shell globs.
  const target =
    url.search || endpoint.includes('[') ? `'${endpoint}'` : endpoint
  return `codex mcp add lmm --url ${target}\n# Complete browser authorization. If needed, run:\ncodex mcp login lmm`
}

export function MarketOAuthConnection({ endpoint }: { endpoint: string }) {
  const { t } = useTranslation()
  const [compact, setCompact] = useState(true)
  const [status, setStatus] = useState<{
    value: string
    failed: boolean
  } | null>(null)
  // The parent supplies a validated, query-free server path.
  const target = compact ? `${endpoint}?mode=compact` : endpoint
  const command = marketOAuthCommand(target)
  const copy = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      setStatus({ value, failed: false })
    } catch {
      setStatus({ value, failed: true })
    }
  }
  const visibleStatus =
    status && [target, command].includes(status.value) ? status : null
  return (
    <div className='min-w-0 space-y-4'>
      <h4 className='font-medium'>{t('Browser login (recommended)')}</h4>
      <p className='text-muted-foreground text-sm leading-6'>
        {t(
          'Add this server URL in an MCP client, choose OAuth, and approve access in your browser. No pasted token is needed.'
        )}
      </p>
      <div
        role='group'
        aria-label={t('MCP tool list')}
        className='flex flex-wrap gap-2'
      >
        <Button
          variant={compact ? 'default' : 'outline'}
          aria-pressed={compact}
          className='min-h-11'
          onClick={() => setCompact(true)}
        >
          {t('Compact: metamcp only')}
        </Button>
        <Button
          variant={!compact ? 'default' : 'outline'}
          aria-pressed={!compact}
          className='min-h-11'
          onClick={() => setCompact(false)}
        >
          {t('Full: individual tools too')}
        </Button>
      </div>
      <div className='flex min-w-0 flex-col items-start gap-3 sm:flex-row sm:items-center'>
        <code className='bg-muted min-w-0 flex-1 rounded-lg p-3 text-sm break-all'>
          {target}
        </code>
        <Button
          variant='outline'
          className='min-h-11 shrink-0'
          onClick={() => void copy(target)}
        >
          {t('Copy server URL')}
        </Button>
      </div>
      <p className='text-muted-foreground text-sm leading-6'>
        {t(
          'Run the command, sign in to LMM, and approve access. Your provider key is not sent to the client.'
        )}
      </p>
      <pre
        className='bg-muted max-w-full overflow-x-auto rounded-lg p-4 text-xs leading-6'
        tabIndex={0}
      >
        {command}
      </pre>
      <Button
        variant='outline'
        className='min-h-11'
        onClick={() => void copy(command)}
      >
        {t('Copy command')}
      </Button>
      {visibleStatus && (
        <p role={visibleStatus.failed ? 'alert' : 'status'}>
          {t(visibleStatus.failed ? 'Copy failed' : 'Copied')}
        </p>
      )}
      <p className='text-muted-foreground text-xs leading-5'>
        {t(
          'After login, select this OAuth client and set tool access and spending limits. Login alone does not authorize spending.'
        )}
      </p>
    </div>
  )
}
