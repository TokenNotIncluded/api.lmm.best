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
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { GOOGLE_BUILT_IN_PRESET } from '@/features/auth/lib/google-oauth'

import { customOAuthFormSchema, OAUTH_PRESETS } from './types'

describe('Google built-in vs custom registry', () => {
  test('moves the template to the fixed built-in provider', () => {
    assert.equal(
      OAUTH_PRESETS.some((preset) => preset.key === 'google'),
      false
    )
    assert.equal(GOOGLE_BUILT_IN_PRESET.key, 'google')
    assert.equal(
      GOOGLE_BUILT_IN_PRESET.authorization_endpoint,
      'https://accounts.google.com/o/oauth2/v2/auth'
    )
    assert.equal(
      GOOGLE_BUILT_IN_PRESET.token_endpoint,
      'https://oauth2.googleapis.com/token'
    )
    assert.equal(
      GOOGLE_BUILT_IN_PRESET.user_info_endpoint,
      'https://openidconnect.googleapis.com/v1/userinfo'
    )
    assert.equal(GOOGLE_BUILT_IN_PRESET.scopes, 'openid profile email')
    assert.ok(Object.isFrozen(GOOGLE_BUILT_IN_PRESET))
    assert.ok(OAUTH_PRESETS.some((preset) => preset.key === 'gitlab'))
  })

  test('existing custom Google credentials and slug remain valid editable records', () => {
    const existing = {
      name: 'Google',
      slug: 'google',
      icon: 'google',
      enabled: true,
      client_id: 'existing-test-client',
      client_secret: 'synthetic-test-secret',
      authorization_endpoint: GOOGLE_BUILT_IN_PRESET.authorization_endpoint,
      token_endpoint: GOOGLE_BUILT_IN_PRESET.token_endpoint,
      user_info_endpoint: GOOGLE_BUILT_IN_PRESET.user_info_endpoint,
      scopes: 'openid profile email',
      user_id_field: 'sub',
      username_field: 'email',
      display_name_field: 'name',
      email_field: 'email',
      well_known: '',
      auth_style: 0,
      access_policy: '',
      access_denied_message: '',
    }
    assert.deepEqual(customOAuthFormSchema.parse(existing), existing)
  })
})
