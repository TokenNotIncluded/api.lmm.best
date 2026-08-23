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

import {
  getHeroSmsPreviewCustomerPrice,
  serializeHeroSmsSettingsUpdate,
  toHeroSmsSettingsFormValues,
} from './hero-sms-api'

describe('hero sms settings api helpers', () => {
  test('keeps blank api key out of update payloads', () => {
    assert.deepEqual(
      serializeHeroSmsSettingsUpdate({
        enabled: true,
        emailEnabled: true,
        smsEnabled: false,
        apiKey: '   ',
        priceMultiplier: 1,
      }),
      {
        enabled: true,
        email_enabled: true,
        sms_enabled: false,
        price_multiplier: '1',
      }
    )

    assert.deepEqual(
      serializeHeroSmsSettingsUpdate({
        enabled: false,
        emailEnabled: false,
        smsEnabled: true,
        apiKey: 'secret-key',
        priceMultiplier: 12,
      }),
      {
        enabled: false,
        email_enabled: false,
        sms_enabled: true,
        price_multiplier: '12',
        api_key: 'secret-key',
      }
    )
  })

  test('builds form defaults and preview values', () => {
    assert.deepEqual(
      toHeroSmsSettingsFormValues({
        enabled: true,
        email_enabled: true,
        sms_enabled: true,
        api_key_configured: true,
        pending_work: false,
        currency: 'USD',
        currency_code: 840,
        price_multiplier: 0,
      }),
      {
        enabled: true,
        emailEnabled: true,
        smsEnabled: true,
        apiKey: '',
        priceMultiplier: 1,
      }
    )

    assert.equal(getHeroSmsPreviewCustomerPrice(1), 1)
    assert.equal(getHeroSmsPreviewCustomerPrice(12.5), 12.5)
  })
})
