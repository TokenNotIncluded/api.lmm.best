/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */

export type StoreCheckoutActor =
  | { kind: 'account'; accountId: number }
  | { kind: 'guest'; guestId: string }

export interface StoreCheckoutSelection {
  productId: string
  variantId?: string
  quantity: number
  promotionCode?: string
  cartItemId?: string
  ownerPreview?: boolean
}

export interface StoreCheckoutReplayVersions {
  disclaimer_version?: string
  seller_terms_version?: string
  accept_seller_terms?: boolean
}

export interface StoreCheckoutIntentRecord {
  actor: string
  selection: StoreCheckoutSelection
  paymentMethod: string
  // Old version IDs and acceptance reconstruct an exact same-key replay only.
  // They never constitute acceptance of a later version for a new purchase.
  replayVersions: StoreCheckoutReplayVersions
  requestKey: string
  salt: string
  digest: string
  state: 'unknown' | 'pending' | 'known'
  orderId?: string
  orderStatus?: string
  superseded: boolean
  createdAt: number
  updatedAt: number
  revision: number
}

export interface StoreCheckoutRecoveryOrder {
  id: string
  product_id: string
  variant_id?: string
  quantity: number
  payment_method: string
  buyer_id?: number
  status: string
}

export type StoreCheckoutIntentFailure =
  | 'invalid-intent'
  | 'actor-changed'
  | 'crypto-unavailable'
  | 'locking-unavailable'
  | 'storage-unavailable'
  | 'corrupt-storage'
  | 'unsupported-schema'
  | 'persistence-unverified'
  | 'full'
  | 'needs-recovery'
  | 'request-mismatch'
  | 'order-mismatch'
  | 'not-settled'
  | 'stale-recovery'

export const STORE_CHECKOUT_INTENT_ERROR_COPY: Record<
  StoreCheckoutIntentFailure,
  string
> = {
  'invalid-intent':
    'Checkout details are invalid. Refresh the product and try again.',
  'actor-changed':
    'Your checkout account changed. Check the current account before ordering.',
  'crypto-unavailable': 'This browser cannot safely save checkout recovery.',
  'locking-unavailable':
    'This browser cannot safely coordinate checkout recovery.',
  'storage-unavailable':
    'Checkout recovery could not be saved. Check the original order before trying again.',
  'corrupt-storage':
    'Saved checkout recovery cannot be read. Check existing orders before ordering again.',
  'unsupported-schema':
    'Saved checkout recovery uses a different version. Check existing orders before ordering again.',
  'persistence-unverified': 'Checkout recovery could not be verified.',
  full: 'Checkout recovery is full. Recover existing orders before starting another purchase.',
  'needs-recovery':
    'Recover the previous checkout before changing this purchase.',
  'request-mismatch': 'These checkout details differ from the saved request.',
  'order-mismatch': 'The recovered order does not match this checkout.',
  'not-settled':
    'This checkout is still unresolved. Check the original order first.',
  'stale-recovery':
    'This checkout changed while checking the order. Check the original order again.',
}

// The UI decides the appropriate message for preparation versus a response
// already received. Never describe a failed receipt write as "no order sent".
export class StoreCheckoutIntentError extends Error {
  constructor(readonly reason: StoreCheckoutIntentFailure) {
    super(STORE_CHECKOUT_INTENT_ERROR_COPY[reason])
    this.name = 'StoreCheckoutIntentError'
  }
}

type StoragePort = Pick<Storage, 'getItem' | 'setItem'>
type ExclusiveLock = <T>(name: string, task: () => Promise<T>) => Promise<T>
interface JournalOptions {
  isActorCurrent: (actor: StoreCheckoutActor) => boolean
  storage?: StoragePort
  crypto?: Crypto
  withLock?: ExclusiveLock
  now?: () => number
  maxRecords?: number
}
interface Ledger {
  schema: 1
  records: StoreCheckoutIntentRecord[]
}

export const STORE_CHECKOUT_INTENT_STORAGE_KEY = 'lmm:store:checkout-intents'
export const STORE_CHECKOUT_INTENT_RECORD_LIMIT = 32
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const ID = /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/
const HEX = /^[0-9a-f]{64}$/
const SETTLED = new Set(['paid', 'cancelled', 'expired', 'refunded'])
const SELECTION_FIELDS = [
  'productId',
  'variantId',
  'quantity',
  'promotionCode',
  'cartItemId',
  'ownerPreview',
]
const RECORD_FIELDS = [
  'actor',
  'selection',
  'paymentMethod',
  'replayVersions',
  'requestKey',
  'salt',
  'digest',
  'state',
  'orderId',
  'orderStatus',
  'superseded',
  'createdAt',
  'updatedAt',
  'revision',
]

function fail(reason: StoreCheckoutIntentFailure): never {
  throw new StoreCheckoutIntentError(reason)
}
function plain(value: unknown): value is Record<string, unknown> {
  return (
    !!value &&
    typeof value === 'object' &&
    !Array.isArray(value) &&
    [Object.prototype, null].includes(Object.getPrototypeOf(value))
  )
}
function only(value: Record<string, unknown>, keys: string[]) {
  return Object.keys(value).every((key) => keys.includes(key))
}
function optionalId(value: unknown) {
  return value === undefined || (typeof value === 'string' && ID.test(value))
}
function validReplayVersions(
  value: unknown
): value is StoreCheckoutReplayVersions {
  return (
    plain(value) &&
    only(value, [
      'disclaimer_version',
      'seller_terms_version',
      'accept_seller_terms',
    ]) &&
    optionalId(value.disclaimer_version) &&
    (value.seller_terms_version === undefined ||
      (typeof value.seller_terms_version === 'string' &&
        UUID.test(value.seller_terms_version))) &&
    (value.accept_seller_terms === undefined ||
      typeof value.accept_seller_terms === 'boolean')
  )
}
function replayVersions(
  body: Record<string, unknown>
): StoreCheckoutReplayVersions {
  const value = Object.fromEntries(
    ['disclaimer_version', 'seller_terms_version', 'accept_seller_terms']
      .filter((key) => body[key] !== undefined)
      .map((key) => [key, body[key]])
  )
  if (!validReplayVersions(value)) return fail('invalid-intent')
  return freeze(value)
}
function validSelection(value: unknown): value is StoreCheckoutSelection {
  if (!plain(value) || !only(value, SELECTION_FIELDS)) return false
  return (
    typeof value.productId === 'string' &&
    ID.test(value.productId) &&
    optionalId(value.variantId) &&
    optionalId(value.cartItemId) &&
    Number.isSafeInteger(value.quantity) &&
    Number(value.quantity) >= 1 &&
    Number(value.quantity) <= 1000 &&
    (value.ownerPreview === undefined ||
      typeof value.ownerPreview === 'boolean') &&
    (value.promotionCode === undefined ||
      (typeof value.promotionCode === 'string' &&
        value.promotionCode.length > 0 &&
        value.promotionCode.length <= 256 &&
        !Array.from(value.promotionCode).some((char) => {
          const code = char.charCodeAt(0)
          return code < 32 || code === 127
        })))
  )
}

export function storeCheckoutActorScope(actor: StoreCheckoutActor): string {
  if (
    actor.kind === 'account' &&
    Number.isSafeInteger(actor.accountId) &&
    actor.accountId > 0
  ) {
    return `account:${actor.accountId}`
  }
  if (actor.kind === 'guest' && UUID.test(actor.guestId)) {
    return `guest:${actor.guestId.toLowerCase()}`
  }
  return fail('invalid-intent')
}
function captureActor(value: StoreCheckoutActor): StoreCheckoutActor {
  const actor: StoreCheckoutActor =
    value.kind === 'account'
      ? { kind: 'account', accountId: value.accountId }
      : { kind: 'guest', guestId: value.guestId }
  storeCheckoutActorScope(actor)
  return Object.freeze(actor)
}

// This is navigation context, never proof of inventory, access or a discount.
// The receiving checkout must resolve the IDs and obtain a fresh server quote.
export function storeCheckoutReturnUrl(
  selection: StoreCheckoutSelection
): string {
  if (!validSelection(selection)) return fail('invalid-intent')
  const params = new URLSearchParams({ quantity: String(selection.quantity) })
  if (selection.variantId) params.set('variant_id', selection.variantId)
  if (selection.promotionCode) params.set('promotion', selection.promotionCode)
  if (selection.cartItemId) params.set('cart_item_id', selection.cartItemId)
  return `/store/${selection.ownerPreview ? 'preview' : 'products'}/${encodeURIComponent(selection.productId)}?${params}`
}

// Only non-sensitive persisted wire fields are returned. A legacy replay must
// add the original protected input and pass the full fingerprint comparison.
export function storeCheckoutReplayFields(record: StoreCheckoutIntentRecord) {
  return freeze({
    product_id: record.selection.productId,
    ...(record.selection.variantId
      ? { variant_id: record.selection.variantId }
      : {}),
    quantity: record.selection.quantity,
    payment_method: record.paymentMethod,
    ...(record.selection.promotionCode
      ? { promotion_code: record.selection.promotionCode }
      : {}),
    ...record.replayVersions,
  })
}

function canonical(
  value: unknown,
  inArray = false,
  depth = 0
): string | undefined {
  if (depth > 12) return fail('invalid-intent')
  if (value === undefined) return inArray ? fail('invalid-intent') : undefined
  if (
    value === null ||
    typeof value === 'string' ||
    typeof value === 'boolean'
  ) {
    return JSON.stringify(value)
  }
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) return fail('invalid-intent')
    return JSON.stringify(value)
  }
  if (Array.isArray(value)) {
    return `[${Array.from(value, (item) => canonical(item, true, depth + 1)).join(',')}]`
  }
  if (!plain(value)) return fail('invalid-intent')
  return `{${Object.keys(value)
    .sort()
    .flatMap((key) => {
      const item = canonical(value[key], false, depth + 1)
      return item === undefined ? [] : [`${JSON.stringify(key)}:${item}`]
    })
    .join(',')}}`
}
function freeze<T>(value: T): T {
  if (value && typeof value === 'object') {
    for (const child of Object.values(value)) freeze(child)
    Object.freeze(value)
  }
  return value
}
function hex(value: Uint8Array) {
  return Array.from(value, (byte) => byte.toString(16).padStart(2, '0')).join(
    ''
  )
}
function validRecord(value: unknown): value is StoreCheckoutIntentRecord {
  if (
    !plain(value) ||
    !only(value, RECORD_FIELDS) ||
    !validSelection(value.selection) ||
    !validReplayVersions(value.replayVersions)
  ) {
    return false
  }
  if (
    typeof value.actor !== 'string' ||
    !(
      /^account:[1-9]\d*$/.test(value.actor) ||
      (value.actor.startsWith('guest:') && UUID.test(value.actor.slice(6)))
    )
  ) {
    return false
  }
  if (
    typeof value.paymentMethod !== 'string' ||
    !/^[a-z][a-z0-9:_-]{0,100}$/.test(value.paymentMethod) ||
    typeof value.requestKey !== 'string' ||
    !UUID.test(value.requestKey) ||
    typeof value.salt !== 'string' ||
    !HEX.test(value.salt) ||
    typeof value.digest !== 'string' ||
    !HEX.test(value.digest) ||
    !['unknown', 'pending', 'known'].includes(String(value.state)) ||
    typeof value.superseded !== 'boolean' ||
    !Number.isSafeInteger(value.createdAt) ||
    !Number.isSafeInteger(value.updatedAt) ||
    !Number.isSafeInteger(value.revision) ||
    Number(value.revision) < 0 ||
    Number(value.createdAt) < 0 ||
    Number(value.updatedAt) < Number(value.createdAt) ||
    !optionalId(value.orderId) ||
    (value.orderStatus !== undefined &&
      (typeof value.orderStatus !== 'string' ||
        !/^[a-z_]{1,40}$/.test(value.orderStatus)))
  ) {
    return false
  }
  if (value.state === 'unknown') {
    return !value.orderId && !value.orderStatus && !value.superseded
  }
  if (!value.orderId || !value.orderStatus) return false
  const settled = SETTLED.has(String(value.orderStatus))
  return (
    value.state === (settled ? 'known' : 'pending') &&
    (!value.superseded || settled)
  )
}

export interface StoreCheckoutPrepared<T> {
  kind: 'ready' | 'recover'
  record: StoreCheckoutIntentRecord
  // Sensitive input lives only in this deep-frozen transient snapshot.
  request: Readonly<T & { request_key: string }>
  assertActorCurrent: () => void
}
export interface StoreCheckoutLookupAbsence {
  readonly kind: 'absent'
  readonly record: StoreCheckoutIntentRecord
}

/**
 * Storage and lock failures fail closed. Successful set/readback protects a
 * refresh; it cannot promise retention after private-session closure or manual
 * browser-data deletion. No local timestamp makes an unknown payment safe to
 * forget. UI recovery must query the original authenticated request key.
 */
export function createStoreCheckoutIntentJournal(options: JournalOptions) {
  const limit = options.maxRecords ?? STORE_CHECKOUT_INTENT_RECORD_LIMIT
  if (
    !Number.isSafeInteger(limit) ||
    limit < 1 ||
    limit > STORE_CHECKOUT_INTENT_RECORD_LIMIT
  ) {
    fail('invalid-intent')
  }
  const now = options.now ?? Date.now
  const absences = new WeakSet<StoreCheckoutLookupAbsence>()
  function storage() {
    try {
      const value = options.storage ?? globalThis.localStorage
      if (!value) return fail('storage-unavailable')
      return value
    } catch {
      return fail('storage-unavailable')
    }
  }
  function cryptography() {
    const value = options.crypto ?? globalThis.crypto
    if (!value?.subtle || !value.getRandomValues || !value.randomUUID) {
      return fail('crypto-unavailable')
    }
    return value
  }
  function assertCurrent(actor: StoreCheckoutActor) {
    storeCheckoutActorScope(actor)
    if (!options.isActorCurrent(actor)) fail('actor-changed')
  }
  async function locked<T>(task: () => Promise<T>): Promise<T> {
    if (options.withLock) {
      return options.withLock(STORE_CHECKOUT_INTENT_STORAGE_KEY, task)
    }
    if (!globalThis.navigator?.locks) return fail('locking-unavailable')
    // The lock covers the whole blob, including writes by other actors/tabs.
    return navigator.locks.request(STORE_CHECKOUT_INTENT_STORAGE_KEY, task)
  }
  function read(): Ledger {
    let raw: string | null
    try {
      raw = storage().getItem(STORE_CHECKOUT_INTENT_STORAGE_KEY)
    } catch {
      return fail('storage-unavailable')
    }
    if (raw === null) return { schema: 1, records: [] }
    if (raw.length > 65536) return fail('corrupt-storage')
    let value: unknown
    try {
      value = JSON.parse(raw)
    } catch {
      return fail('corrupt-storage')
    }
    if (!plain(value)) return fail('corrupt-storage')
    if (value.schema !== 1) return fail('unsupported-schema')
    if (
      !only(value, ['schema', 'records']) ||
      !Array.isArray(value.records) ||
      value.records.length > STORE_CHECKOUT_INTENT_RECORD_LIMIT ||
      !value.records.every(validRecord) ||
      new Set(value.records.map((record) => record.requestKey)).size !==
        value.records.length
    ) {
      return fail('corrupt-storage')
    }
    return value as unknown as Ledger
  }
  function write(ledger: Ledger) {
    const raw = JSON.stringify(ledger)
    try {
      const target = storage()
      target.setItem(STORE_CHECKOUT_INTENT_STORAGE_KEY, raw)
      if (target.getItem(STORE_CHECKOUT_INTENT_STORAGE_KEY) !== raw) {
        fail('persistence-unverified')
      }
    } catch (error) {
      if (error instanceof StoreCheckoutIntentError) throw error
      fail('storage-unavailable')
    }
  }
  function record(ledger: Ledger, actor: StoreCheckoutActor, key: string) {
    const result = ledger.records.find(
      (entry) =>
        entry.actor === storeCheckoutActorScope(actor) &&
        entry.requestKey === key
    )
    if (!result) return fail('request-mismatch')
    return result
  }
  function snapshot<T extends object>(
    selection: StoreCheckoutSelection,
    body: T
  ): T {
    if (
      !validSelection(selection) ||
      !plain(body) ||
      'request_key' in body ||
      body.product_id !== selection.productId ||
      body.quantity !== selection.quantity ||
      (body.variant_id ?? undefined) !== selection.variantId ||
      (body.promotion_code ?? undefined) !== selection.promotionCode ||
      typeof body.payment_method !== 'string' ||
      !/^[a-z][a-z0-9:_-]{0,100}$/.test(body.payment_method) ||
      Object.keys(body).some((key) => /token|authorization|password/i.test(key))
    ) {
      return fail('invalid-intent')
    }
    const serialized = canonical(body)
    if (!serialized || new TextEncoder().encode(serialized).length > 65536) {
      return fail('invalid-intent')
    }
    replayVersions(body)
    return freeze(JSON.parse(serialized) as T)
  }
  async function fingerprint(
    actor: string,
    selection: StoreCheckoutSelection,
    body: object,
    salt: string
  ) {
    try {
      const serialized = canonical([1, salt, actor, selection, body])
      if (!serialized) return fail('invalid-intent')
      const bytes = new TextEncoder().encode(serialized)
      return hex(
        new Uint8Array(await cryptography().subtle.digest('SHA-256', bytes))
      )
    } catch (error) {
      if (error instanceof StoreCheckoutIntentError) throw error
      return fail('crypto-unavailable')
    }
  }
  function prepared<T>(
    actor: StoreCheckoutActor,
    value: StoreCheckoutIntentRecord,
    body: T,
    kind: 'ready' | 'recover'
  ): StoreCheckoutPrepared<T> {
    return {
      kind,
      record: freeze(structuredClone(value)),
      request: freeze({ ...body, request_key: value.requestKey }),
      assertActorCurrent: () => assertCurrent(actor),
    }
  }
  async function recordKnown(
    identity: StoreCheckoutActor,
    key: string,
    response: StoreCheckoutRecoveryOrder,
    expectedRevision: number
  ) {
    const actor = captureActor(identity)
    const order = Object.freeze({
      id: response.id,
      product_id: response.product_id,
      variant_id: response.variant_id,
      quantity: response.quantity,
      payment_method: response.payment_method,
      buyer_id: response.buyer_id,
      status: response.status,
    })
    return locked(async () => {
      const ledger = read()
      const value = record(ledger, actor, key)
      if (value.revision !== expectedRevision) return fail('stale-recovery')
      if (
        !ID.test(order.id) ||
        !/^[a-z_]{1,40}$/.test(order.status) ||
        order.product_id !== value.selection.productId ||
        order.quantity !== value.selection.quantity ||
        order.payment_method !== value.paymentMethod ||
        (value.selection.variantId &&
          order.variant_id !== value.selection.variantId) ||
        (actor.kind === 'account' &&
          order.buyer_id !== undefined &&
          order.buyer_id !== actor.accountId) ||
        (value.orderId && value.orderId !== order.id)
      ) {
        return fail('order-mismatch')
      }
      if (
        (value.orderStatus === 'paid' && order.status === 'pending') ||
        (value.orderStatus === 'refunded' && order.status !== 'refunded')
      ) {
        return fail('stale-recovery')
      }
      value.orderId = order.id
      value.orderStatus = order.status
      value.state = SETTLED.has(order.status) ? 'known' : 'pending'
      value.updatedAt = Math.max(now(), value.createdAt)
      value.revision++
      // A stale response cannot make a settled superseded record pending.
      if (value.superseded && value.state !== 'known') {
        return fail('order-mismatch')
      }
      write(ledger)
      return freeze(structuredClone(value))
    })
  }

  return {
    async list(identity: StoreCheckoutActor) {
      const actor = captureActor(identity)
      const scope = storeCheckoutActorScope(actor)
      assertCurrent(actor)
      return locked(async () => {
        assertCurrent(actor)
        return freeze(read().records.filter((entry) => entry.actor === scope))
      })
    },
    async prepare<T extends object>(
      identity: StoreCheckoutActor,
      selection: StoreCheckoutSelection,
      body: T
    ): Promise<StoreCheckoutPrepared<T>> {
      const actor = captureActor(identity)
      const exact = snapshot(selection, body)
      const chosen = freeze(structuredClone(selection))
      const scope = storeCheckoutActorScope(actor)
      assertCurrent(actor)
      return locked(async () => {
        assertCurrent(actor)
        const ledger = read()
        const heads = ledger.records.filter(
          (entry) =>
            entry.actor === scope &&
            entry.selection.productId === chosen.productId &&
            !entry.superseded
        )
        for (const head of heads) {
          const digest = await fingerprint(scope, chosen, exact, head.salt)
          assertCurrent(actor)
          if (head.digest === digest) {
            return prepared(actor, head, exact, 'recover')
          }
        }
        if (heads.length) return fail('needs-recovery')
        // Only explicitly superseded settled histories are eligible for pruning.
        ledger.records = ledger.records.filter((entry) => !entry.superseded)
        if (ledger.records.length >= limit) return fail('full')
        const crypto = cryptography()
        const salt = hex(crypto.getRandomValues(new Uint8Array(32)))
        const digest = await fingerprint(scope, chosen, exact, salt)
        assertCurrent(actor)
        const timestamp = now()
        const value: StoreCheckoutIntentRecord = {
          actor: scope,
          selection: chosen,
          paymentMethod: (exact as Record<string, unknown>)
            .payment_method as string,
          replayVersions: replayVersions(exact as Record<string, unknown>),
          requestKey: crypto.randomUUID(),
          salt,
          digest,
          state: 'unknown',
          superseded: false,
          createdAt: timestamp,
          updatedAt: timestamp,
          revision: 0,
        }
        ledger.records.push(value)
        write(ledger)
        assertCurrent(actor)
        return prepared(actor, value, exact, 'ready')
      })
    },
    // A response received after an account switch still belongs to its captured
    // actor's record. It must never be applied to the new account's checkout UI.
    recordKnown,
    async recover(
      identity: StoreCheckoutActor,
      key: string,
      lookup: (key: string) => Promise<StoreCheckoutRecoveryOrder | undefined>
    ) {
      const actor = captureActor(identity)
      assertCurrent(actor)
      const before = await locked(async () => {
        assertCurrent(actor)
        return structuredClone(record(read(), actor, key))
      })
      // No global storage lock is held over a network lookup. Only an actual
      // authorized 404 may be mapped to undefined; 401/403/5xx must throw.
      assertCurrent(actor)
      const order = await lookup(before.requestKey)
      if (order) {
        const value = await recordKnown(actor, key, order, before.revision)
        assertCurrent(actor)
        return { kind: 'found' as const, record: value, order }
      }
      assertCurrent(actor)
      const absence: StoreCheckoutLookupAbsence = freeze({
        kind: 'absent' as const,
        record: freeze(before),
      })
      absences.add(absence)
      return absence
    },
    async retryAfterAbsent<T extends object>(
      identity: StoreCheckoutActor,
      absence: StoreCheckoutLookupAbsence,
      body: T
    ) {
      const actor = captureActor(identity)
      if (!absences.has(absence)) return fail('needs-recovery')
      assertCurrent(actor)
      const exact = snapshot(absence.record.selection, body)
      return locked(async () => {
        const value = record(read(), actor, absence.record.requestKey)
        if (value.state !== 'unknown' || value.orderId) {
          return fail('needs-recovery')
        }
        if (
          value.digest !==
          (await fingerprint(value.actor, value.selection, exact, value.salt))
        ) {
          return fail('request-mismatch')
        }
        assertCurrent(actor)
        return prepared(actor, value, exact, 'ready')
      })
    },
    // Legacy servers have no lookup route. Only a user-requested replay of the
    // unchanged wire request can retry an unknown create, always with its old
    // key. Secrets missing after a refresh must be entered again and match.
    async replayUnknown<T extends object>(
      identity: StoreCheckoutActor,
      key: string,
      body: T
    ) {
      const actor = captureActor(identity)
      assertCurrent(actor)
      return locked(async () => {
        const value = record(read(), actor, key)
        if (value.state !== 'unknown' || value.orderId) {
          return fail('needs-recovery')
        }
        const exact = snapshot(value.selection, body)
        if (
          value.digest !==
          (await fingerprint(value.actor, value.selection, exact, value.salt))
        ) {
          return fail('request-mismatch')
        }
        assertCurrent(actor)
        return prepared(actor, value, exact, 'ready')
      })
    },
    // Explicit user cleanup only. For servers with lookup support the caller
    // first refreshes each order; it must never pass unknown or pending keys.
    // This deletes local requests, not real order history. A clear success must
    // never invoke create: buying again requires a separate user action.
    async cleanupOwnSettled(
      identity: StoreCheckoutActor,
      verified: Array<{
        requestKey: string
        orderId: string
        revision: number
      }>
    ) {
      const actor = captureActor(identity)
      const chosen = freeze(structuredClone(verified))
      if (
        chosen.length > limit ||
        new Set(chosen.map((item) => item.requestKey)).size !== chosen.length
      ) {
        return fail('invalid-intent')
      }
      assertCurrent(actor)
      return locked(async () => {
        assertCurrent(actor)
        const ledger = read()
        for (const item of chosen) {
          const value = record(ledger, actor, item.requestKey)
          if (value.revision !== item.revision) return fail('stale-recovery')
          if (
            value.state !== 'known' ||
            !SETTLED.has(value.orderStatus || '')
          ) {
            return fail('not-settled')
          }
          if (value.orderId !== item.orderId) return fail('order-mismatch')
        }
        const keys = new Set(chosen.map((item) => item.requestKey))
        ledger.records = ledger.records.filter(
          (item) =>
            item.actor !== storeCheckoutActorScope(actor) ||
            !keys.has(item.requestKey)
        )
        write(ledger)
        return chosen.length
      })
    },
    // Called only by an explicit new-purchase action after displaying the
    // recovered order. Generic create/pay/network errors never call this.
    async beginNewPurchase(
      identity: StoreCheckoutActor,
      key: string,
      orderId: string
    ) {
      const actor = captureActor(identity)
      assertCurrent(actor)
      return locked(async () => {
        assertCurrent(actor)
        const ledger = read()
        const value = record(ledger, actor, key)
        if (value.state !== 'known' || !SETTLED.has(value.orderStatus || '')) {
          return fail('not-settled')
        }
        if (value.orderId !== orderId) return fail('order-mismatch')
        value.superseded = true
        value.revision++
        value.updatedAt = Math.max(now(), value.createdAt)
        write(ledger)
      })
    },
    async recordMinimumCancellation(
      identity: StoreCheckoutActor,
      key: string,
      response: {
        code?: string
        orderId?: string
        orderStatus?: string
        orderCancelled?: boolean
      },
      expectedRevision: number
    ) {
      const actor = captureActor(identity)
      const error = Object.freeze({
        code: response.code,
        orderId: response.orderId,
        orderStatus: response.orderStatus,
        orderCancelled: response.orderCancelled,
      })
      return locked(async () => {
        const ledger = read()
        const value = record(ledger, actor, key)
        if (value.revision !== expectedRevision) return fail('stale-recovery')
        if (
          value.state !== 'pending' ||
          error.code !== 'STORE_PAYMENT_MINIMUM' ||
          error.orderCancelled !== true ||
          error.orderStatus !== 'cancelled' ||
          !value.orderId ||
          error.orderId !== value.orderId
        ) {
          return fail('order-mismatch')
        }
        value.orderStatus = 'cancelled'
        value.state = 'known'
        value.revision++
        value.updatedAt = Math.max(now(), value.createdAt)
        write(ledger)
        return freeze(structuredClone(value))
      })
    },
  }
}
