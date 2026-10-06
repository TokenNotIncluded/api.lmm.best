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
import { beforeEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'
import { createInstance } from 'i18next'
import type { ComponentProps, ReactElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import appI18n from '@/i18n/config'
import { useAuthStore } from '@/stores/auth-store'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'
import { useWalletCurrencyPreferenceStore } from '@/stores/wallet-currency-preference-store'

import type { HeroSmsSmsOrder } from './sms-api.js'
import { SmsBalanceNotice } from './sms-balance-notice.js'
import { getSmsPurchaseBalance } from './sms-balance.js'
import { describeSmsAccessError } from './sms-error.js'
import {
  SmsActiveOrdersCard,
  SmsOrderHistoryCard,
} from './sms-order-sections.js'
import { SmsPurchaseCard } from './sms-purchase-card.js'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
  keySeparator: false,
})
beforeEach(async () => {
  useAuthStore.getState().auth.setUser(null)
  useWalletCurrencyPreferenceStore.getState().setPreference('')
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      currencyUnit: 'credit',
      creditsPerUsd: 500_000,
      creditsPerUsdExact: '500000',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
      legacyPricingUnitsPerUsd: 1,
      quotaPerUnit: 500_000,
      usdExchangeRate: 7,
    },
  })
  await i18n.changeLanguage('en')
  await appI18n.changeLanguage('en')
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
  // SSR reads Zustand's server snapshot, so expose this test's complete
  // currency fixture instead of the application's unloaded initial config.
  const authState = useAuthStore.getInitialState()
  const configState = useSystemConfigStore.getInitialState()
  const preferenceState = useWalletCurrencyPreferenceStore.getInitialState()
  const authSnapshot = authState.auth
  const configSnapshot = configState.config
  const preferenceSnapshot = preferenceState.preference
  authState.auth = useAuthStore.getState().auth
  configState.config = useSystemConfigStore.getState().config
  preferenceState.preference =
    useWalletCurrencyPreferenceStore.getState().preference
  try {
    return renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>{element}</I18nextProvider>
    )
  } finally {
    authState.auth = authSnapshot
    configState.config = configSnapshot
    preferenceState.preference = preferenceSnapshot
  }
}

function button(markup: string, text: string) {
  const window = new Window()
  window.document.body.innerHTML = markup
  const result = [...window.document.querySelectorAll('button')].find(
    (candidate) => candidate.textContent?.trim() === text
  )
  assert.ok(result, `missing button: ${text}`)
  // Inspect the native attribute, not Tailwind's disabled: style variants.
  const state = result.hasAttribute('disabled') ? 'disabled' : 'enabled'
  window.close()
  return state
}

describe('SMS balance notice and action boundaries', () => {
  test('turns access errors into actionable reasons', () => {
    const t = i18n.t.bind(i18n)
    assert.deepEqual(
      describeSmsAccessError(
        Object.assign(new Error('denied'), {
          code: 'TEMPORARY_SMS_MINIMUM_BALANCE',
        }),
        t
      ),
      {
        title: 'Insufficient quota',
        description:
          'Temporary SMS purchases require a balance of at least 10 USD',
      }
    )
    assert.deepEqual(
      describeSmsAccessError(
        {
          response: {
            status: 500,
            data: {
              code: 'FEATURE_NOT_UNLOCKED',
              message: 'feature is locked',
            },
          },
        },
        t
      ),
      {
        title: 'Purchasing unavailable',
        description:
          'A funded, active account gradually unlocks more tools and better rates. Your current level is shown in the wallet.',
      }
    )
    assert.deepEqual(
      describeSmsAccessError(
        Object.assign(new Error('HeroSMS SMS purchasing is disabled'), {
          code: 'NOT_CONFIGURED',
        }),
        t
      ),
      {
        title: 'Purchasing unavailable',
        description: 'HeroSMS purchasing is disabled',
      }
    )
  })

  test('provider balance is never described as the customer balance floor', () => {
    const t = i18n.t.bind(i18n)
    const result = describeSmsAccessError(
      {
        response: {
          status: 503,
          data: {
            code: 'PROVIDER_BALANCE_INSUFFICIENT',
            message: 'HeroSMS provider balance is insufficient',
          },
        },
      },
      t
    )
    assert.equal(result.title, 'Purchasing unavailable')
    assert.match(result.description, /provider/)
    assert.doesNotMatch(result.description, /at least|10 USD/)
  })

  test('uses a captured minimum with its existing denomination in access errors', () => {
    const error = Object.assign(new Error('denied'), {
      code: 'TEMPORARY_SMS_MINIMUM_BALANCE',
    })
    for (const minimum of ['10 CNY', '5,000,000 Credits']) {
      assert.equal(
        describeSmsAccessError(error, i18n.t.bind(i18n), minimum).description,
        `Temporary SMS purchases require a balance of at least ${minimum}`
      )
    }
  })

  test('below-floor notice preserves the raw boundary and shows the actual USD balance', () => {
    const markup = render(
      <SmsBalanceNotice
        {...getSmsPurchaseBalance(4_999_999, 500_000)}
        isLoading={false}
        isRefreshing={false}
        onRefresh={noop}
      />
    )
    assert.match(markup, /role="status"/)
    assert.match(markup, /Minimum balance: 10 USD/)
    assert.match(markup, /Current balance: 9\.999998 USD/)
    assert.doesNotMatch(markup, /1\.428571|\{\{/)
    assert.match(markup, /Existing orders can still receive codes/)
    assert.doesNotMatch(button(markup, 'Refresh balance'), /disabled/)
  })

  test('the same raw boundary is shown as CNY or smallest credit units', () => {
    const balance = getSmsPurchaseBalance(4_999_999, 500_000)
    useWalletCurrencyPreferenceStore.getState().setPreference('CNY')
    const yuanMarkup = render(
      <SmsBalanceNotice
        {...balance}
        isLoading={false}
        isRefreshing={false}
        onRefresh={noop}
      />
    )
    assert.match(yuanMarkup, /Minimum balance: 70 CNY/)
    assert.match(yuanMarkup, /Current balance: 69\.999986 CNY/)
    useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
    const creditMarkup = render(
      <SmsBalanceNotice
        {...balance}
        isLoading={false}
        isRefreshing={false}
        onRefresh={noop}
      />
    )
    assert.match(creditMarkup, /Minimum balance: 5,000,000 Credits/)
    assert.match(creditMarkup, /Current balance: 4,999,999 Credits/)
    assert.doesNotMatch(creditMarkup, /\{\{|\(Platform\)/)
    assert.equal(balance.status, 'below-minimum')
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
    assert.doesNotMatch(markup, /Current balance:|\b0 USD/)
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

  test('exactly 5,000,000 raw credits enables the purchase control', () => {
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

test('historical SMS prices display their stored raw debit instead of today’s legacy quote scale', () => {
  const order: HeroSmsSmsOrder = {
    id: 'historical-micro-price',
    country_id: 6,
    service: 'tg',
    operator: '',
    status: 'completed',
    customer_price_usd: '0.000011',
    charge_quota: 6,
    refunded_quota: 0,
    provider_id: null,
    phone_number: '79001234567',
    code: '123456',
    message: '',
    last_error_code: '',
    last_error_message: '',
    created_at: 1,
    updated_at: 1,
  }
  const component = (
    <SmsOrderHistoryCard
      orders={[order]}
      countries={new Map()}
      services={new Map()}
      language='en'
      isPending={false}
      isError={false}
      errorTitle=''
      errorDescription=''
      onRetry={noop}
      onOpenOrder={noop}
      onRemoveOrder={noop}
      onClearHistory={noop}
      cleanupPending={false}
    />
  )
  assert.match(render(component), /0\.000012 USD/)
  const config = useSystemConfigStore.getState().config.currency
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...config, quotaPerUnit: 1000000 } })
  assert.match(render(component), /0\.000012 USD/)
  useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  assert.match(render(component), /6 Credits/)
  assert.equal(order.charge_quota, 6)
  assert.equal(order.customer_price_usd, '0.000011')
})
