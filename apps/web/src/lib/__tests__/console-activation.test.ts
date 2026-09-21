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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { AuthUser } from '@/stores/auth-store'

import {
  getAuthenticatedLandingRoute,
  isConsoleActivated,
  isContributorRoute,
  isRestrictedPublicRoute,
} from '../console-activation'

function user(overrides: Partial<AuthUser> = {}): AuthUser {
  return {
    id: 7,
    username: 'contributor',
    role: 1,
    ...overrides,
  }
}

describe('console activation boundary', () => {
  test('keeps new accounts restricted until the server grants access', () => {
    assert.equal(
      isConsoleActivated(
        user({
          developer_access_granted: false,
          onboarding: {
            activation_complete: false,
            credential_complete: false,
            first_request_complete: false,
            stage: 'activate',
          },
        })
      ),
      false
    )
  })

  test('uses the nested server state before temporary flat compatibility fields', () => {
    const account = user({
      developer_access_granted: false,
      onboarding: {
        activation_complete: false,
        credential_complete: false,
        first_request_complete: false,
        stage: 'activate',
      },
      activation_complete: true,
      onboarding_stage: 'complete',
      trust_level_info: {
        level: 2,
        automatic_level: 2,
        override_level: null,
        paid_amount: 10,
        discount_ratio: 0.9,
        discount_percent: 10,
        inactivity_decay_steps: 0,
        decay_period_days: 90,
        overridden: false,
      },
    })

    assert.equal(isConsoleActivated(account), false)
    assert.equal(getAuthenticatedLandingRoute(account), '/getting-started')
  })

  test('keeps activation distinct from onboarding completion', () => {
    const account = user({
      developer_access_granted: true,
      onboarding: {
        activation_complete: true,
        credential_complete: false,
        first_request_complete: false,
        stage: 'credential',
      },
    })

    assert.equal(isConsoleActivated(account), true)
    assert.equal(getAuthenticatedLandingRoute(account), '/dashboard')
  })

  test('uses a valid visible user-selected landing page after activation', () => {
    const account = user({
      developer_access_granted: true,
      sidebar_modules: JSON.stringify({
        modules: {},
        preferences: {
          default_route: '/profile',
          hidden_sections: [],
        },
      }),
    })

    assert.equal(getAuthenticatedLandingRoute(account), '/profile')
  })

  test('falls back when the selected page is hidden or outside the role', () => {
    const hidden = user({
      developer_access_granted: true,
      sidebar_modules: JSON.stringify({
        modules: {},
        preferences: {
          default_route: '/profile',
          hidden: ['/profile'],
        },
      }),
    })
    const nonAdmin = user({
      developer_access_granted: true,
      sidebar_modules: JSON.stringify({
        modules: {},
        preferences: { default_route: '/users' },
      }),
    })

    assert.equal(getAuthenticatedLandingRoute(hidden), '/dashboard')
    assert.equal(getAuthenticatedLandingRoute(nonAdmin), '/dashboard')
  })

  test('falls back when either effective sidebar module layer hides the selected page', () => {
    const account = user({
      developer_access_granted: true,
      sidebar_modules: JSON.stringify({
        modules: { console: { detail: false } },
        preferences: { default_route: '/dashboard/overview' },
      }),
    })
    const adminHidden = user({
      developer_access_granted: true,
      sidebar_modules: JSON.stringify({
        modules: {},
        preferences: { default_route: '/dashboard/overview' },
      }),
    })

    assert.equal(getAuthenticatedLandingRoute(account, {}), '/dashboard')
    assert.equal(
      getAuthenticatedLandingRoute(adminHidden, {
        console: { enabled: true, detail: false },
      }),
      '/dashboard'
    )
  })

  test('lands only explicitly granted complete accounts in the dashboard', () => {
    assert.equal(
      getAuthenticatedLandingRoute(
        user({
          developer_access_granted: true,
          onboarding: {
            activation_complete: true,
            credential_complete: true,
            first_request_complete: true,
            stage: 'complete',
          },
        })
      ),
      '/dashboard'
    )
    assert.equal(
      getAuthenticatedLandingRoute(user({ role: 10 })),
      '/getting-started'
    )
    assert.equal(
      getAuthenticatedLandingRoute(
        user({
          developer_access_granted: true,
          trust_level_info: {
            level: 0,
            paid_amount: 1,
          } as AuthUser['trust_level_info'],
        })
      ),
      '/dashboard'
    )
  })

  test('does not infer access from an administrator level override or legacy fields', () => {
    const overridden = user({
      trust_level_info: {
        level: 2,
        paid_amount: 0,
        overridden: true,
      } as AuthUser['trust_level_info'],
    })

    assert.equal(isConsoleActivated(overridden), false)
    assert.equal(getAuthenticatedLandingRoute(overridden), '/getting-started')
    assert.equal(
      isConsoleActivated(
        user({ permissions: { console_activated_at: 1720000000 } })
      ),
      false
    )
  })

  test('derives nested stages from monotonic booleans and fails closed on malformed state', () => {
    const contradictory = user({
      onboarding: {
        activation_complete: false,
        credential_complete: true,
        first_request_complete: true,
        stage: 'complete',
      },
    })
    const malformed = user({
      onboarding: 'complete' as unknown as AuthUser['onboarding'],
    })

    assert.equal(isConsoleActivated(contradictory), false)
    assert.equal(
      getAuthenticatedLandingRoute(contradictory),
      '/getting-started'
    )
    assert.equal(isConsoleActivated(malformed), false)
    assert.equal(getAuthenticatedLandingRoute(malformed), '/getting-started')
  })

  test('fails closed for unknown activation state', () => {
    assert.equal(isConsoleActivated(user()), false)
    assert.equal(getAuthenticatedLandingRoute(user()), '/getting-started')
    assert.equal(
      isConsoleActivated(
        user({
          trust_level_info: {
            paid_amount: Number.POSITIVE_INFINITY,
          } as AuthUser['trust_level_info'],
        })
      ),
      false
    )
    assert.equal(
      isConsoleActivated(user({ permissions: { console_activated_at: 0 } })),
      false
    )
  })

  test('allows onboarding, tools and checkout before activation', () => {
    assert.equal(isContributorRoute('/getting-started'), true)
    assert.equal(isContributorRoute('/getting-started/request'), true)
    assert.equal(isContributorRoute('/wallet'), true)
    assert.equal(isContributorRoute('/wallet/'), true)
    assert.equal(isContributorRoute('/wallet/admin'), false)
    assert.equal(isContributorRoute('/tool-market'), true)
    assert.equal(isContributorRoute('/open-source-bounties'), false)
    assert.equal(isContributorRoute('/profile/security'), false)
    assert.equal(isContributorRoute('/support'), false)
    assert.equal(isContributorRoute('/workspace'), false)
    assert.equal(isContributorRoute('/challenges/42'), false)
    assert.equal(isContributorRoute('/models'), false)
  })

  test('hides legacy public discovery surfaces before activation', () => {
    assert.equal(isRestrictedPublicRoute('/pricing'), false)
    assert.equal(isRestrictedPublicRoute('/pricing/model-1'), false)
    assert.equal(isRestrictedPublicRoute('/rankings'), true)
    assert.equal(isRestrictedPublicRoute('/about'), true)
    assert.equal(isRestrictedPublicRoute('/challenges/42'), false)
    assert.equal(isRestrictedPublicRoute('/privacy-policy'), false)
    assert.equal(isRestrictedPublicRoute('/sign-in'), false)
  })
})
