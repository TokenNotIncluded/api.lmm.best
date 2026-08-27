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
export type VerificationMethod = 'email' | '2fa' | 'passkey'

export type VerificationMethodsAvailability =
  | 'complete'
  | 'partial'
  | 'unavailable'

export type SecurityProofScope =
  | 'channel.key.read'
  | 'passkey.register'
  | 'passkey.delete'

export interface SecurityProof {
  proof_token: string
  expires_at: number
  method: VerificationMethod
  scope: SecurityProofScope
}

export interface VerificationMethods {
  hasEmail: boolean
  emailHint?: string
  has2FA: boolean
  hasPasskey: boolean
  passkeySupported: boolean
  availability: VerificationMethodsAvailability
}

/**
 * Sensitive dashboard actions use the strongest available independent proof
 * in a stable order: email, 2FA, then an existing Passkey.
 */
export function getPreferredVerificationMethods(
  methods: VerificationMethods
): VerificationMethods {
  if (methods.hasEmail) {
    return {
      ...methods,
      has2FA: false,
      hasPasskey: false,
    }
  }

  if (methods.has2FA) {
    return {
      ...methods,
      hasEmail: false,
      hasPasskey: false,
    }
  }

  if (methods.hasPasskey && methods.passkeySupported) {
    return {
      ...methods,
      hasEmail: false,
      has2FA: false,
    }
  }

  return {
    ...methods,
    hasEmail: false,
    has2FA: false,
    hasPasskey: false,
  }
}

export interface SecureVerificationState {
  method: VerificationMethod | null
  scope?: SecurityProofScope
  loading: boolean
  code: string
  title?: string
  description?: string
}

export interface UseSecureVerificationOptions {
  onSuccess?: (result: unknown, method: VerificationMethod) => void
  onError?: (error: unknown) => void
  successMessage?: string
  autoReset?: boolean
}

export interface StartVerificationOptions {
  scope: SecurityProofScope
  preferredMethod?: VerificationMethod
  title?: string
  description?: string
  verificationMethods?: VerificationMethods
}
