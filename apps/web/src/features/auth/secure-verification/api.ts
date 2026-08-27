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

import type { ApiResponse } from '@/features/auth/types'
import { api, get2FAStatus, getSelf } from '@/lib/api'
import {
  buildAssertionResult,
  prepareCredentialRequestOptions,
  isPasskeySupported as detectPasskeySupport,
} from '@/lib/passkey'

import {
  beginPasskeyVerification,
  finishPasskeyVerification,
  getPasskeyStatus,
} from '../passkey'
import type {
  SecurityProof,
  SecurityProofScope,
  VerificationMethod,
  VerificationMethods,
} from './types'

const VERIFICATION_PROBE_RETRY_DELAY_MS = 250

/**
 * Fetch available verification methods for the current user.
 *
 * Each capability is independent. A transient failure from one endpoint must
 * not hide methods reported successfully by the other endpoints.
 */
export async function checkVerificationMethods(): Promise<VerificationMethods> {
  const [selfResponse, twoFAResponse, passkeyResponse, passkeySupported] =
    await Promise.all([
      probeVerificationMethod('account', () => getSelf()),
      probeVerificationMethod('2FA', () =>
        get2FAStatus({ skipErrorHandler: true })
      ),
      probeVerificationMethod('Passkey', () =>
        getPasskeyStatus({ skipErrorHandler: true })
      ),
      probeVerificationMethod('Passkey support', detectPasskeySupport),
    ])

  const email = String(selfResponse?.data?.email ?? '').trim()
  const has2FA =
    Boolean(twoFAResponse?.success) && Boolean(twoFAResponse?.data?.enabled)
  const hasPasskey =
    Boolean(passkeyResponse?.success) && Boolean(passkeyResponse?.data?.enabled)

  return {
    hasEmail: email.length > 0,
    emailHint: email ? maskEmail(email) : undefined,
    has2FA,
    hasPasskey,
    passkeySupported: Boolean(passkeySupported),
  }
}

async function probeVerificationMethod<T>(
  method: string,
  request: () => Promise<T>
): Promise<T | undefined> {
  try {
    return await request()
  } catch (firstError) {
    if (!isRetryableVerificationError(firstError)) {
      // eslint-disable-next-line no-console
      console.warn(
        `[Secure Verification] Failed to check ${method}`,
        firstError
      )
      return undefined
    }

    await new Promise((resolve) =>
      setTimeout(resolve, VERIFICATION_PROBE_RETRY_DELAY_MS)
    )

    try {
      return await request()
    } catch (retryError) {
      // eslint-disable-next-line no-console
      console.warn(
        `[Secure Verification] Failed to check ${method} after retry`,
        retryError
      )
      return undefined
    }
  }
}

function isRetryableVerificationError(error: unknown): boolean {
  const status = (error as { response?: { status?: unknown } })?.response
    ?.status
  if (typeof status !== 'number') return true
  return status === 408 || status === 425 || status === 429 || status >= 500
}

function maskEmail(email: string): string {
  const [local, domain] = email.split('@', 2)
  if (!local || !domain) return ''
  if (local.length <= 2) return `${local.slice(0, 1)}***@${domain}`
  return `${local.slice(0, 1)}***${local.slice(-1)}@${domain}`
}

/** Request a one-time code for the authenticated user's bound email. */
export async function sendSecurityEmailVerification(): Promise<{
  email_hint?: string
}> {
  const res = await api.post<{
    success: boolean
    message?: string
    data?: { email_hint?: string }
  }>('/api/verify/email')
  if (!res.data?.success) {
    throw new Error(
      res.data?.message || i18next.t('Failed to send verification email')
    )
  }
  return res.data.data ?? {}
}

/**
 * Execute a verification flow based on the method type.
 */
export async function verify(
  method: VerificationMethod,
  scope: SecurityProofScope,
  code?: string
): Promise<SecurityProof> {
  switch (method) {
    case 'email':
      return verifyEmail(scope, code)
    case '2fa':
      return verifyTwoFA(scope, code)
    case 'passkey':
      return verifyPasskey(scope)
    default:
      throw new Error(
        i18next.t('Unsupported verification method: {{method}}', { method })
      )
  }
}

async function verifyEmail(
  scope: SecurityProofScope,
  code?: string | null
): Promise<SecurityProof> {
  const trimmed = code?.trim()
  if (!trimmed) {
    throw new Error(i18next.t('Please enter the verification code'))
  }
  const res = await api.post<ApiResponse<SecurityProof>>('/api/verify', {
    method: 'email',
    code: trimmed,
    scope,
  })
  if (!res.data?.success) {
    throw new Error(res.data?.message || i18next.t('Verification failed'))
  }
  if (!res.data.data?.proof_token) {
    throw new Error(i18next.t('Verification proof was not returned'))
  }
  return res.data.data
}

/**
 * Perform 2FA verification flow.
 */
async function verifyTwoFA(
  scope: SecurityProofScope,
  code?: string | null
): Promise<SecurityProof> {
  const trimmed = code?.trim()
  if (!trimmed) {
    throw new Error(
      i18next.t('Please enter the verification code or backup code')
    )
  }

  const res = await api.post<ApiResponse<SecurityProof>>('/api/verify', {
    method: '2fa',
    code: trimmed,
    scope,
  })

  if (!res.data?.success) {
    throw new Error(res.data?.message || i18next.t('Verification failed'))
  }
  if (!res.data.data?.proof_token) {
    throw new Error(i18next.t('Verification proof was not returned'))
  }
  return res.data.data
}

/**
 * Perform Passkey verification flow.
 */
async function verifyPasskey(
  scope: SecurityProofScope
): Promise<SecurityProof> {
  if (typeof navigator === 'undefined' || !navigator.credentials) {
    throw new Error(
      i18next.t('Passkey verification is not supported in this environment')
    )
  }

  try {
    const beginResponse = await beginPasskeyVerification(scope)
    if (!beginResponse.success) {
      throw new Error(
        beginResponse.message || i18next.t('Failed to start verification')
      )
    }

    const publicKey = prepareCredentialRequestOptions(
      beginResponse.data?.options ?? beginResponse.data
    )
    const flowToken = beginResponse.data?.flow_token
    if (!flowToken) {
      throw new Error(i18next.t('Verification flow expired'))
    }

    const credential = (await navigator.credentials.get({
      publicKey,
    })) as PublicKeyCredential | null

    if (!credential) {
      throw new Error(i18next.t('Passkey verification was cancelled'))
    }

    const assertion = buildAssertionResult(credential)
    if (!assertion) {
      throw new Error(i18next.t('Unable to build Passkey assertion'))
    }

    const finishResponse = await finishPasskeyVerification(flowToken, assertion)
    if (!finishResponse.success) {
      throw new Error(
        finishResponse.message || i18next.t('Passkey verification failed')
      )
    }

    if (!finishResponse.data?.proof_token) {
      throw new Error(i18next.t('Verification proof was not returned'))
    }
    return finishResponse.data
  } catch (error: unknown) {
    if (error instanceof DOMException && error.name === 'NotAllowedError') {
      throw new Error(
        i18next.t('Passkey verification was cancelled or timed out'),
        { cause: error }
      )
    }
    if (error instanceof DOMException && error.name === 'InvalidStateError') {
      throw new Error(
        i18next.t('Passkey verification is not available in the current state'),
        { cause: error }
      )
    }
    throw error
  }
}
