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
import i18next from 'i18next'
import { useCallback, useMemo, useState } from 'react'
import { toast } from 'sonner'

import { bootstrapAuthentication } from '@/lib/auth-session'
import {
  extractVerificationInfo,
  isVerificationRequiredError,
} from '@/lib/secure-verification'

import {
  checkVerificationMethods,
  sendSecurityEmailVerification,
  verify,
} from '../api'
import {
  getPreferredVerificationMethods,
  type SecureVerificationState,
  type StartVerificationOptions,
  type UseSecureVerificationOptions,
  type VerificationMethod,
  type VerificationMethods,
} from '../types'

type ApiCall = ((proofToken?: string) => Promise<unknown>) | null

interface InternalState extends SecureVerificationState {
  apiCall: ApiCall
}

const defaultMethods: VerificationMethods = {
  hasEmail: false,
  has2FA: false,
  hasPasskey: false,
  passkeySupported: false,
  availability: 'unavailable',
}

const initialState: InternalState = {
  method: null,
  loading: false,
  code: '',
  title: undefined,
  description: undefined,
  apiCall: null,
}

export function useSecureVerification(
  options: UseSecureVerificationOptions = {}
) {
  const { onSuccess, onError, successMessage, autoReset = true } = options

  const [methods, setMethods] = useState<VerificationMethods>(defaultMethods)
  const [state, setState] = useState<InternalState>(initialState)
  const [open, setOpen] = useState(false)
  const [emailCodeSending, setEmailCodeSending] = useState(false)
  const [emailCodeSent, setEmailCodeSent] = useState(false)

  const fetchVerificationMethods = useCallback(async () => {
    const result = await checkVerificationMethods()
    setMethods(result)
    return result
  }, [])

  const reset = useCallback(() => {
    setState(initialState)
    setOpen(false)
    setEmailCodeSent(false)
  }, [])

  const startVerification = useCallback(
    async (
      apiCall: (proofToken?: string) => Promise<unknown>,
      config: StartVerificationOptions
    ) => {
      const {
        scope,
        preferredMethod,
        title,
        description,
        verificationMethods,
      } = config
      const authOutcome = await bootstrapAuthentication()
      if (authOutcome.kind !== 'authenticated') {
        let error = new Error(i18next.t('Session expired!'))
        if (authOutcome.kind === 'transient_error') {
          error = new Error(i18next.t('Request failed'), {
            cause: authOutcome.error,
          })
        }
        toast.error(error.message)
        onError?.(error)
        return false
      }

      const checkedMethods =
        verificationMethods ?? (await fetchVerificationMethods())
      if (verificationMethods) setMethods(verificationMethods)
      const availableMethods = getPreferredVerificationMethods(checkedMethods)
      const hasAvailableMethod =
        availableMethods.hasEmail ||
        availableMethods.has2FA ||
        availableMethods.hasPasskey

      if (!hasAvailableMethod && checkedMethods.availability !== 'complete') {
        const error = new Error(i18next.t('Request failed'))
        toast.error(error.message)
        onError?.(error)
        return false
      }

      if (!hasAvailableMethod) {
        toast.error(
          i18next.t(
            'Please bind an email, enable 2FA, or set up a Passkey before proceeding'
          )
        )
        onError?.(
          new Error(
            'No verification methods available. Bind an email, enable 2FA, or set up a Passkey to continue.'
          )
        )
        return false
      }

      const preferredMethodAvailable =
        (preferredMethod === 'email' && availableMethods.hasEmail) ||
        (preferredMethod === '2fa' && availableMethods.has2FA) ||
        (preferredMethod === 'passkey' && availableMethods.hasPasskey)
      let defaultMethod: VerificationMethod | null = null
      if (preferredMethodAvailable && preferredMethod) {
        defaultMethod = preferredMethod
      } else if (availableMethods.hasEmail) {
        defaultMethod = 'email'
      } else if (availableMethods.has2FA) {
        defaultMethod = '2fa'
      } else if (availableMethods.hasPasskey) {
        defaultMethod = 'passkey'
      }

      setState((prev) => ({
        ...prev,
        apiCall,
        method: defaultMethod,
        scope,
        title,
        description,
      }))
      setEmailCodeSent(false)
      setOpen(true)
      return true
    },
    [fetchVerificationMethods, onError]
  )

  const sendEmailCode = useCallback(async () => {
    setEmailCodeSending(true)
    try {
      const result = await sendSecurityEmailVerification()
      setEmailCodeSent(true)
      toast.success(
        result.email_hint
          ? i18next.t('Verification code sent to {{email}}', {
              email: result.email_hint,
            })
          : i18next.t('Verification code sent')
      )
      return result
    } catch (error) {
      const message =
        error instanceof Error
          ? error.message
          : i18next.t('Failed to send verification email')
      toast.error(message)
      throw error
    } finally {
      setEmailCodeSending(false)
    }
  }, [])

  const executeVerification = useCallback(
    async (method?: VerificationMethod, code?: string) => {
      if (!state.apiCall) {
        toast.error(i18next.t('Verification is not configured properly'))
        return
      }

      const actualMethod = method ?? state.method
      if (!actualMethod) {
        toast.error(i18next.t('Select a verification method first'))
        return
      }

      setState((prev) => ({ ...prev, loading: true }))

      try {
        if (!state.scope) {
          throw new Error(i18next.t('Verification scope is missing'))
        }
        const proof = await verify(
          actualMethod,
          state.scope,
          code ?? state.code
        )
        const result = await state.apiCall(proof.proof_token)

        if (successMessage) {
          toast.success(successMessage)
        }

        onSuccess?.(result, actualMethod)

        if (autoReset) {
          reset()
        }

        return result
      } catch (error) {
        const message =
          error instanceof Error
            ? error.message
            : i18next.t('Verification failed')
        toast.error(message)
        onError?.(error)
        throw error
      } finally {
        setState((prev) => ({ ...prev, loading: false }))
      }
    },
    [state, successMessage, onSuccess, onError, autoReset, reset]
  )

  const setCode = useCallback((code: string) => {
    setState((prev) => ({ ...prev, code }))
  }, [])

  const switchMethod = useCallback((method: VerificationMethod) => {
    setState((prev) => ({ ...prev, method, code: '' }))
  }, [])

  const cancel = useCallback(() => {
    reset()
  }, [reset])

  const withVerification = useCallback(
    async (
      apiCall: (proofToken?: string) => Promise<unknown>,
      config: StartVerificationOptions
    ) => {
      try {
        return await apiCall()
      } catch (error) {
        if (isVerificationRequiredError(error)) {
          const info = extractVerificationInfo(error)
          toast.info(info.message)
          await startVerification(apiCall, config)
          return null
        }
        throw error
      }
    },
    [startVerification]
  )

  const canUseMethod = useCallback(
    (method: VerificationMethod) => {
      const preferredMethods = getPreferredVerificationMethods(methods)
      if (method === 'email') return preferredMethods.hasEmail
      if (method === '2fa') return preferredMethods.has2FA
      if (method === 'passkey') return preferredMethods.hasPasskey
      return false
    },
    [methods]
  )

  const recommendedMethod = useMemo<VerificationMethod | null>(() => {
    const preferredMethods = getPreferredVerificationMethods(methods)
    if (preferredMethods.hasEmail) return 'email'
    if (preferredMethods.has2FA) return '2fa'
    if (preferredMethods.hasPasskey) return 'passkey'
    return null
  }, [methods])

  const preferredMethods = useMemo(
    () => getPreferredVerificationMethods(methods),
    [methods]
  )

  return {
    open,
    setOpen,
    methods,
    state,
    startVerification,
    executeVerification,
    cancel,
    sendEmailCode,
    emailCodeSending,
    emailCodeSent,
    reset,
    setCode,
    switchMethod,
    withVerification,
    fetchVerificationMethods,
    canUseMethod,
    recommendedMethod,
    hasAnyMethod:
      preferredMethods.hasEmail ||
      preferredMethods.has2FA ||
      preferredMethods.hasPasskey,
    preferredMethods,
    isLoading: state.loading,
    currentMethod: state.method,
    code: state.code,
  }
}
