/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { resolveHeroSmsPhoneNumber } from './sms-phone-number'

describe('HeroSMS phone-number presentation', () => {
  test('separates the Chile calling code and adds the E.164 plus sign', () => {
    assert.deepEqual(resolveHeroSmsPhoneNumber('56972498825'), {
      callingCode: '56',
      subscriberNumber: '972498825',
      e164: '+56972498825',
      display: '+56 972498825',
    })
  })

  test('normalizes an existing international prefix without duplicating it', () => {
    assert.deepEqual(resolveHeroSmsPhoneNumber('+44 7700 900123'), {
      callingCode: '44',
      subscriberNumber: '7700900123',
      e164: '+447700900123',
      display: '+44 7700900123',
    })
    assert.equal(
      resolveHeroSmsPhoneNumber('00 86 13800138000')?.display,
      '+86 13800138000'
    )
    assert.equal(
      resolveHeroSmsPhoneNumber('971501234567')?.display,
      '+971 501234567'
    )
  })

  test('preserves a canonical plus-prefixed value when the prefix is unknown', () => {
    assert.deepEqual(resolveHeroSmsPhoneNumber('99912345'), {
      callingCode: '',
      subscriberNumber: '99912345',
      e164: '+99912345',
      display: '+99912345',
    })
    assert.equal(resolveHeroSmsPhoneNumber(''), null)
  })

  test('preserves a redacted history number instead of inventing an E.164 number', () => {
    assert.deepEqual(resolveHeroSmsPhoneNumber('•••• 8825'), {
      callingCode: '',
      subscriberNumber: '•••• 8825',
      e164: '•••• 8825',
      display: '•••• 8825',
      masked: true,
    })
    assert.equal(resolveHeroSmsPhoneNumber('••••')?.display, '••••')
  })
})
