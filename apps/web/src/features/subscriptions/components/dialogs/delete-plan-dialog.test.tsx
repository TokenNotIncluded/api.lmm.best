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

import { getDeletePlanErrorMessage } from './delete-plan-dialog.js'

describe('getDeletePlanErrorMessage', () => {
  test('prefers the backend response message', () => {
    assert.equal(
      getDeletePlanErrorMessage(
        {
          response: {
            status: 409,
            data: { message: 'Subscription plan is still referenced' },
          },
        },
        'Operation failed'
      ),
      'Subscription plan is still referenced'
    )
  })

  test('includes the HTTP status when the backend omits a message', () => {
    assert.equal(
      getDeletePlanErrorMessage(
        { response: { status: 503, data: {} } },
        'Operation failed'
      ),
      'Operation failed (503)'
    )
  })
})
