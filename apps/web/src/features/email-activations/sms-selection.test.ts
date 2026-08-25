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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { HeroSmsSmsCountry, HeroSmsSmsOrder } from './sms-api'
import {
  clampHeroSmsQuantity,
  getHeroSmsCountryFlag,
  getHeroSmsCountryName,
  HERO_SMS_FAVORITES_STORAGE_KEY,
  HERO_SMS_MAX_FAVORITES,
  isActiveHeroSmsSmsOrder,
  loadHeroSmsFavorites,
  toggleHeroSmsFavorite,
} from './sms-selection'

class MemoryStorage {
  values = new Map<string, string>()

  getItem(key: string) {
    return this.values.get(key) ?? null
  }

  setItem(key: string, value: string) {
    this.values.set(key, value)
  }
}

const russia: HeroSmsSmsCountry = {
  id: 6,
  name: '俄罗斯',
  english_name: 'Russia',
  chinese_name: '俄罗斯',
  popularity: 12,
}

function order(status: string): HeroSmsSmsOrder {
  return {
    id: 'hssms_test',
    country_id: 6,
    service: 'tg',
    operator: 'any',
    status,
    customer_price_usd: '2',
    charge_quota: 1,
    refunded_quota: 0,
    provider_id: '909',
    phone_number: '79001234567',
    code: '',
    message: '',
    last_error_code: '',
    last_error_message: '',
    created_at: 1,
    updated_at: 1,
  }
}

describe('phone activation selection helpers', () => {
  test('resolves an honest localized country identity', () => {
    assert.equal(getHeroSmsCountryFlag(russia), '🇷🇺')
    assert.equal(
      getHeroSmsCountryFlag({
        ...russia,
        id: 4,
        name: '法国',
        english_name: 'France',
        chinese_name: '法国',
      }),
      '🇫🇷'
    )
    assert.equal(
      getHeroSmsCountryFlag({
        ...russia,
        id: 14,
        name: '韩国',
        english_name: 'Korea, Republic of',
        chinese_name: '韩国',
      }),
      '🇰🇷'
    )
    assert.equal(getHeroSmsCountryName(russia, 'zhCN'), '俄罗斯')
    assert.equal(getHeroSmsCountryName(russia, 'zhTW'), '俄羅斯')
    assert.match(getHeroSmsCountryName(russia, 'en'), /Russia/)
  })

  test('persists minimal favorite pairs and toggles them', () => {
    const storage = new MemoryStorage()
    const added = toggleHeroSmsFavorite(
      [],
      { serviceCode: 'tg', countryId: 6 },
      storage
    )
    assert.equal(added.added, true)
    assert.equal(added.persisted, true)
    assert.deepEqual(loadHeroSmsFavorites(storage), [
      { serviceCode: 'tg', countryId: 6 },
    ])
    assert.equal(
      storage.values.get(HERO_SMS_FAVORITES_STORAGE_KEY),
      '[{"serviceCode":"tg","countryId":6}]'
    )

    const removed = toggleHeroSmsFavorite(
      added.items,
      { serviceCode: 'tg', countryId: 6 },
      storage
    )
    assert.equal(removed.added, false)
    assert.deepEqual(removed.items, [])
  })

  test('deduplicates stored favorites and enforces the limit', () => {
    const storage = new MemoryStorage()
    storage.setItem(
      HERO_SMS_FAVORITES_STORAGE_KEY,
      JSON.stringify([
        { serviceCode: 'tg', countryId: 6 },
        { serviceCode: 'tg', countryId: 6 },
        ...Array.from({ length: HERO_SMS_MAX_FAVORITES + 5 }, (_, index) => ({
          serviceCode: `service-${index}`,
          countryId: index,
        })),
        { serviceCode: '', countryId: -1 },
      ])
    )
    const favorites = loadHeroSmsFavorites(storage)
    assert.equal(favorites.length, HERO_SMS_MAX_FAVORITES)
    assert.deepEqual(favorites[0], { serviceCode: 'tg', countryId: 6 })

    const full = toggleHeroSmsFavorite(
      favorites,
      { serviceCode: 'another', countryId: 999 },
      storage
    )
    assert.equal(full.limitReached, true)
    assert.equal(full.items.length, HERO_SMS_MAX_FAVORITES)
  })

  test('clamps quantity and identifies every active order state', () => {
    assert.equal(clampHeroSmsQuantity(20, 50), 10)
    assert.equal(clampHeroSmsQuantity(5, 3), 3)
    assert.equal(clampHeroSmsQuantity(Number.NaN, 3), 1)
    assert.equal(isActiveHeroSmsSmsOrder(order('pending_provider')), true)
    assert.equal(isActiveHeroSmsSmsOrder(order('purchase_unknown')), true)
    assert.equal(isActiveHeroSmsSmsOrder(order('active')), true)
    assert.equal(isActiveHeroSmsSmsOrder(order('completed')), false)
  })
})
