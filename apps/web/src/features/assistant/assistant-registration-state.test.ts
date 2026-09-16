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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { expect, test } from 'bun:test'
import { registrationState, registrationStateCopy } from './assistant-registration-state'

test('unrecognized or missing server states never imply approval', () => {
  for (const state of [undefined, null, 'approved', 'verified_human', {}, 1]) {
    expect(registrationState(state)).toBe('context_needed')
  }
})
test('ready means eligible, not already activated', () => {
  expect(registrationStateCopy('ready').title).not.toContain('active')
  expect(registrationStateCopy('active').title).toContain('active')
})
test('a hold preserves a human support path', () => {
  expect(registrationStateCopy('held').detail).toContain('human support')
})
