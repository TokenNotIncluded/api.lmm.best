/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { createInstance } from 'i18next'
import type { ComponentProps, ReactElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import type { HeroSmsSmsOrder } from './sms-api.js'
import { SmsBalanceNotice } from './sms-balance-notice.js'
import { getSmsPurchaseBalance } from './sms-balance.js'
import { SmsActiveOrdersCard } from './sms-order-sections.js'
import { SmsPurchaseCard } from './sms-purchase-card.js'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
  keySeparator: false,
})
const noop = () => undefined
const ready = { isPending: false, isError: false, onRetry: noop }
const purchaseProps: ComponentProps<typeof SmsPurchaseCard> = {
  language: 'en',
  services: [],
  countries: [],
  favoriteCountries: [],
  servicesState: ready,
  countriesState: ready,
  favorites: [],
  channel: 'sms',
  service: 'tg',
  country: '6',
  operator: '',
  operators: [],
  operatorsState: ready,
  selectedTierPrice: '',
  bidEnabled: false,
  bidPrice: '',
  quantity: 1,
  selectedIsFavorite: false,
  offer: {
    id: 'quote',
    country_id: 6,
    service: 'tg',
    operator: '',
    inventory: 1,
    customer_price_usd: '1',
    charge_quota: 500_000,
  },
  offerIsFetching: false,
  offerIsError: false,
  offerError: undefined,
  batchProgress: null,
  batchResult: null,
  batchFeedback: '',
  canPurchase: false,
  reconciliationPending: false,
  onChannelChange: noop,
  onServiceChange: noop,
  onCountryChange: noop,
  onOperatorChange: noop,
  onTierChange: noop,
  onBidEnabledChange: noop,
  onBidPriceChange: noop,
  onQuantityChange: noop,
  onSelectFavorite: noop,
  onRemoveFavorite: noop,
  onToggleFavorite: noop,
  onRefreshOffer: noop,
  onReconcile: noop,
  onPurchase: noop,
}

function render(element: ReactElement) {
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>{element}</I18nextProvider>
  )
}

function button(markup: string, text: string) {
  const buttons = markup.match(/<button\b[^>]*>[\s\S]*?<\/button>/g) ?? []
  const result = buttons.find(
    (candidate) => candidate.replaceAll(/<[^>]*>/g, '').trim() === text
  )
  assert.ok(result, `missing button: ${text}`)
  const attributes = result
    .slice(0, result.indexOf('>') + 1)
    .replaceAll(/="[^"]*"/g, '=""')
  // Inspect the native attribute, not Tailwind's disabled: style variants.
  return /\sdisabled(?:\s|=|>)/.test(attributes) ? 'disabled' : 'enabled'
}

describe('SMS balance notice and action boundaries', () => {
  test('below-floor notice is persistent and shows the actual USD balance', () => {
    const markup = render(
      <SmsBalanceNotice
        {...getSmsPurchaseBalance(4_999_999, 500_000)}
        isLoading={false}
        isRefreshing={false}
        onRefresh={noop}
      />
    )
    assert.match(markup, /role="status"/)
    assert.match(markup, /Minimum balance: USD 10/)
    assert.match(markup, /Current balance: USD 9\.999998/)
    assert.match(markup, /Existing orders can still receive codes/)
    assert.doesNotMatch(button(markup, 'Refresh balance'), /disabled/)
  })

  test('unknown balance offers a retry without inventing a zero balance', () => {
    const markup = render(
      <SmsBalanceNotice
        status='unknown'
        isLoading={false}
        isRefreshing={false}
        onRefresh={noop}
      />
    )
    assert.match(markup, /balance could not be verified/)
    assert.doesNotMatch(markup, /Current balance:|USD 0/)
    assert.doesNotMatch(button(markup, 'Refresh balance'), /disabled/)
  })

  test('only the new purchase is disabled below the floor; ambiguous replay stays available', () => {
    const markup = render(
      <SmsPurchaseCard
        {...purchaseProps}
        batchResult={{
          orders: [],
          requested: 1,
          failure: {
            code: 'REQUEST_FAILED',
            item: 1,
            ambiguous: true,
            offerId: 'quote',
            idempotencyKey: 'existing-key',
          },
        }}
      />
    )
    assert.match(button(markup, 'Buy phone activation'), /disabled/)
    assert.doesNotMatch(
      button(markup, 'Resolve purchase and continue'),
      /disabled/
    )
  })

  test('exactly USD 10 enables the purchase control', () => {
    const allowed = getSmsPurchaseBalance(5_000_000, 500_000)
    const markup = render(
      <SmsPurchaseCard
        {...purchaseProps}
        canPurchase={allowed.status === 'allowed'}
      />
    )
    assert.doesNotMatch(button(markup, 'Buy phone activation'), /disabled/)
    assert.equal(
      render(
        <SmsBalanceNotice
          {...allowed}
          isLoading={false}
          isRefreshing={false}
          onRefresh={noop}
        />
      ),
      ''
    )
  })

  test('an existing paid order keeps receiving and cancellation controls below the floor', () => {
    const order: HeroSmsSmsOrder = {
      id: 'paid-order',
      country_id: 6,
      service: 'tg',
      operator: '',
      status: 'active',
      customer_price_usd: '1',
      charge_quota: 500_000,
      refunded_quota: 0,
      provider_id: null,
      can_cancel: true,
      can_complain: false,
      phone_number: '79001234567',
      code: '123456',
      message: 'Code: 123456',
      last_error_code: '',
      last_error_message: '',
      created_at: 1,
      updated_at: 1,
    }
    const markup = render(
      <>
        <SmsBalanceNotice
          {...getSmsPurchaseBalance(0, 500_000)}
          isLoading={false}
          isRefreshing={false}
          onRefresh={noop}
        />
        <SmsActiveOrdersCard
          orders={[order]}
          countries={new Map()}
          services={new Map()}
          language='en'
          isPending={false}
          isError={false}
          errorTitle=''
          errorDescription=''
          onRetry={noop}
          refresh={{ onOrder: noop }}
          complaint={{ onOrder: noop }}
          cancel={{ onOrder: noop }}
        />
      </>
    )
    assert.match(markup, /123456/)
    assert.doesNotMatch(button(markup, 'Refresh'), /disabled/)
    assert.doesNotMatch(button(markup, 'Cancel and request refund'), /disabled/)
  })
})
