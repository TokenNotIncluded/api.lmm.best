/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  createStoreCheckoutIntentJournal,
  STORE_CHECKOUT_INTENT_STORAGE_KEY,
  StoreCheckoutIntentError,
  storeCheckoutActorScope,
  storeCheckoutReturnUrl,
  storeCheckoutReplayFields,
  type StoreCheckoutActor,
  type StoreCheckoutIntentFailure,
  type StoreCheckoutPrepared,
  type StoreCheckoutRecoveryOrder,
  type StoreCheckoutRejectedBeforeCreateProof,
  type StoreCheckoutSelection,
} from './checkout-intent'

class MemoryStorage {
  data = new Map<string, string>()
  failRead = false
  failWrite = false
  dropWrite = false
  getItem(key: string) {
    if (this.failRead) throw new Error('private browsing denied access')
    return this.data.get(key) ?? null
  }
  setItem(key: string, value: string) {
    if (this.failWrite) throw new Error('quota exceeded')
    if (!this.dropWrite) this.data.set(key, value)
  }
}

function locks() {
  let tail = Promise.resolve()
  const names: string[] = []
  return {
    names,
    async run<T>(name: string, task: () => Promise<T>) {
      names.push(name)
      const previous = tail
      let release = () => {}
      tail = new Promise<void>((resolve) => {
        release = resolve
      })
      await previous
      try {
        return await task()
      } finally {
        release()
      }
    },
  }
}
const account: StoreCheckoutActor = { kind: 'account', accountId: 12 }
const guest: StoreCheckoutActor = {
  kind: 'guest',
  guestId: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
}
const selection: StoreCheckoutSelection = {
  productId: 'product-a',
  variantId: 'variant-a',
  quantity: 2,
  promotionCode: 'SALE',
  cartItemId: 'cart-a',
}
const body = () => ({
  product_id: 'product-a',
  variant_id: 'variant-a',
  quantity: 2,
  promotion_code: 'SALE',
  payment_method: 'balance',
  disclaimer_version: 'v1',
  seller_terms_version: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
  accept_seller_terms: true,
  pickup_email: 'buyer@example.test',
  pickup_code: 'correct-secret',
  extra: { ids: ['one', 'two'] },
})
const order = (
  status = 'paid',
  patch: Partial<StoreCheckoutRecoveryOrder> = {}
): StoreCheckoutRecoveryOrder => ({
  id: 'order-a',
  product_id: 'product-a',
  variant_id: 'variant-a',
  quantity: 2,
  payment_method: 'balance',
  buyer_id: 12,
  status,
  ...patch,
})
function fixture(maxRecords?: number) {
  const storage = new MemoryStorage()
  const mutex = locks()
  let active: StoreCheckoutActor = account
  let clock = 1000
  const make = (crypto = globalThis.crypto) =>
    createStoreCheckoutIntentJournal({
      storage,
      crypto,
      withLock: mutex.run,
      now: () => clock,
      maxRecords,
      isActorCurrent: (actor) =>
        storeCheckoutActorScope(actor) === storeCheckoutActorScope(active),
    })
  return {
    storage,
    mutex,
    make,
    activate: (actor: StoreCheckoutActor) => {
      active = actor
    },
    advance: (amount: number) => {
      clock += amount
    },
  }
}
async function rejects(
  task: () => Promise<unknown>,
  reason: StoreCheckoutIntentFailure
) {
  await assert.rejects(
    task,
    (error: unknown) =>
      error instanceof StoreCheckoutIntentError && error.reason === reason
  )
}
async function saveKnown(
  journal: ReturnType<typeof createStoreCheckoutIntentJournal>,
  actor: StoreCheckoutActor,
  key: string,
  response: StoreCheckoutRecoveryOrder
) {
  const current = (await journal.list(actor)).find(
    (record) => record.requestKey === key
  )
  assert.ok(current)
  return journal.recordKnown(actor, key, response, current.revision)
}

test('return URL retains exact non-sensitive SKU quantity promotion and cart context and cannot escape the route', () => {
  assert.equal(
    storeCheckoutReturnUrl(selection),
    '/store/products/product-a?quantity=2&variant_id=variant-a&promotion=SALE&cart_item_id=cart-a'
  )
  assert.match(
    storeCheckoutReturnUrl({ ...selection, ownerPreview: true }),
    /^\/store\/preview\/product-a\?/
  )
  for (const invalid of [
    { ...selection, productId: '..' },
    { ...selection, productId: '//evil.test' },
    { ...selection, variantId: '../escape' },
    { ...selection, quantity: 0 },
    { ...selection, quantity: 1001 },
    { ...selection, pickup_email: 'secret@example.test' },
  ]) {
    assert.throws(
      () => storeCheckoutReturnUrl(invalid),
      StoreCheckoutIntentError
    )
  }
})

test('actor scope uses server guest IDs and does not accept guest credentials as identity', () => {
  assert.equal(storeCheckoutActorScope(account), 'account:12')
  assert.equal(
    storeCheckoutActorScope(guest),
    'guest:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
  )
  assert.throws(
    () =>
      storeCheckoutActorScope({ kind: 'guest', guestId: 'secret.jwt.token' }),
    StoreCheckoutIntentError
  )
  assert.throws(
    () => storeCheckoutActorScope({ kind: 'account', accountId: 0 }),
    StoreCheckoutIntentError
  )
})

test('first creation is durably marked unknown before a ready request; the wire snapshot is immutable and secrets are not stored', async () => {
  const f = fixture()
  const journal = f.make()
  const input = body()
  const prepared = await journal.prepare(account, selection, input)
  assert.equal(prepared.kind, 'ready')
  const stored = f.storage.getItem(STORE_CHECKOUT_INTENT_STORAGE_KEY)
  assert.ok(stored)
  assert.ok(stored.includes(prepared.request.request_key))
  assert.equal(JSON.parse(stored).records[0].state, 'unknown')
  for (const secret of [
    input.pickup_email,
    input.pickup_code,
    'pickup_email',
    'pickup_code',
    input.extra.ids[0],
  ]) {
    assert.equal(stored.includes(secret), false)
  }
  input.pickup_code = 'changed-secret'
  input.extra.ids[0] = 'changed-id'
  assert.equal(prepared.request.pickup_code, 'correct-secret')
  assert.deepEqual(prepared.request.extra.ids, ['one', 'two'])
  assert.equal(Object.isFrozen(prepared.request.extra.ids), true)
})

test('balance paid response loss and refresh preserve the original key; lookup can recover the paid order without another create', async () => {
  const f = fixture()
  const first = await f.make().prepare(account, selection, body())
  // A server balance charge succeeded, but its response never reached this tab.
  f.advance(365 * 86400000)
  const afterRefresh = f.make()
  const restored = await afterRefresh.prepare(account, selection, body())
  assert.equal(restored.kind, 'recover')
  assert.equal(restored.request.request_key, first.request.request_key)
  let lookedUp = ''
  const recovered = await afterRefresh.recover(
    account,
    first.record.requestKey,
    async (key) => {
      lookedUp = key
      return order()
    }
  )
  assert.equal(lookedUp, first.record.requestKey)
  assert.equal(recovered.kind, 'found')
  assert.equal((await afterRefresh.list(account))[0].orderStatus, 'paid')
  assert.equal(
    (await afterRefresh.prepare(account, selection, body())).kind,
    'recover'
  )
})

test('authorized lookup 404 keeps the unknown key and only permits exact same-key replay, including protected fields', async () => {
  const f = fixture()
  const journal = f.make()
  const initial = await journal.prepare(account, selection, body())
  const absent = await journal.recover(
    account,
    initial.record.requestKey,
    async () => undefined
  )
  assert.equal(absent.kind, 'absent')
  assert.equal((await journal.list(account))[0].state, 'unknown')
  if (absent.kind !== 'absent') throw new Error('expected absent')
  const retry = await journal.retryAfterAbsent(account, absent, body())
  assert.equal(retry.request.request_key, initial.request.request_key)
  await rejects(
    () =>
      journal.retryAfterAbsent(account, absent, {
        ...body(),
        pickup_code: 'other-secret',
      }),
    'request-mismatch'
  )
  await rejects(
    () =>
      journal.retryAfterAbsent(
        account,
        { kind: 'absent', record: initial.record },
        body()
      ),
    'needs-recovery'
  )
})

test('legacy explicit replay never calls an unsupported lookup and requires the original exact sensitive input', async () => {
  const f = fixture()
  const key = (await f.make().prepare(account, selection, body())).record
    .requestKey
  const refreshed = f.make()
  const retry = await refreshed.replayUnknown(account, key, body())
  assert.equal(retry.request.request_key, key)
  const { pickup_code: _omitted, ...missing } = body()
  await rejects(
    () => refreshed.replayUnknown(account, key, missing),
    'request-mismatch'
  )
})

test('lookup network or authorization errors never invent an absence or change the request key', async () => {
  const f = fixture()
  const journal = f.make()
  const key = (await journal.prepare(account, selection, body())).record
    .requestKey
  for (const failure of ['403', '401', 'network', '500']) {
    await assert.rejects(
      () =>
        journal.recover(account, key, async () => {
          throw new Error(failure)
        }),
      new RegExp(failure)
    )
    assert.equal((await journal.list(account))[0].requestKey, key)
    assert.equal((await journal.list(account))[0].state, 'unknown')
  }
})

test('all exact wire fields including terms, false/absent, sensitive input and array order change the fingerprint and block a fresh key', async () => {
  const f = fixture()
  const journal = f.make()
  await journal.prepare(account, selection, body())
  const { accept_seller_terms: _removed, ...absent } = body()
  for (const changed of [
    { ...body(), disclaimer_version: 'v2' },
    { ...body(), seller_terms_version: 'cccccccc-cccc-4ccc-8ccc-cccccccccccc' },
    { ...body(), accept_seller_terms: false },
    absent,
    { ...body(), pickup_email: 'other@example.test' },
    { ...body(), pickup_code: 'different-secret' },
    { ...body(), extra: { ids: ['two', 'one'] } },
  ]) {
    await rejects(
      () => journal.prepare(account, selection, changed),
      'needs-recovery'
    )
  }
  await rejects(
    () =>
      journal.prepare(
        account,
        { ...selection, cartItemId: 'different-cart' },
        body()
      ),
    'needs-recovery'
  )
  await rejects(
    () =>
      journal.prepare(
        account,
        { ...selection, variantId: 'variant-b' },
        { ...body(), variant_id: 'variant-b' }
      ),
    'needs-recovery'
  )
  const reordered = Object.fromEntries(Object.entries(body()).reverse())
  assert.equal(
    (await journal.prepare(account, selection, reordered)).kind,
    'recover'
  )
  assert.equal((await journal.list(account)).length, 1)
})

test('two tabs serialize the entire ledger, reuse one exact checkout key and do not lose another product or actor', async () => {
  const f = fixture()
  const [one, two] = await Promise.all([
    f.make().prepare(account, selection, body()),
    f.make().prepare(account, selection, body()),
  ])
  assert.equal(one.record.requestKey, two.record.requestKey)
  assert.deepEqual([one.kind, two.kind].sort(), ['ready', 'recover'])
  await Promise.all([
    f
      .make()
      .prepare(
        account,
        { ...selection, productId: 'product-b' },
        { ...body(), product_id: 'product-b' }
      ),
    f
      .make()
      .prepare(
        account,
        { ...selection, productId: 'product-c' },
        { ...body(), product_id: 'product-c' }
      ),
  ])
  assert.equal((await f.make().list(account)).length, 3)
  assert.deepEqual(
    new Set(f.mutex.names),
    new Set([STORE_CHECKOUT_INTENT_STORAGE_KEY])
  )
})

test('account and guest changes cannot reuse each other keys; guest free remains valid with its server identity', async () => {
  const f = fixture()
  const accountKey = (await f.make().prepare(account, selection, body())).record
    .requestKey
  f.activate(guest)
  const free = await f
    .make()
    .prepare(guest, selection, { ...body(), payment_method: 'free' })
  assert.notEqual(free.record.requestKey, accountKey)
  assert.equal((await f.make().list(guest)).length, 1)
  await rejects(
    () => f.make().prepare(account, selection, body()),
    'actor-changed'
  )
  f.activate(account)
  assert.equal(
    (await f.make().prepare(account, selection, body())).record.requestKey,
    accountKey
  )
})

test('actor switch while hashing prevents a new creation, and a previously prepared sender must check the current actor again', async () => {
  const f = fixture()
  let finish = () => {}
  let started = () => {}
  const began = new Promise<void>((resolve) => {
    started = resolve
  })
  const release = new Promise<void>((resolve) => {
    finish = resolve
  })
  const crypto = {
    getRandomValues: (bytes: Uint8Array<ArrayBuffer>) =>
      globalThis.crypto.getRandomValues(bytes),
    randomUUID: () => globalThis.crypto.randomUUID(),
    subtle: {
      digest: async (...args: Parameters<SubtleCrypto['digest']>) => {
        started()
        await release
        return globalThis.crypto.subtle.digest(...args)
      },
    },
  } as unknown as Crypto
  const pending = f.make(crypto).prepare(account, selection, body())
  await began
  f.activate(guest)
  finish()
  await rejects(() => pending, 'actor-changed')
  assert.equal(f.storage.getItem(STORE_CHECKOUT_INTENT_STORAGE_KEY), null)
  f.activate(account)
  const ready = await f.make().prepare(account, selection, body())
  f.activate(guest)
  assert.throws(
    () => ready.assertActorCurrent(),
    (error: unknown) =>
      error instanceof StoreCheckoutIntentError &&
      error.reason === 'actor-changed'
  )
})

test('late successful responses update only the captured old actor journal after an account switch', async () => {
  const f = fixture()
  const journal = f.make()
  const initial = await journal.prepare(account, selection, body())
  const key = initial.record.requestKey
  f.activate(guest)
  await journal.recordKnown(account, key, order(), initial.record.revision)
  await rejects(() => journal.list(account), 'actor-changed')
  assert.deepEqual(await journal.list(guest), [])
  f.activate(account)
  assert.equal((await journal.list(account))[0].orderStatus, 'paid')
})

test('corrupt JSON unsupported schemas and stored plaintext extras fail closed without overwriting existing data', async () => {
  const f = fixture()
  const valid = await f.make().prepare(account, selection, body())
  const raw = f.storage.getItem(STORE_CHECKOUT_INTENT_STORAGE_KEY)
  assert.ok(raw)
  const withSecret = JSON.parse(raw)
  withSecret.records[0].pickup_email = 'private@example.test'
  for (const [bad, reason] of [
    ['{broken', 'corrupt-storage'],
    [JSON.stringify({ schema: 2, records: [] }), 'unsupported-schema'],
    [JSON.stringify(withSecret), 'corrupt-storage'],
  ] as const) {
    f.storage.data.set(STORE_CHECKOUT_INTENT_STORAGE_KEY, bad)
    await rejects(() => f.make().prepare(account, selection, body()), reason)
    assert.equal(f.storage.getItem(STORE_CHECKOUT_INTENT_STORAGE_KEY), bad)
  }
  f.storage.data.set(STORE_CHECKOUT_INTENT_STORAGE_KEY, raw)
  assert.equal(
    (await f.make().prepare(account, selection, body())).record.requestKey,
    valid.record.requestKey
  )
})

test('quota private-browsing and dropped-write failures never return a safe prepared creation', async () => {
  for (const failure of ['read', 'write', 'drop']) {
    const f = fixture()
    f.storage.failRead = failure === 'read'
    f.storage.failWrite = failure === 'write'
    f.storage.dropWrite = failure === 'drop'
    await rejects(
      () => f.make().prepare(account, selection, body()),
      failure === 'drop' ? 'persistence-unverified' : 'storage-unavailable'
    )
    assert.equal(f.storage.data.size, 0)
  }
})

test('receipt persistence failure leaves the original unknown key recoverable and never converts failure to a fresh checkout', async () => {
  const f = fixture()
  const journal = f.make()
  const initial = await journal.prepare(account, selection, body())
  f.storage.failWrite = true
  await rejects(
    () => saveKnown(journal, account, initial.record.requestKey, order()),
    'storage-unavailable'
  )
  f.storage.failWrite = false
  const restored = await f.make().prepare(account, selection, body())
  assert.equal(restored.record.requestKey, initial.record.requestKey)
  assert.equal(restored.kind, 'recover')
  assert.equal(restored.record.state, 'unknown')
})

test('unknown and pending records never expire; full storage requires recovery and an explicit settled new-purchase action', async () => {
  const f = fixture(1)
  const journal = f.make()
  const initial = await journal.prepare(account, selection, body())
  f.advance(10 * 365 * 86400000)
  await rejects(
    () =>
      journal.prepare(
        account,
        { ...selection, productId: 'another' },
        { ...body(), product_id: 'another' }
      ),
    'full'
  )
  await rejects(
    () =>
      journal.beginNewPurchase(account, initial.record.requestKey, 'order-a'),
    'not-settled'
  )
  await saveKnown(journal, account, initial.record.requestKey, order('pending'))
  await rejects(
    () =>
      journal.beginNewPurchase(account, initial.record.requestKey, 'order-a'),
    'not-settled'
  )
  await saveKnown(
    journal,
    account,
    initial.record.requestKey,
    order('reconciliation_pending')
  )
  await rejects(
    () =>
      journal.beginNewPurchase(account, initial.record.requestKey, 'order-a'),
    'not-settled'
  )
  await saveKnown(
    journal,
    account,
    initial.record.requestKey,
    order('refund_pending')
  )
  await rejects(
    () =>
      journal.beginNewPurchase(account, initial.record.requestKey, 'order-a'),
    'not-settled'
  )
  await saveKnown(journal, account, initial.record.requestKey, order('paid'))
  await rejects(
    () =>
      journal.prepare(
        account,
        { ...selection, productId: 'another' },
        { ...body(), product_id: 'another' }
      ),
    'full'
  )
  await journal.beginNewPurchase(account, initial.record.requestKey, 'order-a')
  const next = await journal.prepare(account, selection, body())
  assert.equal(next.kind, 'ready')
  assert.notEqual(next.record.requestKey, initial.record.requestKey)
  assert.equal((await journal.list(account)).length, 1)
})

test('generic or mismatched minimum errors preserve the pending key; confirmed matching cancellation must save before a new purchase', async () => {
  const f = fixture()
  const journal = f.make()
  const key = (await journal.prepare(account, selection, body())).record
    .requestKey
  await saveKnown(journal, account, key, order('pending'))
  const pendingRevision = (await journal.list(account))[0].revision
  for (const error of [
    {},
    {
      code: 'STORE_PAYMENT_MINIMUM',
      orderId: 'order-a',
      orderStatus: 'pending',
      orderCancelled: false,
    },
    {
      code: 'STORE_PAYMENT_MINIMUM',
      orderId: 'other-order',
      orderStatus: 'cancelled',
      orderCancelled: true,
    },
  ]) {
    await rejects(
      () =>
        journal.recordMinimumCancellation(account, key, error, pendingRevision),
      'order-mismatch'
    )
    assert.equal((await journal.list(account))[0].state, 'pending')
  }
  const confirmed = {
    code: 'STORE_PAYMENT_MINIMUM',
    orderId: 'order-a',
    orderStatus: 'cancelled',
    orderCancelled: true,
  }
  f.storage.failWrite = true
  await rejects(
    () =>
      journal.recordMinimumCancellation(
        account,
        key,
        confirmed,
        pendingRevision
      ),
    'storage-unavailable'
  )
  f.storage.failWrite = false
  assert.equal((await journal.list(account))[0].state, 'pending')
  await journal.recordMinimumCancellation(
    account,
    key,
    confirmed,
    pendingRevision
  )
  assert.equal(
    (await journal.prepare(account, selection, body())).kind,
    'recover'
  )
  await journal.beginNewPurchase(account, key, 'order-a')
  const next = await journal.prepare(account, selection, {
    ...body(),
    payment_method: 'external:epay',
  })
  assert.notEqual(next.record.requestKey, key)
})

test('wrong order identity product SKU quantity or payment method cannot resolve an unknown request', async () => {
  const f = fixture()
  const journal = f.make()
  const key = (await journal.prepare(account, selection, body())).record
    .requestKey
  for (const changed of [
    { product_id: 'other' },
    { variant_id: 'other' },
    { quantity: 3 },
    { payment_method: 'external:epay' },
    { buyer_id: 99 },
  ]) {
    await rejects(
      () => saveKnown(journal, account, key, order('paid', changed)),
      'order-mismatch'
    )
    assert.equal((await journal.list(account))[0].state, 'unknown')
  }
  await saveKnown(journal, account, key, order('pending'))
  await rejects(
    () =>
      saveKnown(journal, account, key, order('paid', { id: 'other-order' })),
    'order-mismatch'
  )
})

test('credentials and a caller-supplied request key cannot enter the checkout fingerprint API', async () => {
  const f = fixture()
  for (const extra of [
    { guest_token: 'credential' },
    { authorization: 'Bearer credential' },
    { request_key: 'caller-key' },
  ]) {
    await rejects(
      () => f.make().prepare(account, selection, { ...body(), ...extra }),
      'invalid-intent'
    )
  }
  assert.equal(f.storage.data.size, 0)
})

test('a caller mutating its actor during an async hash cannot move a ready request to another account', async () => {
  const f = fixture()
  const mutable = { kind: 'account' as const, accountId: 12 }
  let release = () => {}
  let began = () => {}
  const waiting = new Promise<void>((resolve) => {
    release = resolve
  })
  const started = new Promise<void>((resolve) => {
    began = resolve
  })
  const crypto = {
    getRandomValues: (bytes: Uint8Array<ArrayBuffer>) =>
      globalThis.crypto.getRandomValues(bytes),
    randomUUID: () => globalThis.crypto.randomUUID(),
    subtle: {
      digest: async (...args: Parameters<SubtleCrypto['digest']>) => {
        began()
        await waiting
        return globalThis.crypto.subtle.digest(...args)
      },
    },
  } as unknown as Crypto
  const promise = f.make(crypto).prepare(mutable, selection, body())
  await started
  mutable.accountId = 13
  f.activate(mutable)
  release()
  await rejects(() => promise, 'actor-changed')
  assert.equal(f.storage.data.size, 0)
})

test('a late pending lookup cannot overwrite a concurrently saved paid receipt, and refunded cannot regress to paid', async () => {
  const f = fixture()
  const journal = f.make()
  const initial = await journal.prepare(account, selection, body())
  let finish: (value: StoreCheckoutRecoveryOrder) => void = () => {}
  let began = () => {}
  const started = new Promise<void>((resolve) => {
    began = resolve
  })
  const { recover } = journal
  const stale = recover(account, initial.record.requestKey, async () => {
    began()
    return new Promise<StoreCheckoutRecoveryOrder>((resolve) => {
      finish = resolve
    })
  })
  await started
  await journal.recordKnown(
    account,
    initial.record.requestKey,
    order('paid'),
    initial.record.revision
  )
  finish(order('pending'))
  await rejects(() => stale, 'stale-recovery')
  assert.equal((await journal.list(account))[0].orderStatus, 'paid')
  const paid = (await journal.list(account))[0]
  await journal.recordKnown(
    account,
    initial.record.requestKey,
    order('refunded'),
    paid.revision
  )
  const refunded = (await journal.list(account))[0]
  await rejects(
    () =>
      journal.recordKnown(
        account,
        initial.record.requestKey,
        order('paid'),
        paid.revision
      ),
    'stale-recovery'
  )
  await rejects(
    () =>
      journal.recordKnown(
        account,
        initial.record.requestKey,
        order('paid'),
        refunded.revision
      ),
    'stale-recovery'
  )
  assert.equal((await journal.list(account))[0].orderStatus, 'refunded')
})

test('an authorized absence proof is immutable and cannot be redirected to an unqueried request', async () => {
  const f = fixture()
  const journal = f.make()
  const one = await journal.prepare(account, selection, body())
  const two = await journal.prepare(
    account,
    { ...selection, productId: 'product-b' },
    { ...body(), product_id: 'product-b' }
  )
  const absent = await journal.recover(
    account,
    one.record.requestKey,
    async () => undefined
  )
  assert.equal(absent.kind, 'absent')
  assert.equal(Object.isFrozen(absent), true)
  assert.throws(() => Object.assign(absent, { record: two.record }), TypeError)
})

test('explicit settled cleanup frees bounded capacity, preserves pending records and cannot clear another actor', async () => {
  const f = fixture(2)
  const journal = f.make()
  const paid = await journal.prepare(account, selection, body())
  await saveKnown(journal, account, paid.record.requestKey, order())
  const pending = await journal.prepare(
    account,
    { ...selection, productId: 'product-b' },
    { ...body(), product_id: 'product-b' }
  )
  const current = (await journal.list(account)).find(
    (item) => item.requestKey === paid.record.requestKey
  )
  assert.ok(current?.orderId)
  const currentOrderId = current.orderId
  await rejects(
    () =>
      journal.cleanupOwnSettled(account, [
        {
          requestKey: pending.record.requestKey,
          orderId: 'not-known',
          revision: 0,
        },
      ]),
    'not-settled'
  )
  f.activate(guest)
  await rejects(
    () =>
      journal.cleanupOwnSettled(guest, [
        {
          requestKey: current.requestKey,
          orderId: currentOrderId,
          revision: current.revision,
        },
      ]),
    'request-mismatch'
  )
  f.activate(account)
  assert.equal(
    await journal.cleanupOwnSettled(account, [
      {
        requestKey: current.requestKey,
        orderId: currentOrderId,
        revision: current.revision,
      },
    ]),
    1
  )
  assert.deepEqual(
    (await journal.list(account)).map((item) => item.requestKey),
    [pending.record.requestKey]
  )
  assert.equal(
    (
      await journal.prepare(
        account,
        { ...selection, productId: 'product-c' },
        { ...body(), product_id: 'product-c' }
      )
    ).kind,
    'ready'
  )
})

test('unavailable Web Locks or cryptography fail closed instead of creating volatile request keys', async () => {
  const f = fixture()
  const noLock = createStoreCheckoutIntentJournal({
    storage: f.storage,
    isActorCurrent: () => true,
  })
  await rejects(
    () => noLock.prepare(account, selection, body()),
    'locking-unavailable'
  )
  await rejects(
    () => f.make({} as Crypto).prepare(account, selection, body()),
    'crypto-unavailable'
  )
  assert.equal(f.storage.data.size, 0)
})

test('an account switch while waiting for the storage lock prevents old-account reads and lookup requests', async () => {
  const f = fixture()
  const journal = f.make()
  const initial = await journal.prepare(account, selection, body())
  let release = () => {}
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  const holding = f.mutex.run(STORE_CHECKOUT_INTENT_STORAGE_KEY, async () => {
    await gate
  })
  let lookups = 0
  const recovery = journal.recover(
    account,
    initial.record.requestKey,
    async () => {
      lookups++
      return order()
    }
  )
  const listing = journal.list(account)
  f.activate(guest)
  release()
  await holding
  await rejects(() => recovery, 'actor-changed')
  await rejects(() => listing, 'actor-changed')
  assert.equal(lookups, 0)
  assert.deepEqual(await journal.list(guest), [])
})

test('queued receipt and cancellation writes use immutable captured response fields', async () => {
  const f = fixture()
  const journal = f.make()
  const initial = await journal.prepare(account, selection, body())
  let release = () => {}
  let gate = new Promise<void>((resolve) => {
    release = resolve
  })
  let holding = f.mutex.run(STORE_CHECKOUT_INTENT_STORAGE_KEY, async () => {
    await gate
  })
  const received = order('pending')
  const saving = journal.recordKnown(
    account,
    initial.record.requestKey,
    received,
    initial.record.revision
  )
  received.status = 'paid'
  received.id = 'mutated-order'
  release()
  await holding
  const pending = await saving
  assert.equal(pending.orderId, 'order-a')
  assert.equal(pending.orderStatus, 'pending')

  gate = new Promise<void>((resolve) => {
    release = resolve
  })
  holding = f.mutex.run(STORE_CHECKOUT_INTENT_STORAGE_KEY, async () => {
    await gate
  })
  const minimum = {
    code: 'STORE_PAYMENT_MINIMUM',
    orderId: 'order-a',
    orderStatus: 'cancelled',
    orderCancelled: true,
  }
  const cancellation = journal.recordMinimumCancellation(
    account,
    pending.requestKey,
    minimum,
    pending.revision
  )
  minimum.orderCancelled = false
  minimum.orderId = 'mutated-order'
  release()
  await holding
  const cancelled = await cancellation
  assert.equal(cancelled.orderId, 'order-a')
  assert.equal(cancelled.orderStatus, 'cancelled')
})

test('legacy refresh reconstructs old non-sensitive terms IDs for exact replay without accepting new terms', async () => {
  const f = fixture()
  const original = body()
  delete (original as Partial<typeof original>).extra
  const initial = await f.make().prepare(account, selection, original)
  const refreshed = f.make()
  const [saved] = await refreshed.list(account)
  const restored = storeCheckoutReplayFields(saved)
  assert.equal(restored.seller_terms_version, original.seller_terms_version)
  assert.equal(restored.disclaimer_version, 'v1')
  assert.equal(restored.accept_seller_terms, true)
  assert.equal('pickup_email' in restored, false)
  assert.equal('pickup_code' in restored, false)
  const input = {
    ...restored,
    pickup_email: original.pickup_email,
    pickup_code: original.pickup_code,
  }
  await rejects(
    () =>
      refreshed.replayUnknown(account, saved.requestKey, {
        ...input,
        seller_terms_version: 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
      }),
    'request-mismatch'
  )
  const replayed = await refreshed.replayUnknown(
    account,
    saved.requestKey,
    input
  )
  assert.equal(replayed.record.requestKey, initial.record.requestKey)
  assert.equal(
    replayed.request.seller_terms_version,
    original.seller_terms_version
  )
  const persisted = f.storage.getItem(STORE_CHECKOUT_INTENT_STORAGE_KEY)
  assert.ok(persisted)
  assert.equal(persisted.includes(original.pickup_email), false)
  assert.equal(persisted.includes(original.pickup_code), false)
})

test('clearing settled local records performs no checkout; only a subsequent explicit purchase creates a new key', async () => {
  const f = fixture()
  const journal = f.make()
  let creations = 0
  async function purchase() {
    const prepared = await journal.prepare(account, selection, body())
    prepared.assertActorCurrent()
    creations++
    await journal.recordKnown(
      account,
      prepared.record.requestKey,
      order(),
      prepared.record.revision
    )
    return prepared
  }
  const first = await purchase()
  const [saved] = await journal.list(account)
  assert.ok(saved.orderId)
  await journal.cleanupOwnSettled(account, [
    {
      requestKey: saved.requestKey,
      orderId: saved.orderId,
      revision: saved.revision,
    },
  ])
  assert.equal(creations, 1)
  assert.deepEqual(await journal.list(account), [])
  // The UI's separate Buy again click invokes purchase; clearing never does.
  const second = await purchase()
  assert.equal(creations, 2)
  assert.notEqual(second.record.requestKey, first.record.requestKey)
})

test('precreate terms proof releases only its unknown local request before a separate confirmed new purchase', async () => {
  const f = fixture()
  const journal = f.make()
  const initial = await journal.prepare(account, selection, body())
  let creations = 1
  await journal.releaseRejectedBeforeCreate(
    account,
    initial.record.requestKey,
    {
      code: 'STORE_TERMS_UPDATED',
      requestKey: initial.record.requestKey,
      orderCreated: false,
    },
    initial.record.revision
  )
  assert.equal(creations, 1)
  assert.deepEqual(await f.make().list(account), [])
  // Only a later explicit click after accepting the current version prepares
  // and sends the new purchase; release itself has no create/pay callback.
  const confirmed = {
    ...body(),
    seller_terms_version: 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
    accept_seller_terms: true,
  }
  const next = await journal.prepare(account, selection, confirmed)
  next.assertActorCurrent()
  creations++
  assert.equal(creations, 2)
  assert.notEqual(next.record.requestKey, initial.record.requestKey)
  assert.equal(
    next.record.replayVersions.seller_terms_version,
    confirmed.seller_terms_version
  )
})

test('precreate terms release rejects generic errors lookup absence and other request keys without clearing unknown', async () => {
  const f = fixture()
  const journal = f.make()
  const initial = await journal.prepare(account, selection, body())
  const key = initial.record.requestKey
  const stored = f.storage.getItem(STORE_CHECKOUT_INTENT_STORAGE_KEY)
  const invalid = [
    {},
    { code: 'NETWORK_ERROR' },
    { code: 'STORE_CONFLICT', requestKey: key, orderCreated: false },
    { code: 'STORE_TERMS_UPDATED', requestKey: key },
    { code: 'STORE_TERMS_UPDATED', requestKey: key, orderCreated: true },
  ]
  for (const proof of invalid) {
    await rejects(
      () => journal.releaseRejectedBeforeCreate(account, key, proof, 0),
      'needs-recovery'
    )
    assert.equal(f.storage.getItem(STORE_CHECKOUT_INTENT_STORAGE_KEY), stored)
  }
  const absence = await journal.recover(account, key, async () => undefined)
  assert.equal(absence.kind, 'absent')
  await rejects(
    () =>
      journal.releaseRejectedBeforeCreate(
        account,
        key,
        absence as unknown as StoreCheckoutRejectedBeforeCreateProof,
        0
      ),
    'needs-recovery'
  )
  await rejects(
    () =>
      journal.releaseRejectedBeforeCreate(
        account,
        key,
        {
          code: 'STORE_TERMS_UPDATED',
          requestKey: 'other-key',
          orderCreated: false,
        },
        0
      ),
    'request-mismatch'
  )
  assert.equal(f.storage.getItem(STORE_CHECKOUT_INTENT_STORAGE_KEY), stored)
})

test('precreate terms release checks current actor revision and absence of an already known order', async () => {
  const f = fixture()
  const journal = f.make()
  const initial = await journal.prepare(account, selection, body())
  const key = initial.record.requestKey
  const proof = {
    code: 'STORE_TERMS_UPDATED',
    requestKey: key,
    orderCreated: false,
  }
  f.activate(guest)
  await rejects(
    () => journal.releaseRejectedBeforeCreate(account, key, proof, 0),
    'actor-changed'
  )
  await rejects(
    () => journal.releaseRejectedBeforeCreate(guest, key, proof, 0),
    'request-mismatch'
  )
  f.activate(account)
  const pending = await journal.recordKnown(account, key, order('pending'), 0)
  await rejects(
    () => journal.releaseRejectedBeforeCreate(account, key, proof, 0),
    'stale-recovery'
  )
  await rejects(
    () =>
      journal.releaseRejectedBeforeCreate(
        account,
        key,
        proof,
        pending.revision
      ),
    'needs-recovery'
  )
  const paid = await journal.recordKnown(
    account,
    key,
    order(),
    pending.revision
  )
  await rejects(
    () =>
      journal.releaseRejectedBeforeCreate(account, key, proof, paid.revision),
    'needs-recovery'
  )
  assert.equal((await journal.list(account))[0].orderStatus, 'paid')
})

test('precreate terms release must persist its deletion before another request can be prepared', async () => {
  for (const mode of ['failWrite', 'dropWrite'] as const) {
    const f = fixture()
    const journal = f.make()
    const initial: StoreCheckoutPrepared<ReturnType<typeof body>> =
      await journal.prepare(account, selection, body())
    const key: string = initial.record.requestKey
    const stored = f.storage.getItem(STORE_CHECKOUT_INTENT_STORAGE_KEY)
    f.storage[mode] = true
    await rejects(
      () =>
        journal.releaseRejectedBeforeCreate(
          account,
          key,
          {
            code: 'STORE_TERMS_UPDATED',
            requestKey: key,
            orderCreated: false,
          },
          0
        ),
      mode === 'failWrite' ? 'storage-unavailable' : 'persistence-unverified'
    )
    f.storage[mode] = false
    assert.equal(f.storage.getItem(STORE_CHECKOUT_INTENT_STORAGE_KEY), stored)
    await rejects(
      () =>
        journal.prepare(account, selection, {
          ...body(),
          seller_terms_version: 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
        }),
      'needs-recovery'
    )
    assert.equal((await f.make().list(account))[0].requestKey, key)
  }
})
