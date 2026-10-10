/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { Button } from '@/components/ui/button'

import { marketSupports, type MarketConfig } from './api'
import { useMarketTranslation as useTranslation } from './provider-i18n'

export function MarketMetaToolCard({
  config,
  onConnect,
}: {
  config?: MarketConfig
  onConnect: () => void
}) {
  const { t } = useTranslation()
  const tool = config?.meta_tool
  if (!marketSupports(config, 'metamcp') || tool?.name !== 'metamcp') {
    return null
  }
  return (
    <section
      aria-labelledby='market-meta-title'
      className='bg-primary/5 min-w-0 space-y-5 rounded-2xl p-5 sm:p-6'
    >
      <div className='flex flex-col items-start justify-between gap-4 sm:flex-row'>
        <div className='min-w-0 space-y-2'>
          <p className='text-muted-foreground text-xs'>
            {t('Built-in gateway tool')}
          </p>
          <h3
            id='market-meta-title'
            className='text-xl font-semibold tracking-tight'
          >
            metamcp
          </h3>
          <p className='text-muted-foreground max-w-2xl text-sm leading-6'>
            {t(
              'Available after connecting. Discover tools, inspect their details, then load and invoke them through one entry.'
            )}
          </p>
        </div>
        <Button className='min-h-11 shrink-0' onClick={onConnect}>
          {t('Connect MCP client')}
        </Button>
      </div>
      <p className='text-sm leading-7'>
        {t('Discover → Inspect → Load → Authorize → Invoke')}
      </p>
      <p className='text-muted-foreground text-xs leading-6'>
        {t(
          'Management is free. Invocation uses the selected tool price. Loading does not grant payment permission.'
        )}
      </p>
      <details className='min-w-0 text-sm'>
        <summary className='cursor-pointer py-2'>
          {t('View actions and parameters')}
        </summary>
        <pre
          className='bg-background/70 mt-3 max-h-80 max-w-full overflow-auto rounded-xl p-4 text-xs leading-6'
          tabIndex={0}
        >
          {JSON.stringify(tool.inputSchema, null, 2)}
        </pre>
      </details>
    </section>
  )
}
