/*
Copyright (C) 2026 LIghtJUNction

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
*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'

const source = readFileSync(
  new URL('./pricing-toolbar.tsx', import.meta.url),
  'utf8'
)
const modelCardSource = readFileSync(
  new URL('./model-card.tsx', import.meta.url),
  'utf8'
)
const searchBarSource = readFileSync(
  new URL('./search-bar.tsx', import.meta.url),
  'utf8'
)
const vendorSectionsSource = readFileSync(
  new URL('./vendor-model-sections.tsx', import.meta.url),
  'utf8'
)

describe('PricingToolbar mobile controls', () => {
  test('keeps pricing display controls available inside the mobile filter sheet', () => {
    assert.match(
      source,
      /border-t pt-4 sm:hidden[\s\S]*Price display mode[\s\S]*Standard[\s\S]*Recharge[\s\S]*Token unit[\s\S]*\/1M[\s\S]*\/1K/
    )
  })

  test('keeps model-square controls at a thumb-friendly size on mobile', () => {
    assert.match(source, /inline-flex h-11 items-center[\s\S]*sm:h-8/)
    assert.match(source, /w-11 sm:w-7/)
    assert.match(source, /className='h-11 gap-1\.5 sm:h-7 xl:hidden'/)
    assert.match(source, /className='h-11 gap-1\.5 px-3 text-xs sm:h-8'/)
    assert.match(modelCardSource, /inline-flex min-h-11 items-center/)
    assert.match(
      modelCardSource,
      /min-h-11 min-w-11 items-center justify-center/
    )
    assert.match(searchBarSource, /h-11 w-full[\s\S]*sm:h-10/)
    assert.match(vendorSectionsSource, /min-h-16 gap-3 rounded-xl/)
  })
})
