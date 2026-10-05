/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { beforeEach, afterEach, test } from 'node:test'

import { mapStatusDataToConfig } from '@/hooks/use-system-config'
import i18n from '@/i18n/config'
import { formatQuotaInCurrency, formatUSDInCurrency } from '@/lib/currency'
import { api } from '@/lib/http-client'
import { useSystemConfigStore } from '@/stores/system-config-store'

import {
  parsePublicCreditOption,
  updatePublicCreditUnitOption,
} from './public-credit-unit'

const originalGet = api.get
const originalPut = api.put
const originalConfig = useSystemConfigStore.getState().config
const metadata = (p = 100000) => ({
  currency_unit: 'credit',
  credit_unit_schema_version: 2,
  credits_per_usd: 3359744,
  credits_per_usd_exact: '3359744',
  ledger_quota_per_usd: 3359744,
  ledger_quota_per_usd_exact: '3359744',
  public_credits_per_usd: p,
  public_credits_per_usd_exact: String(p),
  quota_unit: 'LEDGER_QUOTA',
  public_credit_unit: 'CREDIT',
  legacy_credit_unit: 'LEDGER_QUOTA',
  cny_per_usd: 6.719488,
  quota_per_unit: 500000,
})
const request = {
  key: 'PublicCreditsPerUSD',
  value: '200000',
  publicCreditUnitBaseline: {
    publicCreditsPerUsd: 100000,
    ledgerQuotaPerUsd: 3359744,
  },
}
beforeEach(async () => {
  await i18n.changeLanguage('en')
  useSystemConfigStore.getState().setConfig(mapStatusDataToConfig(metadata()))
})
afterEach(() => {
  api.get = originalGet
  api.put = originalPut
  useSystemConfigStore.setState({ config: originalConfig })
})

test('parses only a real positive safe-integer option with no default face value', () => {
  assert.equal(parsePublicCreditOption('100000'), 100000)
  for (const input of [
    undefined,
    '',
    '0',
    '-1',
    '0.5',
    '1e5',
    'Infinity',
    '9007199254740992',
  ]) {
    assert.equal(parsePublicCreditOption(input), undefined)
  }
})

test('v2 CAS waits for verified live status before adopting the new credit display', async () => {
  const paths: string[] = []
  const writes: unknown[] = []
  let finish: ((value: unknown) => void) | undefined
  api.get = (async (url: string) => {
    paths.push(url)
    if (url === '/api/option/public-credit-unit') {
      return { data: { success: true, data: metadata() } }
    }
    assert.equal(url, '/api/status')
    return await new Promise<unknown>((resolve) => {
      finish = resolve
    })
  }) as typeof api.get
  api.put = (async (url: string, body: unknown) => {
    paths.push(url)
    writes.push(body)
    return { data: { success: true, data: metadata(200000) } }
  }) as typeof api.put
  const saving = updatePublicCreditUnitOption(request)
  for (let i = 0; i < 10 && !finish; i++) await Promise.resolve()
  assert.ok(finish)
  assert.equal(
    formatQuotaInCurrency(3359744, 'CREDIT', {
      locale: 'en',
      creditLabel: 'Credits',
    }),
    '100,000 Credits'
  )
  finish({ data: { success: true, data: metadata(200000) } })
  assert.equal((await saving).success, true)
  assert.deepEqual(paths, [
    '/api/option/public-credit-unit',
    '/api/option/public-credit-unit',
    '/api/status',
  ])
  assert.deepEqual(writes, [
    {
      credit_unit_schema_version: 2,
      public_credits_per_usd_exact: '200000',
      expected_public_credits_per_usd_exact: '100000',
      expected_ledger_quota_per_usd_exact: '3359744',
    },
  ])
  assert.equal(
    formatQuotaInCurrency(3359744, 'CREDIT', {
      locale: 'en',
      creditLabel: 'Credits',
    }),
    '200,000 Credits'
  )
  assert.equal(formatQuotaInCurrency(3359744, 'USD', { locale: 'en' }), '1 USD')
  assert.equal(formatUSDInCurrency(4, 'USD'), '4 USD')
  assert.equal(
    useSystemConfigStore.getState().config.currency.creditsPerUsd,
    3359744
  )
})

test('unsupported servers or incomplete metadata never receive a generic setter', async () => {
  let puts = 0
  api.put = (async () => {
    puts++
    throw new Error('unexpected setter')
  }) as typeof api.put
  for (const data of [
    { credits_per_usd: 3359744 },
    { ...metadata(), public_credit_unit: 'LEDGER_QUOTA' },
  ]) {
    api.get = (async (url: string) => {
      assert.equal(url, '/api/option/public-credit-unit')
      return { data: { success: true, data } }
    }) as typeof api.get
    await assert.rejects(updatePublicCreditUnitOption(request), /unavailable/)
  }
  api.get = (async () => {
    throw Object.assign(new Error('not found'), { response: { status: 404 } })
  }) as typeof api.get
  await assert.rejects(updatePublicCreditUnitOption(request))
  assert.equal(puts, 0)
})

test('stale baselines and actual v2 CAS conflicts preserve current display', async () => {
  let puts = 0
  api.get = (async () => ({
    data: { success: true, data: metadata(150000) },
  })) as typeof api.get
  api.put = (async () => {
    puts++
    throw Object.assign(new Error('conflict'), { response: { status: 409 } })
  }) as typeof api.put
  await assert.rejects(updatePublicCreditUnitOption(request), /changed/)
  assert.equal(puts, 0)
  api.get = (async () => ({
    data: { success: true, data: metadata() },
  })) as typeof api.get
  await assert.rejects(updatePublicCreditUnitOption(request))
  assert.equal(puts, 1)
  assert.equal(
    useSystemConfigStore.getState().config.currency.publicCreditsPerUsd,
    100000
  )
})

test('a mixed old node cannot reinterpret the guarded v2 write', async () => {
  api.get = (async () => ({
    data: { success: true, data: metadata() },
  })) as typeof api.get
  api.put = (async (url: string) => {
    assert.equal(url, '/api/option/public-credit-unit')
    throw Object.assign(new Error('not found'), { response: { status: 404 } })
  }) as typeof api.put
  await assert.rejects(updatePublicCreditUnitOption(request))
  assert.equal(
    useSystemConfigStore.getState().config.currency.publicCreditsPerUsd,
    100000
  )
})

test('mismatched or legacy refreshed status cannot adopt an optimistic credit face value', async () => {
  api.put = (async () => ({
    data: { success: true, data: metadata(200000) },
  })) as typeof api.put
  for (const data of [
    metadata(100000),
    { ...metadata(200000), credits_per_usd: 200000 },
    { currency_unit: 'credit', credits_per_usd: 3359744 },
  ]) {
    api.get = (async (url: string) => ({
      data: { success: true, data: url === '/api/status' ? data : metadata() },
    })) as typeof api.get
    await assert.rejects(
      updatePublicCreditUnitOption(request),
      /could not be verified/
    )
    assert.equal(
      useSystemConfigStore.getState().config.currency.publicCreditsPerUsd,
      100000
    )
  }
})

test('invalid, unsafe or unguarded values never reach the network', async () => {
  api.get = (async () => {
    throw new Error('unexpected network')
  }) as typeof api.get
  for (const value of [
    '',
    '0',
    '-1',
    '100000.5',
    '9007199254740992',
    '1e-400',
  ]) {
    await assert.rejects(
      updatePublicCreditUnitOption({ ...request, value }),
      /positive whole number/
    )
  }
  await assert.rejects(
    updatePublicCreditUnitOption({
      key: 'PublicCreditsPerUSD',
      value: '200000',
    }),
    /positive whole number/
  )
})
