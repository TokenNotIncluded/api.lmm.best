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

import { renderToStaticMarkup } from 'react-dom/server'

import { PendingReviewSuperscript } from './pending-review-superscript'

describe('pending bounty review superscript', () => {
  test('stays hidden when there is nothing to review', () => {
    const markup = renderToStaticMarkup(
      <PendingReviewSuperscript count={0} label='Pending review' />
    )

    assert.equal(markup, '')
  })

  test('renders the exact actionable count with accessible context', () => {
    const markup = renderToStaticMarkup(
      <PendingReviewSuperscript count={7} label='Pending review' />
    )

    assert.match(markup, /<sup/)
    assert.match(markup, />7</)
    assert.match(markup, /Pending review/)
    assert.match(markup, /tabular-nums/)
  })
})
