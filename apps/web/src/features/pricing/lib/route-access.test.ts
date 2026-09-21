/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { AuthUser } from '@/stores/auth-store'

import {
  resolvePricingRouteAccess,
  type PricingRouteAccessDependencies,
} from './route-access'

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

function runtime(options: {
  requireAuth: boolean
  initialUser?: AuthUser | null
  refreshedUser?: AuthUser | null
}) {
  let user = options.initialUser ?? null
  let bootstrapCalls = 0
  const dependencies: PricingRouteAccessDependencies = {
    loadModuleAccess: async () => ({
      enabled: true,
      requireAuth: options.requireAuth,
    }),
    bootstrapAuth: async () => {
      bootstrapCalls += 1
      user = options.refreshedUser ?? null
    },
    getUser: () => user,
  }
  return {
    dependencies,
    getBootstrapCalls: () => bootstrapCalls,
  }
}

describe('pricing route authentication', () => {
  test('waits for an in-flight session bootstrap before deciding that a required user is anonymous', async () => {
    const bootstrapStarted = deferred()
    const releaseBootstrap = deferred()
    let user: AuthUser | null = null
    const dependencies: PricingRouteAccessDependencies = {
      loadModuleAccess: async () => ({ enabled: true, requireAuth: true }),
      bootstrapAuth: async () => {
        bootstrapStarted.resolve()
        await releaseBootstrap.promise
        user = {
          id: 7,
          username: 'refreshed-user',
          role: 1,
          developer_access_granted: true,
        }
      },
      getUser: () => user,
    }

    const decisionPromise = resolvePricingRouteAccess(
      '/pricing?view=card',
      'marketplace',
      dependencies
    )
    await bootstrapStarted.promise

    let settledBeforeBootstrap = false
    void decisionPromise.then(
      () => {
        settledBeforeBootstrap = true
      },
      () => {
        settledBeforeBootstrap = true
      }
    )
    await Promise.resolve()
    assert.equal(settledBeforeBootstrap, false)

    releaseBootstrap.resolve()
    assert.deepEqual(await decisionPromise, { kind: 'allow' })
  })

  test('redirects to sign-in only after bootstrap confirms no user', async () => {
    const fixture = runtime({ requireAuth: true })
    const decision = await resolvePricingRouteAccess(
      '/pricing',
      'marketplace',
      fixture.dependencies
    )

    assert.deepEqual(decision, {
      kind: 'redirect-sign-in',
      redirect: '/pricing',
    })
    assert.equal(fixture.getBootstrapCalls(), 1)
  })

  test('keeps public pricing non-blocking while authentication restores in the background', async () => {
    const fixture = runtime({ requireAuth: false })

    const decision = await resolvePricingRouteAccess(
      '/pricing',
      'marketplace',
      fixture.dependencies
    )

    assert.deepEqual(decision, { kind: 'allow' })
    assert.equal(fixture.getBootstrapCalls(), 0)
  })

  test('restores an activated session before checking model details access', async () => {
    const fixture = runtime({
      requireAuth: false,
      refreshedUser: {
        id: 8,
        username: 'model-reader',
        role: 1,
        developer_access_granted: true,
      },
    })

    const decision = await resolvePricingRouteAccess(
      '/pricing/gpt-4o',
      'details',
      fixture.dependencies
    )

    assert.deepEqual(decision, { kind: 'allow' })
    assert.equal(fixture.getBootstrapCalls(), 1)
  })
})
