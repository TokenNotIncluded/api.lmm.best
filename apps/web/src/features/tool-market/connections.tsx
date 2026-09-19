/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { useAuthStore } from '@/stores/auth-store'

import {
  marketAPI,
  marketQuota,
  type Budget,
  type Grant,
  type Installation,
  type MarketConfig,
  type MarketToken,
} from './api'
import { creditAmount } from './money'

export function MarketConnections({ config }: { config: MarketConfig }) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const userID = useAuthStore((state) => state.auth.user?.id)
  const tokens = useQuery({
    queryKey: ['tool-market', userID, 'tokens'],
    queryFn: () => marketAPI.mine<MarketToken>('tokens'),
  })
  const grants = useQuery({
    queryKey: ['tool-market', userID, 'grants'],
    queryFn: () => marketAPI.mine<Grant>('grants'),
  })
  const installations = useQuery({
    queryKey: ['tool-market', userID, 'installations'],
    queryFn: () => marketAPI.mine<Installation>('installations'),
  })
  const budgets = useQuery({
    queryKey: ['tool-market', userID, 'budgets'],
    queryFn: () => marketAPI.mine<Budget>('budgets'),
  })
  const [client, setClient] = useState('my-agent')
  const [secret, setSecret] = useState('')
  const [scope, setScope] = useState('account')
  const [scopeID, setScopeID] = useState('')
  const [limit, setLimit] = useState('0')
  const action = useMutation({
    retry: false,
    mutationFn: (operation: () => Promise<unknown>) => operation(),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ['tool-market'] })
    },
  })
  return (
    <div className='grid gap-8 xl:grid-cols-2'>
      {(tokens.isPending ||
        grants.isPending ||
        installations.isPending ||
        budgets.isPending) && (
        <p
          role='status'
          className='text-muted-foreground text-sm xl:col-span-2'
        >
          {t('Loading…')}
        </p>
      )}
      <section className='space-y-5'>
        <h3 className='text-lg font-semibold'>
          {t('Connect your MCP client')}
        </h3>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Create a seven-day connection token. It can discover tools and manage this client’s tool set. Calls still require a separate tool authorization.'
          )}
        </p>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor='mcp-client'>{t('Client ID')}</FieldLabel>
            <Input
              id='mcp-client'
              value={client}
              maxLength={128}
              onChange={(e) => setClient(e.target.value)}
            />
            <FieldDescription>
              {t('Use this same client ID when loading and authorizing tools.')}
            </FieldDescription>
          </Field>
          <Button
            disabled={action.isPending || !client.trim()}
            onClick={() =>
              action.mutate(async () => {
                const data = await marketAPI.token(client.trim())
                setSecret(data.token)
                return data.record
              })
            }
          >
            {t('Create connection token')}
          </Button>
        </FieldGroup>
        <Field>
          <FieldLabel htmlFor='mcp-url'>{t('MCP endpoint')}</FieldLabel>
          <Input
            id='mcp-url'
            readOnly
            value={`${window.location.origin}${config.mcp_path}`}
          />
        </Field>
        {secret && (
          <div className='space-y-3'>
            <Field>
              <FieldLabel htmlFor='mcp-secret'>
                {t('Connection token — shown once')}
              </FieldLabel>
              <Input
                id='mcp-secret'
                type='password'
                autoComplete='off'
                readOnly
                value={secret}
              />
              <FieldDescription>
                {t(
                  'Add this as a Bearer token in your client. Do not paste it into an Agent conversation.'
                )}
              </FieldDescription>
            </Field>
            <div className='flex gap-2'>
              <Button
                variant='outline'
                onClick={() =>
                  action.mutate(() => navigator.clipboard.writeText(secret))
                }
              >
                {t('Copy token')}
              </Button>
              <Button variant='ghost' onClick={() => setSecret('')}>
                {t('Hide token')}
              </Button>
            </div>
          </div>
        )}
        <p className='text-muted-foreground text-sm'>
          {t(
            'OAuth connections use the client ID oauth:lmm-pi or oauth:lmm-dsh and require newly approved market scopes.'
          )}
        </p>
        {(tokens.data ?? []).map((token) => (
          <div
            key={token.id}
            className='flex flex-wrap items-center justify-between gap-2 border-b py-3 text-sm'
          >
            <div className='min-w-0'>
              <p className='font-medium break-all'>{token.client_id}</p>
              <p className='text-muted-foreground'>
                {token.revoked_at
                  ? t('Revoked')
                  : new Date(token.expires_at * 1000).toLocaleString()}
              </p>
            </div>
            <Button
              variant='outline'
              disabled={!!token.revoked_at || action.isPending}
              onClick={() =>
                action.mutate(() => marketAPI.revokeToken(token.id))
              }
            >
              {t('Revoke')}
            </Button>
          </div>
        ))}
        <Separator />
        <h3 className='font-semibold'>{t('Spending budgets')}</h3>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Budgets include reserved and spent credits. These are cumulative limits; changing them does not reset usage.'
          )}
        </p>
        <form
          onSubmit={(event) => {
            event.preventDefault()
            action.mutate(() =>
              marketAPI.budget({
                scope,
                scope_id: scope === 'account' ? '' : scopeID,
                limit_quota: marketQuota(limit, config.quota_per_unit),
              })
            )
          }}
        >
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor='budget-scope'>
                {t('Budget scope')}
              </FieldLabel>
              <select
                id='budget-scope'
                className='border-input bg-background h-9 rounded-md border px-3 text-sm'
                value={scope}
                onChange={(e) => setScope(e.target.value)}
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
                  onChange={(e) => setScopeID(e.target.value)}
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
                onChange={(e) => setLimit(e.target.value)}
              />
            </Field>
            <Button disabled={action.isPending}>{t('Save budget')}</Button>
          </FieldGroup>
        </form>
        {(budgets.data ?? []).map((budget) => (
          <p
            key={`${budget.scope}:${budget.scope_id}`}
            className='text-sm break-all'
          >
            {budget.scope} {budget.scope_id}:{' '}
            {creditAmount(
              budget.spent_quota + budget.reserved_quota,
              config.quota_per_unit
            )}{' '}
            / {creditAmount(budget.limit_quota, config.quota_per_unit)}
          </p>
        ))}
      </section>
      <section className='space-y-5'>
        <h3 className='text-lg font-semibold'>
          {t('Loaded tools and authorizations')}
        </h3>
        {installations.isSuccess &&
          grants.isSuccess &&
          !(installations.data.length || grants.data.length) && (
            <p className='text-muted-foreground text-sm'>
              {t(
                'Load a tool from the market, then authorize its client and spending limits.'
              )}
            </p>
          )}
        {(installations.data ?? []).map((item) => (
          <div
            key={`${item.client_id}:${item.tool_id}`}
            className='space-y-2 border-b pb-4 text-sm'
          >
            <p className='font-medium'>{item.client_id}</p>
            <p className='text-muted-foreground break-all'>{item.tool_id}</p>
            <Button
              variant='outline'
              disabled={action.isPending}
              onClick={() =>
                action.mutate(() => marketAPI.install(item, false))
              }
            >
              {t('Unload')}
            </Button>
          </div>
        ))}
        {(grants.data ?? []).map((grant) => (
          <div key={grant.id} className='space-y-2 border-b pb-4 text-sm'>
            <p className='font-medium'>{grant.client_id}</p>
            <p className='text-muted-foreground break-all'>{grant.tool_id}</p>
            <p>
              {t('{{amount}} credits', {
                amount: creditAmount(
                  grant.spent_quota + grant.reserved_quota,
                  config.quota_per_unit
                ),
              })}{' '}
              / {creditAmount(grant.max_total_quota, config.quota_per_unit)}
            </p>
            <p>
              {grant.revoked_at
                ? t('Revoked')
                : new Date(grant.expires_at * 1000).toLocaleString()}
            </p>
            <Button
              variant='outline'
              disabled={!!grant.revoked_at || action.isPending}
              onClick={() =>
                action.mutate(() => marketAPI.revokeGrant(grant.id))
              }
            >
              {t('Revoke authorization')}
            </Button>
          </div>
        ))}
      </section>
      {(action.isError ||
        tokens.isError ||
        grants.isError ||
        budgets.isError ||
        installations.isError) && (
        <p role='alert' className='text-destructive text-sm xl:col-span-2'>
          {t(
            'The operation failed. Check the limits and connection settings, then retry.'
          )}
        </p>
      )}
      {(tokens.isError ||
        grants.isError ||
        budgets.isError ||
        installations.isError) && (
        <Button
          variant='outline'
          className='w-fit'
          onClick={() =>
            void Promise.allSettled([
              tokens.refetch(),
              grants.refetch(),
              budgets.refetch(),
              installations.refetch(),
            ])
          }
        >
          {t('Retry')}
        </Button>
      )}
    </div>
  )
}
