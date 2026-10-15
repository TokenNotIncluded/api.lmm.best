/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  redirect,
} from '@tanstack/react-router'
import { CanceledError } from 'axios'

import { AuthBootstrapPendingError } from '@/lib/auth-bootstrap-navigation'

import {
  classifyError,
  copyErrorDiagnostics,
  createErrorDiagnostics,
  diagnosticRouteTemplate,
  isNonFailureError,
} from './error-diagnostics'

const template = '/store/claim/$token'
const routes = { [template]: {} }
const matches = [{ routeId: template }]

function poison(): object {
  const value = {}
  for (const key of [
    'message',
    'stack',
    'cause',
    'config',
    'headers',
    'request',
    'body',
    'token',
    'email',
    'toJSON',
  ]) {
    Object.defineProperty(value, key, {
      get() {
        throw new Error(`forbidden read: ${key}`)
      },
    })
  }
  return value
}

test('copies exactly six allowed fields and never evaluates sensitive error getters', () => {
  const error = Object.assign(poison(), {
    response: Object.assign(poison(), {
      status: 200,
      data: Object.assign(poison(), { success: false }),
    }),
  })
  const result = createErrorDiagnostics(error, matches, routes)
  assert.ok(result)
  assert.deepEqual(
    Object.keys(result).sort(),
    [
      'occurred_at_utc',
      'build_id',
      'route_template',
      'local_event_id',
      'http_status',
      'kind',
    ].sort()
  )
  assert.match(result.occurred_at_utc, /^\d{4}-\d{2}-\d{2}T.*Z$/)
  assert.match(result.local_event_id, /^[a-f0-9]{32}$/)
  assert.equal(result.route_template, template)
  assert.equal(result.http_status, 200)
  assert.equal(result.kind, 'business_error')
  assert.equal(Object.isFrozen(result), true)
  assert.doesNotMatch(
    JSON.stringify(result),
    /forbidden|token.*:.*secret|message|stack|headers|email/
  )
})

test('HTTP status is an integer 100 through 599; absence and malformed values stay null', () => {
  for (const status of [
    undefined,
    null,
    '500',
    Number.NaN,
    Infinity,
    0,
    99,
    600,
    -1,
    500.5,
  ]) {
    assert.deepEqual(classifyError({ response: { status } }), {
      http_status: null,
      kind: 'unexpected_error',
    })
  }
  for (const status of [100, 200, 302, 400, 429, 500, 599]) {
    assert.equal(classifyError({ response: { status } }).http_status, status)
  }
  assert.deepEqual(classifyError(new Error('secret')), {
    http_status: null,
    kind: 'unexpected_error',
  })
  assert.deepEqual(classifyError({ code: 'ERR_NETWORK', message: 'secret' }), {
    http_status: null,
    kind: 'transport_error',
  })
  assert.equal(
    classifyError({ response: { status: 429, data: { success: false } } }).kind,
    'http_error'
  )
  assert.equal(
    classifyError({ response: { status: 200, data: { success: true } } }).kind,
    'http_response'
  )
})

test('status and success accessors are ignored without evaluating them', () => {
  let reads = 0
  const response = Object.defineProperty({}, 'status', {
    get() {
      reads++
      return 500
    },
  })
  assert.equal(classifyError({ response }).http_status, null)
  const data = Object.defineProperty({}, 'success', {
    get() {
      reads++
      return false
    },
  })
  assert.equal(
    classifyError({ response: { status: 200, data } }).kind,
    'http_response'
  )
  assert.equal(reads, 0)
})

test('route template comes only from the matched registered route ID, never its resolved ID or params', () => {
  assert.equal(
    diagnosticRouteTemplate(
      [
        {
          routeId: template,
          id: '/store/claim/secret',
          params: { token: 'secret' },
        } as never,
      ],
      routes
    ),
    template
  )
  for (const routeId of [
    '/store/claim/secret',
    'https://secret.test/?token=secret',
    '/secret?token=x',
    undefined,
  ]) {
    assert.equal(diagnosticRouteTemplate([{ routeId }], routes), 'unknown')
  }
  assert.equal(diagnosticRouteTemplate([], routes), 'unknown')
  assert.equal(
    diagnosticRouteTemplate([{ routeId: '__proto__' }], {}),
    'unknown'
  )
})

test('cancellation, pending authentication and redirects never receive fault diagnostics', () => {
  for (const error of [
    new CanceledError('secret'),
    new DOMException('secret', 'AbortError'),
    redirect({ to: '/sign-in', search: { token: 'secret' } } as never),
    new AuthBootstrapPendingError(Promise.resolve({} as never), '/secret'),
  ]) {
    assert.equal(isNonFailureError(error), true)
    assert.equal(createErrorDiagnostics(error, matches, routes), null)
  }
})

test('cryptographic entropy is required; failure never falls back to a weak ID', () => {
  const entropy = {
    getRandomValues() {
      throw new Error('synthetic entropy unavailable')
    },
  }
  assert.equal(createErrorDiagnostics({}, matches, routes, entropy), null)
  assert.equal(createErrorDiagnostics({}, matches, routes, {} as Crypto), null)
  const first = createErrorDiagnostics({}, matches, routes)
  const second = createErrorDiagnostics({}, matches, routes)
  assert.ok(first && second)
  assert.notEqual(first.local_event_id, second.local_event_id)
})

test('clipboard requires explicit invocation, waits for completion, and contains failures without logging', async () => {
  const result = createErrorDiagnostics({}, matches, routes)
  assert.ok(result)
  const written: string[] = []
  assert.equal(
    await copyErrorDiagnostics(result, {
      async writeText(text) {
        written.push(text)
      },
    }),
    true
  )
  assert.deepEqual(JSON.parse(written[0]), result)
  assert.equal(await copyErrorDiagnostics(result, undefined), false)
  assert.equal(
    await copyErrorDiagnostics(result, {
      async writeText() {
        throw new Error('secret-copy-error')
      },
    }),
    false
  )
  assert.equal(
    await copyErrorDiagnostics(result, {
      async writeText() {
        return false
      },
    } as unknown as Clipboard),
    false
  )
})

test('registered TanStack group routes retain their static template without admitting resolved URLs', async () => {
  const root = createRootRoute()
  const group = createRoute({ getParentRoute: () => root, id: '(errors)' })
  const page = createRoute({ getParentRoute: () => group, path: '/500' })
  const router = createRouter({
    routeTree: root.addChildren([group.addChildren([page])]),
    history: createMemoryHistory({
      initialEntries: ['/500?token=QUERY_SECRET#HASH_SECRET'],
    }),
    isServer: true,
  })
  await router.load()
  assert.equal(page.id, '/(errors)/500')
  assert.equal(page.fullPath, '/500')
  assert.equal(
    diagnosticRouteTemplate(router.state.matches, router.routesById),
    '/(errors)/500'
  )
  const record = createErrorDiagnostics(
    {},
    router.state.matches,
    router.routesById
  )
  assert.ok(record)
  assert.equal(record.route_template, '/(errors)/500')
  assert.doesNotMatch(JSON.stringify(record), /QUERY_SECRET|HASH_SECRET/)
  assert.equal(
    diagnosticRouteTemplate(
      [{ routeId: '/(errors)/unregistered' }],
      router.routesById
    ),
    'unknown'
  )
  const accessor = {
    get routeId() {
      throw new Error('route accessor must not be read')
    },
  }
  assert.equal(
    diagnosticRouteTemplate([accessor], router.routesById),
    'unknown'
  )
  for (const invalid of [
    '/500?token=QUERY_SECRET',
    '/500#HASH_SECRET',
    `/(errors)/${'x'.repeat(257)}`,
  ]) {
    const ownEntries = { ...router.routesById, [invalid]: page }
    assert.equal(
      diagnosticRouteTemplate([{ routeId: invalid }], ownEntries),
      'unknown'
    )
  }
})
