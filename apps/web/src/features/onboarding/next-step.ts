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
import { getOnboardingState } from '@/lib/console-activation'
import type { AuthUser } from '@/stores/auth-store'

export type AccessRequestStatus =
  | 'unknown'
  | 'error'
  | 'none'
  | 'pending'
  | 'approved'
  | 'rejected'
export type ConnectionMethod = 'oauth' | 'api-key'

/** Guidance only: authorization remains the server's responsibility. */
export function getAccountNextStep(
  user: AuthUser | null | undefined,
  requestStatus: AccessRequestStatus = 'unknown',
  method?: ConnectionMethod
) {
  if (!user) return { to: '/sign-in', label: 'Sign in to get started' }
  const state = getOnboardingState(user)
  if (!state.activationComplete) {
    const label =
      requestStatus === 'pending'
        ? 'View access request status'
        : requestStatus === 'rejected'
          ? 'Revise access request'
          : requestStatus === 'none'
            ? 'Request API access'
            : 'Check API access status'
    return { to: '/getting-started', label }
  }
  if (state.firstRequestComplete) {
    return { to: '/dashboard', label: 'Open dashboard' }
  }
  // Without a selected client, never force an OAuth user to create a manual key.
  if (!method) return { to: '/guide', label: 'Choose your client' }
  if (
    method === 'api-key' &&
    user.onboarding?.details_available !== false &&
    user.onboarding?.api_key_created === false
  ) {
    return { to: '/keys', label: 'Create your first API key' }
  }
  return { to: '/guide', label: 'Continue client setup' }
}
