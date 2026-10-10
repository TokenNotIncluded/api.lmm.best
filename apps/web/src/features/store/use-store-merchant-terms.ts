/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useCallback, useEffect, useRef, useState } from 'react'

import { storeAccessApi } from './access-api'
import type { StoreSellerTerms } from './access-types'
// Accepted status is actor-specific; keep the read and checkbox out of the
// shared product query cache, including for independent guest credentials.
export function useStoreMerchantTerms(
  productId: string,
  supported: boolean,
  actorId?: number,
  guestToken?: string
) {
  const [terms, setTerms] = useState<StoreSellerTerms | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [loading, setLoading] = useState(supported)
  const [acceptedSignature, setAcceptedSignature] = useState('')
  const generation = useRef(0)
  const refresh = useCallback(async () => {
    const current = ++generation.current
    if (!supported) {
      setTerms(null)
      setLoading(false)
      return
    }
    setLoading(true)
    setError(null)
    try {
      const result = await storeAccessApi.productTerms(productId, guestToken)
      if (current === generation.current) setTerms(result)
    } catch (issue) {
      if (current === generation.current) {
        setError(issue)
        setTerms(null)
      }
    } finally {
      if (current === generation.current) setLoading(false)
    }
  }, [productId, supported, guestToken])
  useEffect(() => {
    setTerms(null)
    setAcceptedSignature('')
    void refresh()
    return () => {
      // eslint-disable-next-line react-hooks/exhaustive-deps -- Invalidates late network replies; this is not a DOM ref.
      generation.current++
    }
  }, [refresh, actorId])
  const signature = terms ? JSON.stringify([terms.version, terms.content]) : ''
  const accepted = !!signature && signature === acceptedSignature
  const ready =
    !supported ||
    (!loading && !error && !!terms?.configured && !!terms.version && accepted)
  return {
    terms,
    error,
    loading,
    accepted,
    ready,
    refresh,
    setAccepted: (value: boolean) =>
      setAcceptedSignature(value ? signature : ''),
  }
}
