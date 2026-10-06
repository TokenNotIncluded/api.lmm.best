/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { storeApi } from '@/features/store/api'
import { STORE_MINIMUM_PRICE_COPY as copy } from '@/features/store/minimum-price-copy'
import { StoreError, StoreLoading } from '@/features/store/shared'
import type { StoreConfig } from '@/features/store/types'
import { useMarketMoneyDraft } from '@/features/tool-market/money'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { useAuthStore } from '@/stores/auth-store'

export function MerchantStoreSettingsSection() {
  const user = useAuthStore((state) => state.auth.user)
  const client = useQueryClient()
  const root = (user?.role ?? 0) >= 100
  const query = useQuery({
    queryKey: ['store', 'config'],
    queryFn: storeApi.config,
    retry: false,
    enabled: root,
  })
  useEffect(() => {
    if (root && query.data && window.location.hash === '#merchant-store') {
      document
        .getElementById('merchant-store')
        ?.scrollIntoView({ block: 'start' })
    }
  }, [root, query.data])
  if (!root) return null
  return (
    <>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <StoreLoading />
      ) : (
        query.data && (
          <StoreRootConfig
            key={JSON.stringify(query.data)}
            config={query.data}
            onSaved={async () => {
              await client.invalidateQueries({ queryKey: ['store', 'config'] })
            }}
          />
        )
      )}
    </>
  )
}

function StoreRootConfig({
  config,
  onSaved,
}: {
  config: StoreConfig
  onSaved: () => Promise<void>
}) {
  const { t } = useTranslation()
  const money = useWalletCurrency()
  const promotion = useMarketMoneyDraft(config.promotion_quota)
  const minimum = useMarketMoneyDraft(
    config.minimum_unit_price_quota ?? Number.NaN
  )
  const [fee, setFee] = useState(String(config.fee_bps / 100))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  async function save(event: React.FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const bps = Math.round(Number(fee) * 100)
      if (
        !/^\d+(\.\d{1,2})?$/.test(fee) ||
        bps < 0 ||
        bps > 10000 ||
        !promotion.quota ||
        (config.minimum_unit_price_quota !== undefined &&
          minimum.quota === undefined)
      ) {
        throw new Error('Invalid amount')
      }
      await storeApi.saveConfig({
        fee_bps: bps,
        promotion_quota: promotion.quota,
        ...(config.minimum_unit_price_quota === undefined
          ? {}
          : {
              minimum_unit_price_quota: minimum.quota,
            }),
      })
      await onSaved()
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <section
      id='merchant-store'
      className='scroll-mt-6 space-y-4 border-t pt-5'
    >
      <h2 className='font-semibold'>{t('Store administration')}</h2>
      <form onSubmit={(event) => void save(event)} className='space-y-4'>
        <StoreError error={error} />
        <div className='grid gap-4 sm:grid-cols-2'>
          {config.minimum_unit_price_quota !== undefined && (
            <div className='space-y-2'>
              <Label htmlFor='store-minimum-price'>
                {t(copy.label)} ({money.label})
              </Label>
              <Input
                id='store-minimum-price'
                inputMode='decimal'
                value={minimum.input}
                onChange={(event) => minimum.setInput(event.target.value)}
              />
            </div>
          )}
          <div className='space-y-2'>
            <Label htmlFor='store-fee'>{t('Seller fee')} (%)</Label>
            <Input
              id='store-fee'
              inputMode='decimal'
              value={fee}
              onChange={(event) => setFee(event.target.value)}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='store-promotion-price'>
              {t('Monthly promotion price')} ({money.label})
            </Label>
            <Input
              id='store-promotion-price'
              inputMode='decimal'
              value={promotion.input}
              onChange={(event) => promotion.setInput(event.target.value)}
            />
          </div>
        </div>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Fees go to the configured super administrator. Purchases from that account’s own products are exempt from seller fees.'
          )}
        </p>
        <Button type='submit' disabled={busy}>
          {t(busy ? 'Saving...' : 'Save store settings')}
        </Button>
      </form>
    </section>
  )
}
