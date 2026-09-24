/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useAuthStore } from '@/stores/auth-store'

import {
  MarketAPIError,
  marketAPI,
  marketQuota,
  type Budget,
  type Grant,
  type Installation,
  type MarketConfig,
  type MarketToken,
} from './api'
import {
  marketConnectionNamespace,
  registerMarketConnectionTranslations,
  type MarketConnectionCopyKey,
} from './connection-i18n'
import {
  buildMarketClientConfig,
  connectionStatus,
  defaultConnectionPermissions,
  isPersonalMarketClient,
  marketEndpoint,
} from './connection-utils'
import { creditAmount } from './money'

type IssuedToken = { token: string; record: MarketToken }
type ClientAccess = {
  tokens: MarketToken[]
  grants: Grant[]
  installations: Installation[]
}

// Remount all local state on account changes; a one-time secret must never survive
// logout/login or become visible to the next account in the same browser session.
export function MarketConnections({ config }: { config: MarketConfig }) {
  const userID = useAuthStore((state) => state.auth.user?.id)
  return userID ? (
    <ConnectionWorkspace key={userID} userID={userID} config={config} />
  ) : null
}

function ConnectionWorkspace({
  userID,
  config,
}: {
  userID: number
  config: MarketConfig
}) {
  const { t, i18n } = useTranslation()
  registerMarketConnectionTranslations(i18n)
  const m = (
    key: MarketConnectionCopyKey,
    values?: Record<string, string | number>
  ) => String(t(key, { ...values, ns: marketConnectionNamespace }))
  const cache = useQueryClient()
  const tokens = useQuery({
    queryKey: ['tool-market', userID, 'tokens'],
    queryFn: ({ signal }) => marketAPI.mine<MarketToken>('tokens', signal),
  })
  const grants = useQuery({
    queryKey: ['tool-market', userID, 'grants'],
    queryFn: ({ signal }) => marketAPI.mine<Grant>('grants', signal),
  })
  const installations = useQuery({
    queryKey: ['tool-market', userID, 'installations'],
    queryFn: ({ signal }) =>
      marketAPI.mine<Installation>('installations', signal),
  })
  const budgets = useQuery({
    queryKey: ['tool-market', userID, 'budgets'],
    queryFn: ({ signal }) => marketAPI.mine<Budget>('budgets', signal),
  })
  const [client, setClient] = useState('my-agent')
  const [permissions, setPermissions] = useState(defaultConnectionPermissions)
  const [issued, setIssued] = useState<IssuedToken | null>(null)
  const [copyStatus, setCopyStatus] = useState<'copied' | 'copyFailed' | null>(
    null
  )
  const [disconnect, setDisconnect] = useState<string | null>(null)
  const [scope, setScope] = useState('account')
  const [scopeID, setScopeID] = useState('')
  const [limit, setLimit] = useState('0')
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30000)
    return () => window.clearInterval(timer)
  }, [])
  useEffect(() => {
    if (!issued) return
    const timer = window.setTimeout(() => setIssued(null), 5 * 60 * 1000)
    return () => window.clearTimeout(timer)
  }, [issued])
  useEffect(() => {
    if (!issued) return
    const latest = tokens.data?.find((token) => token.id === issued.record.id)
    if (connectionStatus(latest ?? issued.record, now) !== 'active')
      setIssued(null)
  }, [issued, tokens.data, now])

  const action = useMutation({
    retry: false,
    mutationFn: (operation: () => Promise<void>) => operation(),
    onSuccess: async () => {
      await cache.invalidateQueries({ queryKey: ['tool-market', userID] })
    },
  })
  const copy = async (text: string) => {
    setCopyStatus(null)
    try {
      await navigator.clipboard.writeText(text)
      setCopyStatus('copied')
    } catch {
      setCopyStatus('copyFailed')
    }
  }
  let endpoint = ''
  try {
    endpoint = marketEndpoint(window.location.origin, config.mcp_path)
  } catch {
    // No token may be copied into an invalid or cross-origin configuration.
  }
  const validClient = isPersonalMarketClient(client.trim())
  const previewClient = issued?.record.client_id ?? client.trim()
  const preview =
    endpoint && isPersonalMarketClient(previewClient)
      ? buildMarketClientConfig(endpoint, previewClient)
      : ''
  let budgetQuota: number | undefined
  try {
    budgetQuota = marketQuota(limit, config.quota_per_unit)
  } catch {
    // Keep invalid draft input local; no budget mutation is sent.
  }
  const readError =
    tokens.isError || grants.isError || installations.isError || budgets.isError
  const accessReady =
    tokens.isSuccess && grants.isSuccess && installations.isSuccess
  const groups = useMemo(() => {
    const rows = new Map<string, ClientAccess>()
    const ensure = (id: string) => {
      let row = rows.get(id)
      if (!row) {
        row = { tokens: [], grants: [], installations: [] }
        rows.set(id, row)
      }
      return row
    }
    for (const row of tokens.data ?? []) ensure(row.client_id).tokens.push(row)
    for (const row of grants.data ?? []) ensure(row.client_id).grants.push(row)
    for (const row of installations.data ?? [])
      ensure(row.client_id).installations.push(row)
    return [...rows].sort(([a], [b]) => a.localeCompare(b))
  }, [tokens.data, grants.data, installations.data])
  const errorKeys: Record<string, MarketConnectionCopyKey> = {
    TOOL_MARKET_BUDGET: 'budgetError',
    TOOL_MARKET_BALANCE: 'balanceError',
    TOOL_MARKET_DENIED: 'deniedError',
    TOOL_MARKET_NOT_FOUND: 'deniedError',
    TOOL_MARKET_BUSY: 'busyError',
    TOOL_MARKET_CONFLICT: 'conflictError',
    TOOL_MARKET_REMOTE_CHANGED: 'conflictError',
  }
  const actionError =
    action.error instanceof MarketAPIError
      ? (errorKeys[action.error.code] ?? 'operationFailed')
      : 'operationFailed'
  const editBudget = (budget: Budget) => {
    setScope(budget.scope)
    setScopeID(budget.scope_id)
    setLimit(String(budget.limit_quota / config.quota_per_unit))
    action.reset()
    document.getElementById('budget-limit')?.focus()
  }

  return (
    <div className='space-y-6'>
      {(action.isError || readError) && (
        <div
          role='alert'
          className='border-destructive/30 bg-destructive/5 flex flex-wrap items-center justify-between gap-3 rounded-xl border p-4 text-sm'
        >
          <p>{m(action.isError ? actionError : 'operationFailed')}</p>
          {readError && (
            <Button
              variant='outline'
              onClick={() =>
                void Promise.allSettled([
                  tokens.refetch(),
                  grants.refetch(),
                  installations.refetch(),
                  budgets.refetch(),
                ])
              }
            >
              {t('Retry')}
            </Button>
          )}
        </div>
      )}
      {action.isSuccess && (
        <p role='status' className='text-muted-foreground text-sm'>
          {m('saved')}
        </p>
      )}
      <div className='grid items-start gap-6 xl:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]'>
        <section className='bg-card min-w-0 space-y-5 rounded-xl border p-5 sm:p-6'>
          <div className='space-y-2'>
            <h3 className='text-lg font-semibold'>
              {t('Connect your MCP client')}
            </h3>
            <p className='text-muted-foreground text-sm'>{m('summary')}</p>
          </div>
          <form
            onSubmit={(event) => {
              event.preventDefault()
              if (!validClient || !endpoint || action.isPending) return
              setIssued(null)
              setCopyStatus(null)
              action.mutate(async () => {
                const data = await marketAPI.token(client.trim(), permissions)
                setIssued(data)
                // Do not return data: React Query's mutation cache must not retain the secret.
              })
            }}
          >
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor='mcp-client'>{t('Client ID')}</FieldLabel>
                <Input
                  id='mcp-client'
                  value={client}
                  maxLength={128}
                  disabled={action.isPending}
                  aria-invalid={!validClient}
                  aria-describedby='mcp-client-help'
                  onChange={(event) => setClient(event.target.value)}
                />
                <FieldDescription id='mcp-client-help'>
                  {validClient
                    ? t(
                        'Use this same client ID when loading and authorizing tools.'
                      )
                    : m('invalidClient')}
                </FieldDescription>
              </Field>
              <fieldset className='space-y-3' disabled={action.isPending}>
                <legend className='mb-3 text-sm font-medium'>
                  {m('permissions')}
                </legend>
                <label className='flex cursor-pointer items-center gap-3 text-sm'>
                  <input
                    type='checkbox'
                    className='accent-foreground size-4'
                    checked={permissions.can_invoke}
                    onChange={(event) =>
                      setPermissions({
                        ...permissions,
                        can_invoke: event.target.checked,
                      })
                    }
                  />
                  {m('invoke')}
                </label>
                <label className='flex cursor-pointer items-center gap-3 text-sm'>
                  <input
                    type='checkbox'
                    className='accent-foreground size-4'
                    checked={permissions.can_manage}
                    onChange={(event) =>
                      setPermissions({
                        ...permissions,
                        can_manage: event.target.checked,
                      })
                    }
                  />
                  {m('manage')}
                </label>
                {!permissions.can_invoke && !permissions.can_manage && (
                  <Badge variant='outline'>{m('readOnly')}</Badge>
                )}
              </fieldset>
              <Field>
                <FieldLabel htmlFor='mcp-expiry'>{m('expiry')}</FieldLabel>
                <select
                  id='mcp-expiry'
                  className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
                  value={permissions.expires_in_days}
                  disabled={action.isPending}
                  onChange={(event) =>
                    setPermissions({
                      ...permissions,
                      expires_in_days: Number(event.target.value),
                    })
                  }
                >
                  {[1, 7, 30, 90].map((days) => (
                    <option key={days} value={days}>
                      {m('days', { count: days })}
                    </option>
                  ))}
                </select>
              </Field>
              {!endpoint && (
                <p role='alert' className='text-destructive text-sm'>
                  {m('invalidEndpoint')}
                </p>
              )}
              <Button
                type='submit'
                disabled={action.isPending || !validClient || !endpoint}
              >
                {action.isPending
                  ? t('Loading…')
                  : t('Create connection token')}
              </Button>
            </FieldGroup>
          </form>
          <Field>
            <FieldLabel htmlFor='mcp-url'>{t('MCP endpoint')}</FieldLabel>
            <Input
              id='mcp-url'
              readOnly
              value={endpoint}
              className='font-mono text-xs'
            />
          </Field>
          {issued && (
            <div className='bg-muted/40 space-y-3 rounded-lg border p-4'>
              <Field>
                <FieldLabel htmlFor='mcp-secret'>
                  {t('Connection token — shown once')}
                </FieldLabel>
                <Input
                  id='mcp-secret'
                  type='password'
                  autoComplete='off'
                  readOnly
                  value={issued.token}
                />
                <FieldDescription>
                  {m('tokenClient', { client: issued.record.client_id })}
                </FieldDescription>
                <FieldDescription>
                  {t(
                    'Add this as a Bearer token in your client. Do not paste it into an Agent conversation.'
                  )}
                </FieldDescription>
              </Field>
              <div className='flex flex-wrap gap-2'>
                <Button
                  variant='outline'
                  onClick={() => void copy(issued.token)}
                >
                  {t('Copy token')}
                </Button>
                <Button
                  variant='outline'
                  disabled={!endpoint}
                  onClick={() =>
                    void copy(
                      buildMarketClientConfig(
                        endpoint,
                        issued.record.client_id,
                        issued.token
                      )
                    )
                  }
                >
                  {m('copyConfig')}
                </Button>
                <Button
                  variant='ghost'
                  onClick={() => {
                    setIssued(null)
                    setCopyStatus(null)
                  }}
                >
                  {t('Hide token')}
                </Button>
              </div>
            </div>
          )}
          {copyStatus && (
            <p
              role={copyStatus === 'copyFailed' ? 'alert' : 'status'}
              className='text-muted-foreground text-sm'
            >
              {m(copyStatus)}
            </p>
          )}
          {preview && (
            <details className='text-sm'>
              <summary className='cursor-pointer font-medium'>
                {m('preview')}
              </summary>
              <pre className='bg-muted mt-3 overflow-x-auto rounded-lg p-4 text-xs'>
                {preview}
              </pre>
            </details>
          )}
          <p className='text-muted-foreground text-xs'>
            {t(
              'OAuth connections use the client ID oauth:lmm-pi or oauth:lmm-dsh and require newly approved market scopes.'
            )}
          </p>
        </section>

        <section className='min-w-0 space-y-4'>
          <div className='flex items-center justify-between gap-3'>
            <h3 className='text-lg font-semibold'>{m('clients')}</h3>
            {accessReady && <Badge variant='outline'>{groups.length}</Badge>}
          </div>
          {(tokens.isPending ||
            grants.isPending ||
            installations.isPending) && (
            <p role='status' className='text-muted-foreground py-6 text-sm'>
              {t('Loading…')}
            </p>
          )}
          {accessReady && !groups.length && (
            <div className='rounded-xl border border-dashed px-6 py-12 text-center'>
              <h4 className='font-medium'>{m('empty')}</h4>
              <p className='text-muted-foreground mx-auto mt-2 max-w-sm text-sm'>
                {t(
                  'Load a tool from the market, then authorize its client and spending limits.'
                )}
              </p>
            </div>
          )}
          {groups.map(([id, group]) => {
            const hasAccess =
              group.tokens.some((row) => !row.revoked_at) ||
              group.grants.some((row) => !row.revoked_at) ||
              group.installations.length > 0
            return (
              <article
                key={id}
                className='bg-card space-y-4 rounded-xl border p-5'
              >
                <div className='flex flex-wrap items-start justify-between gap-3'>
                  <h4 className='min-w-0 font-semibold break-all'>{id}</h4>
                  {isPersonalMarketClient(id) && hasAccess && (
                    <Button
                      variant='outline'
                      disabled={action.isPending || !accessReady}
                      onClick={() => {
                        action.reset()
                        setDisconnect(id)
                      }}
                    >
                      {m('disconnect')}
                    </Button>
                  )}
                </div>
                {group.tokens.map((token) => (
                  <div
                    key={token.id}
                    className='flex flex-wrap items-center justify-between gap-3 border-t pt-3 text-sm'
                  >
                    <div className='min-w-0 space-y-2'>
                      <div className='flex flex-wrap gap-2'>
                        <Badge
                          variant={
                            connectionStatus(token, now) === 'active'
                              ? 'secondary'
                              : 'outline'
                          }
                        >
                          {m(connectionStatus(token, now))}
                        </Badge>
                        {token.can_invoke && (
                          <Badge variant='outline'>{m('invoke')}</Badge>
                        )}
                        {token.can_manage && (
                          <Badge variant='outline'>{m('manage')}</Badge>
                        )}
                        {!token.can_invoke && !token.can_manage && (
                          <Badge variant='outline'>{m('readOnly')}</Badge>
                        )}
                      </div>
                      <time
                        className='text-muted-foreground text-xs'
                        dateTime={new Date(
                          token.expires_at * 1000
                        ).toISOString()}
                      >
                        {new Date(token.expires_at * 1000).toLocaleString()}
                      </time>
                    </div>
                    <Button
                      variant='ghost'
                      disabled={
                        !!token.revoked_at || action.isPending || !accessReady
                      }
                      onClick={() =>
                        action.mutate(async () => {
                          await marketAPI.revokeToken(token.id)
                          if (issued?.record.id === token.id) setIssued(null)
                        })
                      }
                    >
                      {t('Revoke')}
                    </Button>
                  </div>
                ))}
                {(group.installations.length > 0 ||
                  group.grants.length > 0) && (
                  <details className='border-t pt-3 text-sm'>
                    <summary className='cursor-pointer font-medium'>
                      {t('Loaded tools and authorizations')}
                    </summary>
                    <div className='mt-3 space-y-4'>
                      {group.installations.map((item) => (
                        <div
                          key={`${item.tool_id}:${item.version_id}`}
                          className='flex flex-wrap items-center justify-between gap-3'
                        >
                          <code className='text-muted-foreground min-w-0 text-xs break-all'>
                            {item.tool_id}
                          </code>
                          <Button
                            variant='outline'
                            disabled={action.isPending || !accessReady}
                            onClick={() =>
                              action.mutate(async () => {
                                await marketAPI.install(item, false)
                              })
                            }
                          >
                            {t('Unload')}
                          </Button>
                        </div>
                      ))}
                      {group.grants.map((grant) => (
                        <div key={grant.id} className='space-y-2 border-t pt-3'>
                          <code className='text-muted-foreground block text-xs break-all'>
                            {grant.tool_id}
                          </code>
                          <div className='flex flex-wrap items-center justify-between gap-3'>
                            <div className='space-y-1'>
                              <Badge variant='outline'>
                                {m(connectionStatus(grant, now))}
                              </Badge>
                              <p className='tabular-nums'>
                                {t('{{amount}} credits', {
                                  amount: creditAmount(
                                    grant.spent_quota + grant.reserved_quota,
                                    config.quota_per_unit
                                  ),
                                })}{' '}
                                /{' '}
                                {creditAmount(
                                  grant.max_total_quota,
                                  config.quota_per_unit
                                )}
                              </p>
                              <p className='text-muted-foreground text-xs'>
                                {t('Remaining successful calls')}:{' '}
                                {Math.max(
                                  0,
                                  grant.max_calls -
                                    grant.successful_calls -
                                    grant.reserved_calls
                                )}
                              </p>
                            </div>
                            <Button
                              variant='ghost'
                              disabled={
                                !!grant.revoked_at ||
                                action.isPending ||
                                !accessReady
                              }
                              onClick={() =>
                                action.mutate(async () => {
                                  await marketAPI.revokeGrant(grant.id)
                                })
                              }
                            >
                              {t('Revoke authorization')}
                            </Button>
                          </div>
                        </div>
                      ))}
                    </div>
                  </details>
                )}
              </article>
            )
          })}
        </section>
      </div>

      <section className='bg-card space-y-5 rounded-xl border p-5 sm:p-6'>
        <div className='space-y-2'>
          <h3 className='text-lg font-semibold'>{t('Spending budgets')}</h3>
          <p className='text-muted-foreground max-w-3xl text-sm'>
            {t(
              'Budgets include reserved and spent credits. These are cumulative limits; changing them does not reset usage.'
            )}
          </p>
        </div>
        <form
          className='grid items-end gap-4 sm:grid-cols-2 xl:grid-cols-4'
          onSubmit={(event) => {
            event.preventDefault()
            if (
              budgetQuota === undefined ||
              !budgets.isSuccess ||
              action.isPending
            )
              return
            const amount = budgetQuota
            action.mutate(async () => {
              await marketAPI.budget({
                scope,
                scope_id: scope === 'account' ? '' : scopeID.trim(),
                limit_quota: amount,
              })
            })
          }}
        >
          <Field>
            <FieldLabel htmlFor='budget-scope'>{t('Budget scope')}</FieldLabel>
            <select
              id='budget-scope'
              className='border-input bg-background h-9 rounded-md border px-3 text-sm'
              value={scope}
              onChange={(event) => {
                setScope(event.target.value)
                setScopeID('')
              }}
            >
              <option value='account'>{t('Account')}</option>
              <option value='client'>{t('Client')}</option>
              <option value='tool'>{t('Tool')}</option>
            </select>
          </Field>
          {scope !== 'account' && (
            <Field>
              <FieldLabel htmlFor='budget-id'>
                {scope === 'client' ? t('Client ID') : t('Tool ID')}
              </FieldLabel>
              <Input
                id='budget-id'
                required
                value={scopeID}
                maxLength={128}
                onChange={(event) => setScopeID(event.target.value)}
              />
            </Field>
          )}
          <Field>
            <FieldLabel htmlFor='budget-limit'>
              {t('Total spending limit')}
            </FieldLabel>
            <Input
              id='budget-limit'
              inputMode='decimal'
              required
              value={limit}
              aria-invalid={budgetQuota === undefined}
              onChange={(event) => setLimit(event.target.value)}
            />
          </Field>
          <Button
            type='submit'
            disabled={
              action.isPending ||
              !budgets.isSuccess ||
              budgetQuota === undefined ||
              (scope !== 'account' && !scopeID.trim())
            }
          >
            {t('Save budget')}
          </Button>
        </form>
        <p className='text-muted-foreground text-xs'>
          {budgetQuota === undefined ? m('invalidBudget') : m('zeroBudget')}
        </p>
        {budgets.isPending && (
          <p role='status' className='text-muted-foreground text-sm'>
            {t('Loading…')}
          </p>
        )}
        <div className='grid gap-4 md:grid-cols-2 xl:grid-cols-3'>
          {(budgets.data ?? []).map((budget) => {
            const used = budget.spent_quota + budget.reserved_quota
            return (
              <div
                key={`${budget.scope}:${budget.scope_id}`}
                className='space-y-3 rounded-lg border p-4 text-sm'
              >
                <div className='flex items-start justify-between gap-3'>
                  <p className='min-w-0 font-medium break-all'>
                    {budget.scope === 'account'
                      ? t('Account')
                      : budget.scope === 'client'
                        ? t('Client')
                        : t('Tool')}
                    {budget.scope_id && ` · ${budget.scope_id}`}
                  </p>
                  <Button
                    variant='ghost'
                    disabled={action.isPending || !budgets.isSuccess}
                    onClick={() => editBudget(budget)}
                  >
                    {m('editBudget')}
                  </Button>
                </div>
                <p className='tabular-nums'>
                  {creditAmount(used, config.quota_per_unit)} /{' '}
                  {creditAmount(budget.limit_quota, config.quota_per_unit)}
                </p>
                <progress
                  aria-label={t('Total spending limit')}
                  className='accent-foreground h-1.5 w-full'
                  value={Math.min(used, Math.max(1, budget.limit_quota))}
                  max={Math.max(1, budget.limit_quota)}
                />
              </div>
            )
          })}
        </div>
      </section>

      <Dialog
        open={disconnect !== null}
        onOpenChange={(open) => {
          if (!open && !action.isPending) setDisconnect(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {m('disconnectTitle', { client: disconnect ?? '' })}
            </DialogTitle>
            <DialogDescription>{m('disconnectWarning')}</DialogDescription>
          </DialogHeader>
          {action.isError && (
            <p role='alert' className='text-destructive text-sm'>
              {m(actionError)}
            </p>
          )}
          <div className='flex justify-end gap-2'>
            <Button
              variant='outline'
              disabled={action.isPending}
              onClick={() => setDisconnect(null)}
            >
              {t('Cancel')}
            </Button>
            <Button
              variant='destructive'
              disabled={action.isPending || !disconnect || !accessReady}
              onClick={() => {
                if (!disconnect) return
                const clientID = disconnect
                action.mutate(async () => {
                  await marketAPI.disconnectClient(clientID)
                  if (issued?.record.client_id === clientID) setIssued(null)
                  setDisconnect(null)
                })
              }}
            >
              {m('disconnect')}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
