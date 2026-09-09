/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

import assert from 'node:assert/strict'
import test from 'node:test'

import { WebhookEventType } from '@waffo/pancake-ts'

import {
  WAFFO_PANCAKE_ACTIVE_PRODUCTS_QUERY,
  WAFFO_PANCAKE_STORES_QUERY,
  findMatchingTestWebhook,
  WAFFO_PANCAKE_WEBHOOK_EVENTS,
} from './waffo-pancake-smoke.mjs'

test('managed Test webhook subscribes to settlement, subscription, and refund events', () => {
  assert.deepEqual([...WAFFO_PANCAKE_WEBHOOK_EVENTS], [
    WebhookEventType.OrderCompleted,
    WebhookEventType.SubscriptionActivated,
    'subscription.renewed',
    WebhookEventType.SubscriptionPaymentSucceeded,
    WebhookEventType.RefundSucceeded,
    WebhookEventType.RefundFailed,
  ])
})

test('smoke runner reuses the matching Test HTTP webhook instead of duplicating it', () => {
  const matching = { id: 'existing', channel: 'http', url: 'https://example.test/waffo', testMode: true }
  const webhooks = [
    matching,
    { id: 'prod', channel: 'http', url: matching.url, testMode: false },
    { id: 'other-channel', channel: 'discord', url: matching.url, testMode: true },
    { id: 'other-url', channel: 'http', url: 'https://other.test/waffo', testMode: true },
  ]
  assert.equal(findMatchingTestWebhook(webhooks, matching.url), matching)
  assert.equal(findMatchingTestWebhook(webhooks, 'https://missing.test/waffo'), undefined)
  assert.equal(findMatchingTestWebhook(webhooks, '  '), undefined)
})

test('smoke catalog queries use the supported root store and product fields', () => {
  assert.match(WAFFO_PANCAKE_STORES_QUERY, /^query \{ stores \{ id name status \} \}$/)
  assert.doesNotMatch(WAFFO_PANCAKE_STORES_QUERY, /onetimeProducts/)
  assert.match(WAFFO_PANCAKE_ACTIVE_PRODUCTS_QUERY, /onetimeProducts\(filter:/)
  assert.match(WAFFO_PANCAKE_ACTIVE_PRODUCTS_QUERY, /storeId: \{ eq: \$storeId \}/)
  assert.match(WAFFO_PANCAKE_ACTIVE_PRODUCTS_QUERY, /status: \{ eq: "active" \}/)
})
