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

type VerificationProbeResult<T> = { ok: true; value: T } | { ok: false }

export interface VerificationProbeDependencies {
  getSelf: () => ReturnType<typeof getSelf>
  get2FAStatus: () => ReturnType<typeof get2FAStatus>
  getPasskeyStatus: () => ReturnType<typeof getPasskeyStatus>
  detectPasskeySupport: () => Promise<boolean>
  wait: (delay: number) => Promise<void>
  warn: (message: string, detail: string) => void
}

const defaultVerificationProbeDependencies: VerificationProbeDependencies = {
  getSelf: () => getSelf(),
  get2FAStatus: () => get2FAStatus({ skipErrorHandler: true }),
  getPasskeyStatus: () => getPasskeyStatus({ skipErrorHandler: true }),
  detectPasskeySupport,
  wait: (delay) =>
    new Promise((resolve) => globalThis.setTimeout(resolve, delay)),
  // Expected probe failures are surfaced by the action that needs them.
  // Keep optional capability checks from adding production console noise.
  warn: () => undefined,
}

/**
 * Fetch available verification methods for the current user.
 *
 * Each capability is independent. A transient failure from one endpoint must
 * not hide methods reported successfully by the other endpoints.
 */
export async function checkVerificationMethods(
  dependencies: VerificationProbeDependencies = defaultVerificationProbeDependencies
): Promise<VerificationMethods> {
  const [selfProbe, twoFAProbe, passkeyProbe, passkeySupportProbe] =
    await Promise.all([
      probeVerificationMethod('account', dependencies.getSelf, dependencies),
      probeVerificationMethod('2FA', dependencies.get2FAStatus, dependencies),
      probeVerificationMethod(
        'Passkey',
        dependencies.getPasskeyStatus,
        dependencies
      ),
      probeVerificationMethod(
        'Passkey support',
        dependencies.detectPasskeySupport,
        dependencies
      ),
    ])

  const selfResponse = selfProbe.ok ? selfProbe.value : undefined
  const twoFAResponse = twoFAProbe.ok ? twoFAProbe.value : undefined
  const passkeyResponse = passkeyProbe.ok ? passkeyProbe.value : undefined
  const passkeySupported = passkeySupportProbe.ok
    ? passkeySupportProbe.value
    : false
  const completedServerProbes = [selfProbe, twoFAProbe, passkeyProbe].filter(
    (probe) => probe.ok
  ).length
  let availability: VerificationMethods['availability'] = 'partial'
  if (completedServerProbes === 3) availability = 'complete'
  if (completedServerProbes === 0) availability = 'unavailable'

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
    passkeySupported,
    availability,
  }
}

async function probeVerificationMethod<T>(
  method: string,
  request: () => Promise<T>,
  dependencies: Pick<VerificationProbeDependencies, 'wait' | 'warn'>
): Promise<VerificationProbeResult<T>> {
  try {
    return { ok: true, value: await request() }
  } catch (firstError) {
    if (!isRetryableVerificationError(firstError)) {
      dependencies.warn(
        `[Secure Verification] Failed to check ${method}`,
        String(firstError)
      )
      return { ok: false }
    }

    await dependencies.wait(VERIFICATION_PROBE_RETRY_DELAY_MS)

    try {
      return { ok: true, value: await request() }
    } catch (retryError) {
      dependencies.warn(
        `[Secure Verification] Failed to check ${method} after retry`,
        String(retryError)
      )
      return { ok: false }
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
