/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useEffect, useRef, useState } from 'react'

import { storeGuestEmailApi } from './access-api'
import { isStoreEmail } from './utils'

export function useStoreGuestEmailVerification(
  token: string | undefined,
  email: string
) {
  const [verifiedEmail, setVerifiedEmail] = useState('')
  const [challenge, setChallenge] = useState<{
    id: string
    expires: number
  } | null>(null)
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [resendAt, setResendAt] = useState(0)
  const [now, setNow] = useState(Date.now())
  const generation = useRef(0)
  useEffect(() => {
    const current = ++generation.current
    setVerifiedEmail('')
    setChallenge(null)
    setCode('')
    setError(null)
    setBusy(false)
    setChecking(false)
    setResendAt(0)
    if (!token || !isStoreEmail(email)) return
    setChecking(true)
    void storeGuestEmailApi
      .status(token, email)
      .then((result) => {
        if (current === generation.current && result.verified === true) {
          setVerifiedEmail(email)
        }
      })
      .catch((issue) => {
        if (current === generation.current) setError(issue)
      })
      .finally(() => {
        if (current === generation.current) setChecking(false)
      })
    return () => {
      // eslint-disable-next-line react-hooks/exhaustive-deps -- Invalidates late network replies; this is not a DOM ref.
      generation.current++
    }
  }, [token, email])
  useEffect(() => {
    if (!resendAt) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [resendAt])
  async function send() {
    if (!token || !isStoreEmail(email) || busy || Date.now() < resendAt) return
    const current = generation.current
    setBusy(true)
    setError(null)
    try {
      const result = await storeGuestEmailApi.send(token, email)
      if (current !== generation.current) return
      if (
        result.sent !== true ||
        !result.challenge_id ||
        !Number.isSafeInteger(result.expires_at)
      ) {
        throw new Error('Store request failed')
      }
      setChallenge({ id: result.challenge_id, expires: result.expires_at })
      setCode('')
      setNow(Date.now())
      setResendAt(Date.now() + 60000)
    } catch (issue) {
      if (current === generation.current) setError(issue)
    } finally {
      if (current === generation.current) setBusy(false)
    }
  }
  async function confirm() {
    if (
      !token ||
      !challenge ||
      busy ||
      !/^[0-9]{6}$/.test(code) ||
      challenge.expires <= Date.now() / 1000
    ) {
      return
    }
    const current = generation.current
    setBusy(true)
    setError(null)
    try {
      const result = await storeGuestEmailApi.confirm(
        token,
        email,
        challenge.id,
        code
      )
      if (current === generation.current && result.verified === true) {
        setVerifiedEmail(email)
        setCode('')
        setChallenge(null)
      }
    } catch (issue) {
      if (current === generation.current) setError(issue)
    } finally {
      if (current === generation.current) setBusy(false)
    }
  }
  return {
    ready: !email || (!!token && verifiedEmail === email),
    verified: !!email && verifiedEmail === email,
    challenge,
    code,
    setCode,
    busy,
    checking,
    error,
    send,
    confirm,
    seconds: Math.max(0, Math.ceil((resendAt - now) / 1000)),
  }
}
