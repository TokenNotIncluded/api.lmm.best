/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import { initializeFrontendCache } from './frontend-cache'

const dom = new Window()
Object.defineProperty(globalThis, 'window', { configurable: true, value: dom })
after(() => dom.close())

test('UI cache rotation preserves pending checkout receipts', () => {
  dom.localStorage.setItem('newapi:default:cache-version', 'older-version')
  dom.localStorage.setItem('wallet-topup-cloud:7', '[{"tradeNo":"pending"}]')
  dom.localStorage.setItem('old-ui-cache', 'stale')
  initializeFrontendCache()
  assert.equal(
    dom.localStorage.getItem('wallet-topup-cloud:7'),
    '[{"tradeNo":"pending"}]'
  )
  assert.equal(dom.localStorage.getItem('old-ui-cache'), null)
})
