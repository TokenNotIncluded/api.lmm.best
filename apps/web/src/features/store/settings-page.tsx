/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { useAuthStore } from '@/stores/auth-store'

import { storeApi } from './api'
import { StoreMerchantTermsEditor } from './merchant-terms'
import { StorePaymentCategoriesForm } from './payment-categories'
import { STORE_PAYMENT_CATEGORY_COPY as copy } from './payment-category-copy'
import {
  CopyStoreValue,
  StoreAmount,
  StoreAuthGate,
  StoreError,
  StoreLoading,
} from './shared'
import type {
  StoreGateway,
  StoreGatewayInput,
  StorePaymentMethod,
} from './types'
import { paymentLabel, EXTERNAL_MINIMUM_QUOTA } from './utils'

const PROVIDERS: StorePaymentMethod[] = [
  'platform:waffo_pancake',
  'platform:linuxdo',
  'balance',
  'external:epay',
  'external:waffo_pancake',
]
export function StoreSettingsPage() {
  return (
    <StoreAuthGate>
      <StorePaymentSettings />
    </StoreAuthGate>
  )
}
function StorePaymentSettings() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)!
  const client = useQueryClient()
  const query = useQuery({
    queryKey: ['store', 'payments', user.id],
    queryFn: storeApi.paymentSettings,
    retry: false,
  })
  const catalog = useQuery({
    queryKey: ['store', 'config'],
    queryFn: storeApi.config,
    retry: false,
  })
  return (
    <div className='space-y-7'>
      <div>
        <h1 className='console-page-title text-xl font-bold'>
          {t('Seller payment settings')}
        </h1>
        <p className='text-muted-foreground mt-1 max-w-prose text-sm'>
          {t(
            'Choose how customers pay. All methods are disabled until you enable them.'
          )}
        </p>
      </div>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      <StoreError error={catalog.error} retry={() => void catalog.refetch()} />
      {query.isPending ? (
        <StoreLoading />
      ) : (
        query.data && (
          <>
            <div className='bg-muted space-y-2 rounded-md p-4 text-sm'>
              <p>
                {t('Seller balance')}:{' '}
                <StoreAmount quota={query.data.balance_quota} />
              </p>
              <p>
                {t('Seller fee')}: {query.data.fee_bps / 100}%
              </p>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Fees are deducted from the seller for both platform and external payments. Insufficient seller balance automatically pauses trading.'
                )}
              </p>
            </div>
            <StorePaymentCategoriesForm
              key={JSON.stringify(query.data.categories)}
              categories={
                query.data.categories || {
                  platform_enabled: false,
                  external_enabled: false,
                }
              }
              onSaved={async () => {
                await client.invalidateQueries({
                  queryKey: ['store', 'payments', user.id],
                })
                await client.invalidateQueries({
                  queryKey: ['store', 'my-products', user.id],
                })
              }}
            />
            {(['platform', 'external'] as const).map((group) => (
              <section key={group} className='space-y-4'>
                <div className='space-y-2 border-b pb-3'>
                  <h2 className='font-semibold'>
                    {t(
                      group === 'platform'
                        ? 'Platform payments'
                        : 'External payments'
                    )}
                  </h2>
                  <p className='text-muted-foreground max-w-prose text-sm'>
                    {t(
                      group === 'platform'
                        ? 'Payments credit the seller’s platform account. Platform funds cannot be withdrawn and can only be spent here.'
                        : 'Payments go directly to your external merchant account. Configure your own gateway credentials.'
                    )}
                  </p>
                  {group === 'external' && (
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'External gateways require a platform balance greater than {{amount}}.',
                        { amount: '10 USD / 5,000,000 Credits' }
                      )}
                    </p>
                  )}
                </div>
                <div className='divide-y rounded-lg border'>
                  {PROVIDERS.filter((provider) =>
                    group === 'external'
                      ? provider.startsWith('external:')
                      : !provider.startsWith('external:')
                  ).map((provider) => (
                    <StoreGatewayEditor
                      key={`${user.id}-${provider}-${JSON.stringify(query.data!.items.find((item) => item.provider === provider))}`}
                      gateway={
                        query.data!.items.find(
                          (item) => item.provider === provider
                        ) || {
                          provider,
                          enabled: false,
                          configured: provider === 'balance',
                          has_key: false,
                          has_private_key: false,
                        }
                      }
                      eligible={
                        query.data!.external_eligible &&
                        query.data!.balance_quota > EXTERNAL_MINIMUM_QUOTA
                      }
                      onSaved={async () => {
                        await client.invalidateQueries({
                          queryKey: ['store', 'payments', user.id],
                        })
                      }}
                    />
                  ))}
                </div>
                {group === 'platform' &&
                  catalog.data?.platform_payment_catalog
                    ?.filter((item) => !item.supported)
                    .map((item) => (
                      <div
                        key={`${item.payment_type}-${item.name}`}
                        className='text-muted-foreground flex items-center justify-between gap-3 border-b py-3 text-sm'
                      >
                        <span>{item.name}</span>
                        <span>{t('Unavailable')}</span>
                      </div>
                    ))}
              </section>
            ))}
          </>
        )
      )}
      {catalog.data?.store_access_supported === true && (
        <StoreMerchantTermsEditor sellerId={user.id} />
      )}
      {user.role >= 100 && (
        <Button
          variant='outline'
          render={<a href='/system-settings/billing/payment#merchant-store' />}
        >
          {t(copy.adminLink)}
        </Button>
      )}
    </div>
  )
}
export function StoreGatewayEditor({
  gateway,
  eligible,
  onSaved,
}: {
  gateway: StoreGateway
  eligible: boolean
  onSaved: () => Promise<void>
}) {
  const { t } = useTranslation()
  const external = gateway.provider.startsWith('external:')
  const pancake = gateway.provider === 'external:waffo_pancake'
  const [draft, setDraft] = useState<StoreGatewayInput>(() => ({
    provider: gateway.provider,
    enabled: gateway.enabled,
    gateway_url: gateway.gateway_url || '',
    partner_id: gateway.partner_id || '',
    payment_type: gateway.payment_type || 'alipay',
    currency: gateway.currency || (pancake ? 'USD' : 'CNY'),
    merchant_id: gateway.merchant_id || '',
    store_id: gateway.store_id || '',
    product_id: gateway.product_id || '',
    environment: gateway.environment || 'prod',
    key: '',
    private_key: '',
  }))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [saved, setSaved] = useState(false)
  const change = <K extends keyof StoreGatewayInput>(
    key: K,
    value: StoreGatewayInput[K]
  ) => {
    setDraft((current) => ({ ...current, [key]: value }))
    setSaved(false)
  }
  async function save(event: React.FormEvent) {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await storeApi.savePaymentSettings(
        external ? draft : { provider: draft.provider, enabled: draft.enabled }
      )
      setDraft((current) => ({ ...current, key: '', private_key: '' }))
      setSaved(true)
      await onSaved()
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  const fields: [keyof StoreGatewayInput, string][] = pancake
    ? [
        ['merchant_id', 'Merchant ID'],
        ['store_id', 'Store ID'],
        ['product_id', 'Product ID'],
      ]
    : [
        ['gateway_url', 'Gateway URL'],
        ['partner_id', 'Merchant ID'],
        ['payment_type', 'Payment type'],
      ]
  const id = gateway.provider.replaceAll(':', '-')
  return (
    <form onSubmit={(event) => void save(event)} className='space-y-4 p-4'>
      <div className='flex items-start justify-between gap-4'>
        <div className='space-y-1'>
          <h3 className='font-medium'>
            {gateway.provider === 'external:epay'
              ? 'Epay'
              : t(paymentLabel(gateway.provider))}
          </h3>
          <p className='text-muted-foreground text-xs'>
            {t(gateway.configured ? 'Configured' : 'Configuration required')}
            {gateway.currency ? ` · ${gateway.currency}` : ''}
          </p>
        </div>
        <Switch
          checked={draft.enabled}
          aria-label={t('Enable {{method}}', {
            method: t(paymentLabel(gateway.provider)),
          })}
          disabled={
            busy ||
            (external && !eligible && !draft.enabled) ||
            (!external && !!gateway.unavailable_code && !draft.enabled)
          }
          onCheckedChange={(value) => change('enabled', value)}
        />
      </div>
      <StoreError error={error} />
      {external && (
        <div className='grid gap-4 sm:grid-cols-2'>
          {fields.map(([key, label]) => (
            <div key={key} className='space-y-2'>
              <Label htmlFor={`${id}-${key}`}>{t(label)}</Label>
              <Input
                id={`${id}-${key}`}
                value={String(draft[key] || '')}
                maxLength={2048}
                onChange={(event) => change(key, event.target.value)}
              />
            </div>
          ))}
          <div className='space-y-2'>
            <Label htmlFor={`${id}-currency`}>{t('Settlement currency')}</Label>
            <select
              id={`${id}-currency`}
              className='bg-background h-11 w-full rounded-md border px-3 text-sm'
              value={draft.currency}
              onChange={(event) => change('currency', event.target.value)}
            >
              {(pancake ? ['USD', 'CNY'] : ['CNY']).map((value) => (
                <option key={value} value={value}>
                  {value}
                </option>
              ))}
            </select>
          </div>
          {pancake && (
            <div className='space-y-2'>
              <Label htmlFor={`${id}-environment`}>{t('Environment')}</Label>
              <select
                id={`${id}-environment`}
                className='bg-background h-11 w-full rounded-md border px-3 text-sm'
                value={draft.environment}
                onChange={(event) => change('environment', event.target.value)}
              >
                <option value='prod'>{t('Production')}</option>
                <option value='test'>{t('Test')}</option>
              </select>
            </div>
          )}
          <div className='space-y-2 sm:col-span-2'>
            <Label htmlFor={`${id}-secret`}>
              {t(pancake ? 'Private key' : 'Signing key')}
            </Label>
            {pancake ? (
              <Textarea
                id={`${id}-secret`}
                autoComplete='off'
                spellCheck={false}
                rows={3}
                value={draft.private_key || ''}
                maxLength={16000}
                placeholder={t(
                  gateway.has_private_key
                    ? 'Leave blank to keep the saved secret'
                    : 'Enter private key'
                )}
                onChange={(event) => change('private_key', event.target.value)}
              />
            ) : (
              <Input
                id={`${id}-secret`}
                type='password'
                autoComplete='new-password'
                value={draft.key || ''}
                maxLength={512}
                placeholder={t(
                  gateway.has_key
                    ? 'Leave blank to keep the saved secret'
                    : 'Enter signing key'
                )}
                onChange={(event) => change('key', event.target.value)}
              />
            )}
            <p className='text-muted-foreground text-xs'>
              {t(
                'Credentials are encrypted on the server and never returned to the browser.'
              )}
            </p>
          </div>
        </div>
      )}
      {!external && gateway.unavailable_code && (
        <p className='text-muted-foreground text-xs'>
          {t(
            gateway.unavailable_code === 'merchant_settlement_unsupported'
              ? 'Unavailable'
              : 'This platform method is unavailable until an administrator configures it.'
          )}
        </p>
      )}
      {gateway.callback_urls &&
        Object.entries(gateway.callback_urls).map(([label, url]) => (
          <div key={label} className='space-y-2 border-t pt-3'>
            <p className='text-xs font-medium'>
              {t('Payment callback URL')} ({label})
            </p>
            <div className='flex flex-wrap items-center gap-3'>
              <code className='min-w-0 flex-1 text-xs break-all'>{url}</code>
              <CopyStoreValue value={url} />
            </div>
            {url.includes('{order_id}') && (
              <p className='text-muted-foreground text-xs'>
                {t(
                  'The exact callback URL is generated for each order. This is the callback template.'
                )}
              </p>
            )}
          </div>
        ))}
      <div className='flex items-center gap-3'>
        <Button type='submit' size='sm' variant='outline' disabled={busy}>
          {t(busy ? 'Saving...' : 'Save payment method')}
        </Button>
        {saved && (
          <span role='status' className='text-success text-xs'>
            {t('Saved')}
          </span>
        )}
      </div>
    </form>
  )
}
