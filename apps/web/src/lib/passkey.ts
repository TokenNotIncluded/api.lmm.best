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
/**
 * Passkey helper utilities for WebAuthn credential handling.
 *
 * These helpers convert between ArrayBuffer and Base64URL encodings and
 * normalise server-provided credential options into browser-compatible types.
 */

type NodeBufferCtor = {
  from(input: string, encoding: string): { toString(encoding: string): string }
}

type EncodedCredentialDescriptor = Omit<
  PublicKeyCredentialDescriptor,
  'id'
> & {
  id: string
}

type CredentialCreationOptionsWithFormats = Omit<
  PublicKeyCredentialCreationOptions,
  'attestationFormats'
> & {
  attestationFormats?: string[]
}

type EncodedCredentialCreationOptions = Omit<
  CredentialCreationOptionsWithFormats,
  'challenge' | 'excludeCredentials' | 'user'
> & {
  challenge: string
  excludeCredentials?: EncodedCredentialDescriptor[]
  user: Omit<PublicKeyCredentialUserEntity, 'id'> & { id: string }
}

type EncodedCredentialRequestOptions = Omit<
  PublicKeyCredentialRequestOptions,
  'allowCredentials' | 'challenge'
> & {
  allowCredentials?: EncodedCredentialDescriptor[]
  challenge: string
}

function isUnknownRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isEncodedCredentialDescriptor(
  value: unknown
): value is EncodedCredentialDescriptor {
  return (
    isUnknownRecord(value) &&
    value.type === 'public-key' &&
    typeof value.id === 'string' &&
    (value.transports === undefined ||
      (Array.isArray(value.transports) &&
        value.transports.every((transport) => typeof transport === 'string')))
  )
}

function resolveCredentialOptions(payload: unknown): unknown {
  if (!isUnknownRecord(payload)) return undefined
  return (
    payload.publicKey ??
    payload.PublicKey ??
    payload.response ??
    payload.Response ??
    payload
  )
}

function isEncodedCredentialCreationOptions(
  value: unknown
): value is EncodedCredentialCreationOptions {
  if (!isUnknownRecord(value)) return false
  const { user, rp, pubKeyCredParams, excludeCredentials } = value
  return (
    typeof value.challenge === 'string' &&
    isUnknownRecord(user) &&
    typeof user.id === 'string' &&
    typeof user.name === 'string' &&
    typeof user.displayName === 'string' &&
    isUnknownRecord(rp) &&
    typeof rp.name === 'string' &&
    Array.isArray(pubKeyCredParams) &&
    pubKeyCredParams.every(
      (parameter) =>
        isUnknownRecord(parameter) &&
        parameter.type === 'public-key' &&
        typeof parameter.alg === 'number'
    ) &&
    (excludeCredentials === undefined ||
      (Array.isArray(excludeCredentials) &&
        excludeCredentials.every(isEncodedCredentialDescriptor)))
  )
}

function isEncodedCredentialRequestOptions(
  value: unknown
): value is EncodedCredentialRequestOptions {
  if (!isUnknownRecord(value)) return false
  return (
    typeof value.challenge === 'string' &&
    (value.allowCredentials === undefined ||
      (Array.isArray(value.allowCredentials) &&
        value.allowCredentials.every(isEncodedCredentialDescriptor)))
  )
}

/**
 * Convert a base64url string to an ArrayBuffer.
 */
export function base64UrlToArrayBuffer(value?: string | null): ArrayBuffer {
  if (!value) return new ArrayBuffer(0)

  const padding = '='.repeat((4 - (value.length % 4)) % 4)
  const base64 = (value + padding).replaceAll('-', '+').replaceAll('_', '/')

  const globalRef = globalThis as typeof globalThis & {
    Buffer?: NodeBufferCtor
  }

  const decode =
    typeof globalRef.atob === 'function'
      ? globalRef.atob.bind(globalRef)
      : (input: string) => {
          if (typeof globalRef.Buffer !== 'undefined') {
            return globalRef.Buffer.from(input, 'base64').toString('binary')
          }
          throw new Error(
            'Base64 decoding is not supported in this environment'
          )
        }

  const binary = decode(base64)
  const buffer = new ArrayBuffer(binary.length)
  const bytes = new Uint8Array(buffer)

  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i)
  }

  return buffer
}

/**
 * Convert an ArrayBuffer to a base64url string.
 */
export function arrayBufferToBase64Url(
  buffer?: ArrayBuffer | ArrayBufferLike | null
): string {
  if (!buffer) return ''

  const globalRef = globalThis as typeof globalThis & {
    Buffer?: NodeBufferCtor
  }

  const bytes = new Uint8Array(buffer)
  let binary = ''
  for (let i = 0; i < bytes.byteLength; i += 1) {
    binary += String.fromCharCode(bytes[i])
  }

  const encode =
    typeof globalRef.btoa === 'function'
      ? globalRef.btoa.bind(globalRef)
      : (input: string) => {
          if (typeof globalRef.Buffer !== 'undefined') {
            return globalRef.Buffer.from(input, 'binary').toString('base64')
          }
          throw new Error(
            'Base64 encoding is not supported in this environment'
          )
        }

  return encode(binary)
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replaceAll(/=+$/g, '')
}

/**
 * Prepare credential creation options returned by the backend.
 */
export function prepareCredentialCreationOptions(
  payload: unknown
): PublicKeyCredentialCreationOptions {
  const options = resolveCredentialOptions(payload)
  if (!isEncodedCredentialCreationOptions(options)) {
    throw new Error(
      'Unable to parse Passkey registration options from response'
    )
  }

  const {
    attestationFormats,
    challenge,
    excludeCredentials,
    user,
    ...rest
  } = options

  const publicKey: CredentialCreationOptionsWithFormats = {
    ...rest,
    challenge: base64UrlToArrayBuffer(challenge),
    user: {
      ...user,
      id: base64UrlToArrayBuffer(user.id),
    },
    ...(excludeCredentials
      ? {
          excludeCredentials: excludeCredentials.map((item) => ({
            ...item,
            id: base64UrlToArrayBuffer(item.id),
          })),
        }
      : {}),
    ...(attestationFormats?.length ? { attestationFormats } : {}),
  }
  return publicKey
}

/**
 * Prepare credential request options returned by the backend.
 */
export function prepareCredentialRequestOptions(
  payload: unknown
): PublicKeyCredentialRequestOptions {
  const options = resolveCredentialOptions(payload)
  if (!isEncodedCredentialRequestOptions(options)) {
    throw new Error('Unable to parse Passkey login options from response')
  }

  const { allowCredentials, challenge, ...rest } = options
  return {
    ...rest,
    challenge: base64UrlToArrayBuffer(challenge),
    ...(allowCredentials
      ? {
          allowCredentials: allowCredentials.map((item) => ({
            ...item,
            id: base64UrlToArrayBuffer(item.id),
          })),
        }
      : {}),
  }
}

/**
 * Build payload for registering a new credential.
 */
export function buildRegistrationResult(
  credential: PublicKeyCredential | null
): Record<string, unknown> | null {
  if (!credential) return null

  const response = credential.response as AuthenticatorAttestationResponse & {
    getTransports?: () => string[]
  }

  const transports =
    typeof response.getTransports === 'function'
      ? response.getTransports()
      : undefined

  return {
    id: credential.id,
    rawId: arrayBufferToBase64Url(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment,
    response: {
      attestationObject: arrayBufferToBase64Url(response.attestationObject),
      clientDataJSON: arrayBufferToBase64Url(response.clientDataJSON),
      transports,
    },
    clientExtensionResults: credential.getClientExtensionResults?.() ?? {},
  }
}

/**
 * Build payload for verifying an existing credential.
 */
export function buildAssertionResult(
  credential: PublicKeyCredential | null
): Record<string, unknown> | null {
  if (!credential) return null

  const response = credential.response as AuthenticatorAssertionResponse

  return {
    id: credential.id,
    rawId: arrayBufferToBase64Url(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment,
    response: {
      authenticatorData: arrayBufferToBase64Url(response.authenticatorData),
      clientDataJSON: arrayBufferToBase64Url(response.clientDataJSON),
      signature: arrayBufferToBase64Url(response.signature),
      userHandle: response.userHandle
        ? arrayBufferToBase64Url(response.userHandle)
        : null,
    },
    clientExtensionResults: credential.getClientExtensionResults?.() ?? {},
  }
}

/**
 * Check if current environment supports Passkey/WebAuthn.
 */
export async function isPasskeySupported(): Promise<boolean> {
  if (typeof window === 'undefined') return false
  const { PublicKeyCredential } = window
  if (!PublicKeyCredential) return false

  if (
    typeof PublicKeyCredential.isConditionalMediationAvailable === 'function'
  ) {
    try {
      const available =
        await PublicKeyCredential.isConditionalMediationAvailable()
      if (available) return true
    } catch {
      // ignore
    }
  }

  if (
    typeof PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable ===
    'function'
  ) {
    try {
      return await PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable()
    } catch {
      return false
    }
  }

  return true
}

/**
 * Execute an async Passkey credential creation flow.
 */
export async function createCredential(
  options: PublicKeyCredentialCreationOptions
) {
  return navigator.credentials.create({ publicKey: options })
}

/**
 * Execute an async Passkey credential request flow.
 */
export async function getCredential(
  options: PublicKeyCredentialRequestOptions
) {
  return navigator.credentials.get({ publicKey: options })
}
