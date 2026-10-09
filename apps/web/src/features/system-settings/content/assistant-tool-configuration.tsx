/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { Check, PlugZap, Search, Unplug } from 'lucide-react'
import { useEffect, useId, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { marketAPI } from '@/features/tool-market/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  assistantToolRule,
  defaultAssistantToolRule,
  updateAssistantToolRule,
  weeklyDiscountLimit,
  type AssistantCatalogTool,
  type AssistantToolPolicy,
  type AssistantToolRule,
} from './assistant-tool-policy'

export function AssistantToolConfiguration({
  tool,
  policy,
  onChange,
  onClose,
  disabled,
  children,
  rulesSupported = true,
  groupEnabled = true,
}: {
  tool: AssistantCatalogTool
  policy: AssistantToolPolicy
  onChange: (value: string) => void
  onClose: () => void
  disabled?: boolean
  children?: ReactNode
  rulesSupported?: boolean
  groupEnabled?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const rule = assistantToolRule(policy, tool)
  const defaults = defaultAssistantToolRule(tool)
  const change = (values: Partial<AssistantToolRule>) => {
    if (rulesSupported && !disabled) { onChange(updateAssistantToolRule(policy, tool.name, { ...rule, ...values })) }
  }
  const levelName = (level: number) =>
    level === 6
      ? t('L6 (Super administrator)')
      : level === 5
        ? t('L5 (Administrator)')
        : `L${level}`
  const levels = Array.from(
    { length: defaults.max_level - defaults.min_level + 1 },
    (_, offset) => defaults.min_level + offset
  )
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent
        className='max-h-[90dvh] overflow-y-auto sm:max-w-2xl'
        data-testid='assistant-tool-configuration'
      >
        <DialogHeader>
          <p className='text-muted-foreground font-mono text-xs break-all'>
            {tool.name}
          </p>
          <DialogTitle>
            {t('Configure {{tool}}', { tool: t(tool.label) })}
          </DialogTitle>
          <DialogDescription>
            {t(
              'Changes stay in this draft. Use Save assistant settings to apply them.'
            )}
          </DialogDescription>
        </DialogHeader>
        <div className='space-y-6'>
          <div className='flex items-center justify-between gap-4'>
            <label htmlFor={`${id}-enabled`}>{t('Enable this tool')}</label>
            <Switch id={`${id}-enabled`} checked={policy.tools[tool.name] !== false} disabled={disabled}
              onCheckedChange={(enabled) => onChange(JSON.stringify({...policy, tools:{...policy.tools,[tool.name]:enabled}}))} />
          </div>
          {!groupEnabled && <p role='status' className='text-muted-foreground text-sm'>{t('This tool group is disabled. Enable its group to use the saved tool settings.')}</p>}
          {!rulesSupported && <p role='status' className='text-muted-foreground text-sm'>{t('This server does not support tool-level rules yet. Tool switches and provider settings remain available. Update the server, then reload this page to edit level rules.')}</p>}
          <fieldset disabled={disabled || !rulesSupported} className='space-y-6 disabled:opacity-60'>
          <section className='space-y-3' aria-labelledby={`${id}-access`}>
            <h4 id={`${id}-access`} className='font-medium'>
              {t('Allowed account levels')}
            </h4>
            <div className='grid gap-4 sm:grid-cols-2'>
              {(['min_level', 'max_level'] as const).map((key) => (
                <div key={key} className='space-y-2'>
                  <label
                    htmlFor={`${id}-${key}`}
                    className='text-muted-foreground text-sm'
                  >
                    {t(key === 'min_level' ? 'Minimum level' : 'Maximum level')}
                  </label>
                  <Select
                    value={String(rule[key])}
                    onValueChange={(value) => {
                      if (typeof value !== 'string') return
                      const n = Number(value)
                      if (!levels.includes(n)) return
                      change(
                        key === 'min_level'
                          ? {
                              min_level: n,
                              max_level: Math.max(n, rule.max_level),
                            }
                          : {
                              max_level: n,
                              min_level: Math.min(n, rule.min_level),
                            }
                      )
                    }}
                  >
                    <SelectTrigger
                      id={`${id}-${key}`}
                      className='w-full'
                      disabled={disabled || !rulesSupported}
                    >
                      <SelectValue>{levelName(rule[key])}</SelectValue>
                    </SelectTrigger>
                    <SelectContent alignItemWithTrigger={false}>
                      {levels.map((level) => (
                        <SelectItem key={level} value={String(level)}>
                          {levelName(level)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              ))}
            </div>
            <p className='text-muted-foreground text-xs leading-relaxed'>
              {t(
                'Both limits are inclusive. L5 is an administrator; L6 is a super administrator. Level rules cannot bypass ownership, confirmation or a tool’s required role.'
              )}
            </p>
          </section>
          {tool.name === 'prepare_weekly_discount' && (
            <section className='space-y-3' aria-labelledby={`${id}-discount`}>
              <h4 id={`${id}-discount`} className='font-medium'>
                {t('Maximum discount by level')}
              </h4>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'A value of 10 means 10% off, not a 10% payment. Zero disables offers for that level. Existing claimed codes are unchanged.'
                )}
              </p>
              <div className='grid grid-cols-2 gap-3 sm:grid-cols-3'>
                {Array.from({ length: 7 }, (_, level) => (
                  <label key={level} className='space-y-2 text-sm'>
                    <span className='block'>{levelName(level)}</span>
                    <div className='relative'>
                      <Input
                        type='number'
                        min={0}
                        max={99}
                        step={1}
                        className='pe-8'
                        value={weeklyDiscountLimit(rule, level)}
                        disabled={disabled || level >= 5}
                        onChange={(event) => {
                          const value = Number(event.target.value)
                          if (
                            Number.isInteger(value) &&
                            value >= 0 &&
                            value <= 99
                          ) {
                            change({
                              discount_percent_by_level: {
                                ...rule.discount_percent_by_level,
                                [String(level)]: value,
                              },
                            })
                          }
                        }}
                      />
                      <span className='text-muted-foreground pointer-events-none absolute end-3 top-2.5'>
                        %
                      </span>
                    </div>
                  </label>
                ))}
              </div>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Administrators do not receive conversation rewards. Unconfigured member levels retain the existing 10% limit.'
                )}
              </p>
            </section>
          )}
          {tool.name === 'create_site_issue' && (
            <section className='space-y-3'>
              <label htmlFor={`${id}-visibility`} className='font-medium'>
                {t('Default issue visibility')}
              </label>
              <Select
                value={rule.default_visibility ?? 'user'}
                onValueChange={(value) => {
                  if (value === 'user' || value === 'admin') {
                    change({ default_visibility: value })
                  }
                }}
              >
                <SelectTrigger
                  id={`${id}-visibility`}
                  className='w-full'
                  disabled={disabled || !rulesSupported}
                >
                  <SelectValue>
                    {t(
                      rule.default_visibility === 'admin'
                        ? 'Administrators only'
                        : 'Reporter and administrators'
                    )}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectItem value='user'>
                    {t('Reporter and administrators')}
                  </SelectItem>
                  <SelectItem value='admin'>
                    {t('Administrators only')}
                  </SelectItem>
                </SelectContent>
              </Select>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'User-visible reports are private to their reporter. Security reports and internal notes stay administrator-only.'
                )}
              </p>
            </section>
          )}
          {tool.name === 'call_market_tool' && (
            <AssistantMarketConnections
              ids={rule.market_service_ids ?? []}
              disabled={disabled || !rulesSupported}
              onChange={(ids) => change({ market_service_ids: ids })}
            />
          )}
          </fieldset>
          {children}
        </div>
        <footer className='flex flex-wrap items-center justify-between gap-3 border-t pt-4'>
          <Button
            type='button'
            variant='ghost'
            disabled={disabled || !rulesSupported}
            onClick={() => onChange(updateAssistantToolRule(policy, tool.name))}
          >
            {t('Reset tool rules')}
          </Button>
          <Button type='button' onClick={onClose}>
            {t('Done')}
          </Button>
        </footer>
      </DialogContent>
    </Dialog>
  )
}

function AssistantMarketConnections({
  ids,
  disabled,
  onChange,
}: {
  ids: string[]
  disabled?: boolean
  onChange: (ids: string[]) => void
}) {
  const { t } = useTranslation()
  const userID = useAuthStore((state) => state.auth.user?.id)
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [offset, setOffset] = useState(0)
  useEffect(() => {
    const timer = setTimeout(() => {
      setQuery(search.trim())
      setOffset(0)
    }, 250)
    return () => clearTimeout(timer)
  }, [search])
  const catalog = useQuery({
    queryKey: ['assistant-market-connectable', userID, query, offset],
    queryFn: async () => {
      const rows = await marketAPI.list(query, offset, 'remote')
      if (
        !Array.isArray(rows) ||
        rows.some(
          (row) =>
            !row ||
            typeof row.id !== 'string' ||
            typeof row.name !== 'string' ||
            row.execution_type !== 'remote'
        )
      ) {
        throw new Error('Invalid market catalogue')
      }
      return rows
    },
    retry: false,
    staleTime: 30_000,
  })
  const toggle = (id: string) =>
    onChange(
      ids.includes(id)
        ? ids.filter((current) => current !== id)
        : [...ids, id].sort()
    )
  return (
    <section className='space-y-4' aria-label={t('Tool market connections')}>
      <div>
        <h4 className='font-medium'>{t('Connect tools from the market')}</h4>
        <p className='text-muted-foreground mt-2 text-sm'>
          {t(
            'Connect a service once to make its tools discoverable. Users must still authorize each tool, version and spending limit. No service is connected by default.'
          )}
        </p>
      </div>
      <div className='relative'>
        <Search
          className='text-muted-foreground absolute start-3 top-3 size-4'
          aria-hidden='true'
        />
        <Input
          value={search}
          disabled={disabled}
          className='ps-9'
          aria-label={t('Search market services')}
          placeholder={t('Search market services')}
          onChange={(event) => setSearch(event.target.value)}
        />
      </div>
      <p className='text-muted-foreground text-xs'>
        {t('Connected {{count}} of 64 services', { count: ids.length })}
      </p>
      {ids.length > 0 && (
        <div className='flex flex-wrap gap-2'>
          {ids.map((id) => (
            <Button
              type='button'
              key={id}
              variant='secondary'
              size='sm'
              disabled={disabled}
              className='h-auto max-w-full gap-2 whitespace-normal'
              aria-label={t('Disconnect {{service}}', { service: id })}
              onClick={() => toggle(id)}
            >
              <span className='break-all'>
                {catalog.data?.find((row) => row.id === id)?.name ?? id}
              </span>
              <Unplug className='size-3 shrink-0' />
            </Button>
          ))}
        </div>
      )}
      {catalog.isPending ? (
        <p role='status'>{t('Loading...')}</p>
      ) : catalog.isError ? (
        <div role='alert'>
          <p>{t('Unable to load market services.')}</p>
          <Button
            type='button'
            variant='link'
            onClick={() => void catalog.refetch()}
          >
            {t('Retry')}
          </Button>
        </div>
      ) : (
        <div className='max-h-80 space-y-1 overflow-y-auto'>
          {catalog.data.length === 0 && (
            <p className='text-muted-foreground py-6 text-center'>
              {t('No matching remote services.')}
            </p>
          )}
          {catalog.data.map((row) => (
            <div
              key={row.id}
              className='bg-muted/30 flex items-start justify-between gap-4 rounded-xl p-3'
            >
              <div className='min-w-0'>
                <p className='font-medium break-words'>{row.name}</p>
                <p className='text-muted-foreground mt-1 line-clamp-2 text-xs break-words'>
                  {row.description}
                </p>
              </div>
              <Button
                type='button'
                size='sm'
                variant={ids.includes(row.id) ? 'secondary' : 'outline'}
                disabled={
                  disabled || (!ids.includes(row.id) && ids.length >= 64)
                }
                aria-pressed={ids.includes(row.id)}
                onClick={() => toggle(row.id)}
              >
                {ids.includes(row.id) ? (
                  <Check className='size-3.5' />
                ) : (
                  <PlugZap className='size-3.5' />
                )}
                {t(ids.includes(row.id) ? 'Connected' : 'Connect')}
              </Button>
            </div>
          ))}
        </div>
      )}
      <div className='flex justify-between gap-3'>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          disabled={offset === 0 || catalog.isFetching}
          onClick={() => setOffset(Math.max(0, offset - 30))}
        >
          {t('Previous')}
        </Button>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          disabled={
            catalog.isFetching || !catalog.data || catalog.data.length < 30
          }
          onClick={() => setOffset(offset + 30)}
        >
          {t('Next')}
        </Button>
      </div>
    </section>
  )
}
