/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { isRedirect } from '@tanstack/react-router'

import { Route as ApiKeysAlias } from './api-keys'
import { Route as SettingsAlias } from './settings'

describe('legacy navigation aliases', () => {
  for (const [route, destination] of [
    [ApiKeysAlias, '/keys'],
    [SettingsAlias, '/profile'],
  ] as const) {
    test(`redirects to ${destination}`, () => {
      assert.throws(
        () => route.options.beforeLoad?.({} as never),
        (error: unknown) =>
          isRedirect(error) &&
          error.options.to === destination &&
          error.options.replace === true
      )
    })
  }
})
