/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useCallback, useEffect, useRef, useState } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import { storeGuestApi, type StoreGuestSession } from './access-api'
import { STORE_ACCESS_COPY as copy } from './access-copy'
import { StoreAPIError, storeApi } from './api'
import {
  createStoreCheckoutIntentJournal,
  StoreCheckoutIntentError,
  storeCheckoutReplayFields,
  storeCheckoutActorScope,
  type StoreCheckoutActor,
  type StoreCheckoutIntentRecord,
  type StoreCheckoutSelection,
} from './checkout-intent'
import { readStoreGuestSession } from './guest-session'
import type {
  StoreCheckoutInput,
  StoreCheckoutResult,
  StoreOrder,
} from './types'

export function isStoreCheckoutActorCurrent(actor: StoreCheckoutActor) {
  const user = useAuthStore.getState().auth.user
  return actor.kind === 'account'
    ? user?.id === actor.accountId
    : !user && readStoreGuestSession()?.guest_id === actor.guestId
}

// The journal stores only identifiers and salted request digests. Credentials
// and the exact wire body are captured privately for each operation.
export function useStoreCheckoutRecovery({
  productId,
  supported,
  userId,
  guest,
}: {
  productId: string
  supported: boolean
  userId?: number
  guest: StoreGuestSession | null
}) {
  const [journal] = useState(() =>
    createStoreCheckoutIntentJournal({
      isActorCurrent: isStoreCheckoutActorCurrent,
    })
  )
  const [record, setRecord] = useState<StoreCheckoutIntentRecord | null>(null)
  const recordRef = useRef<StoreCheckoutIntentRecord | null>(null)
  const [storedOrder, setOrder] = useState<StoreOrder | null>(null)
  const [orderActor, setOrderActor] = useState('')
  const expectedActor = userId
    ? `account:${userId}`
    : guest
      ? `guest:${guest.guest_id.toLowerCase()}`
      : ''
  const order = orderActor === expectedActor ? storedOrder : null
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)
  const capture = useCallback(() => {
    const auth = useAuthStore.getState().auth
    const actor: StoreCheckoutActor | null = userId
      ? { kind: 'account', accountId: userId }
      : guest
        ? { kind: 'guest', guestId: guest.guest_id }
        : null
    if (!actor || !isStoreCheckoutActorCurrent(actor)) {
      throw new StoreCheckoutIntentError('actor-changed')
    }
    const scope = { userId: auth.user?.id, sessionId: auth.session?.sid }
    const assertCurrent = () => {
      if (!isStoreCheckoutActorCurrent(actor)) {
        throw new StoreCheckoutIntentError('actor-changed')
      }
      const now = useAuthStore.getState().auth
      if (
        now.user?.id !== scope.userId ||
        now.session?.sid !== scope.sessionId
      ) {
        throw new StoreCheckoutIntentError('actor-changed')
      }
    }
    const token = guest?.token
    return {
      actor,
      scope,
      assertCurrent,
      checkout: (body: StoreCheckoutInput) => {
        assertCurrent()
        return token
          ? storeGuestApi.checkout(token, body, scope)
          : storeApi.checkout(body, scope)
      },
      read: (id: string) => {
        assertCurrent()
        return token
          ? storeGuestApi.order(token, id, scope)
          : storeApi.order(id, scope)
      },
      lookup: (key: string) => {
        assertCurrent()
        return token
          ? storeGuestApi.lookup(token, key, scope)
          : storeGuestApi.memberLookup(key, scope)
      },
      pay: (id: string, currency?: string) => {
        assertCurrent()
        return token
          ? storeGuestApi.pay(token, id, currency, scope)
          : storeApi.pay(id, currency, scope)
      },
    }
  }, [guest, userId])
  function remember(value: StoreCheckoutIntentRecord) {
    recordRef.current = value
    setRecord(value)
  }
  const recover = useCallback(
    async (saved?: StoreCheckoutIntentRecord): Promise<StoreOrder | null> => {
      const credentials = capture()
      const head = saved ?? recordRef.current
      if (!head) return null
      let full: StoreOrder | null = null
      if (!supported && !head.orderId) {
        setError(new Error(copy.orderUnknown))
        return null
      }
      const response = await journal.recover(
        credentials.actor,
        head.requestKey,
        async (key) => {
          full = supported
            ? await credentials.lookup(key)
            : await credentials.read(head.orderId || '')
          return full ?? undefined
        }
      )
      credentials.assertCurrent()
      if (response.kind === 'found' && full) {
        remember(response.record)
        setOrderActor(storeCheckoutActorScope(credentials.actor))
        setOrder(full)
        setError(null)
        return full
      }
      setError(new Error(copy.orderAbsent))
      return null
    },
    [capture, journal, supported]
  )

  useEffect(() => {
    let active = true
    recordRef.current = null
    setRecord(null)
    setOrder(null)
    setError(null)
    if (!userId && !guest) {
      setLoading(false)
      return
    }
    setLoading(true)
    void (async () => {
      try {
        const credentials = capture()
        const records = await journal.list(credentials.actor)
        credentials.assertCurrent()
        if (!active) return
        const head = records.find(
          (item) => item.selection.productId === productId && !item.superseded
        )
        if (head) {
          remember(head)
          await recover(head)
        }
      } catch (issue) {
        if (active) setError(issue)
      } finally {
        if (active) setLoading(false)
      }
    })()
    return () => {
      active = false
    }
  }, [capture, guest, journal, productId, recover, userId])

  async function submit(
    selection: StoreCheckoutSelection,
    body: Omit<StoreCheckoutInput, 'request_key'>,
    retry = false
  ): Promise<StoreCheckoutResult | null> {
    const credentials = capture()
    const head = recordRef.current
    let prepared
    if (retry && head) {
      // The original versions are replay facts, never consent to updated terms.
      const exact = {
        ...storeCheckoutReplayFields(head),
        ...(body.pickup_code ? { pickup_code: body.pickup_code } : {}),
        ...(body.pickup_email ? { pickup_email: body.pickup_email } : {}),
      } as Omit<StoreCheckoutInput, 'request_key'>
      if (supported) {
        let found: StoreOrder | null = null
        const response = await journal.recover(
          credentials.actor,
          head.requestKey,
          async (key) => {
            found = await credentials.lookup(key)
            return found ?? undefined
          }
        )
        credentials.assertCurrent()
        if (response.kind === 'found' && found) {
          remember(response.record)
          setOrderActor(storeCheckoutActorScope(credentials.actor))
          setOrder(found)
          return { order: found, created: false }
        }
        if (response.kind !== 'absent') return null
        prepared = await journal.retryAfterAbsent(
          credentials.actor,
          response,
          exact
        )
      } else {
        prepared = await journal.replayUnknown(
          credentials.actor,
          head.requestKey,
          exact
        )
      }
    } else {
      prepared = await journal.prepare(credentials.actor, selection, body)
      remember(prepared.record)
      if (prepared.kind === 'recover') {
        const recovered = await recover(prepared.record)
        return recovered ? { order: recovered, created: false } : null
      }
    }
    prepared.assertActorCurrent()
    credentials.assertCurrent()
    let created: StoreCheckoutResult
    try {
      created = await credentials.checkout(prepared.request)
    } catch (issue) {
      if (
        supported &&
        issue instanceof StoreAPIError &&
        issue.code === 'STORE_TERMS_UPDATED' &&
        issue.requestKey === prepared.record.requestKey &&
        issue.orderCreated === false
      ) {
        await journal.releaseRejectedBeforeCreate(
          credentials.actor,
          prepared.record.requestKey,
          issue,
          prepared.record.revision
        )
        credentials.assertCurrent()
        recordRef.current = null
        setRecord(null)
        setOrder(null)
      }
      throw issue
    }
    setError(null)
    try {
      const saved = await journal.recordKnown(
        credentials.actor,
        prepared.record.requestKey,
        created.order,
        prepared.record.revision
      )
      credentials.assertCurrent()
      remember(saved)
    } catch (issue) {
      credentials.assertCurrent()
      setError(new Error(copy.orderReceiptFailed))
      // A real create receipt is still shown even if local persistence failed.
      // The stored unknown request remains available for a subsequent lookup.
      if (
        issue instanceof StoreCheckoutIntentError &&
        issue.reason === 'order-mismatch'
      ) {
        throw issue
      }
    }
    credentials.assertCurrent()
    setOrderActor(storeCheckoutActorScope(credentials.actor))
    setOrder(created.order)
    return created
  }
  async function refreshOrder(id: string) {
    const credentials = capture()
    const snapshot = recordRef.current
    const full = await credentials.read(id)
    if (snapshot) {
      const saved = await journal.recordKnown(
        credentials.actor,
        snapshot.requestKey,
        full,
        snapshot.revision
      )
      credentials.assertCurrent()
      remember(saved)
    }
    credentials.assertCurrent()
    setOrderActor(storeCheckoutActorScope(credentials.actor))
    setOrder(full)
    return full
  }
  async function pay(id: string, currency?: string) {
    const credentials = capture()
    const session = await credentials.pay(id, currency)
    credentials.assertCurrent()
    return session
  }
  async function minimumCancellation(issue: {
    code?: string
    orderId?: string
    orderStatus?: string
    orderCancelled?: boolean
  }) {
    const credentials = capture()
    const snapshot = recordRef.current
    if (!snapshot) throw new StoreCheckoutIntentError('needs-recovery')
    const saved = await journal.recordMinimumCancellation(
      credentials.actor,
      snapshot.requestKey,
      issue,
      snapshot.revision
    )
    credentials.assertCurrent()
    remember(saved)
    setOrder((previous) =>
      previous ? { ...previous, status: 'cancelled' } : null
    )
  }
  async function clear(buyAgain: boolean) {
    const credentials = capture()
    const saved = recordRef.current
    if (!saved?.orderId) throw new StoreCheckoutIntentError('needs-recovery')
    // A new server can refresh before accepting a local settled-state action.
    if (supported && !(await recover(saved))) {
      throw new StoreCheckoutIntentError('needs-recovery')
    }
    const current = recordRef.current
    if (!current?.orderId) throw new StoreCheckoutIntentError('needs-recovery')
    if (buyAgain) {
      await journal.beginNewPurchase(
        credentials.actor,
        current.requestKey,
        current.orderId
      )
    } else {
      await journal.cleanupOwnSettled(credentials.actor, [
        {
          requestKey: current.requestKey,
          orderId: current.orderId,
          revision: current.revision,
        },
      ])
    }
    credentials.assertCurrent()
    recordRef.current = null
    setRecord(null)
    setOrder(null)
    setError(null)
  }
  return {
    record: record?.actor === expectedActor ? record : null,
    order,
    loading,
    error,
    submit,
    recover,
    refreshOrder,
    pay,
    minimumCancellation,
    clear,
  }
}
