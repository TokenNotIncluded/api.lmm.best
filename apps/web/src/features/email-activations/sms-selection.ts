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
import type { HeroSmsSmsCountry, HeroSmsSmsOrder } from './sms-api.js'

export const HERO_SMS_FAVORITES_STORAGE_KEY = 'lmm-hero-sms-favorites:v1'
export const HERO_SMS_MAX_FAVORITES = 30
export const HERO_SMS_MAX_QUANTITY = 10

export interface HeroSmsFavoritePair {
  serviceCode: string
  countryId: number
}

interface HeroSmsStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

interface HeroSmsFavoriteUpdate {
  items: HeroSmsFavoritePair[]
  added: boolean
  limitReached: boolean
  persisted: boolean
}

const activeStatuses = new Set([
  'pending_provider',
  'purchase_unknown',
  'active',
  'cancel_pending',
])

const regionAliases = {
  bolivia: 'BO',
  'bolivia plurinational state of': 'BO',
  brunei: 'BN',
  'brunei darussalam': 'BN',
  burma: 'MM',
  'cabo verde': 'CV',
  'cape verde': 'CV',
  congo: 'CG',
  'congo democratic republic of the': 'CD',
  'cote d ivoire': 'CI',
  'czech republic': 'CZ',
  czechia: 'CZ',
  'democratic people s republic of korea': 'KP',
  'democratic republic of the congo': 'CD',
  'east timor': 'TL',
  england: 'GB',
  'federated states of micronesia': 'FM',
  'great britain': 'GB',
  'holy see': 'VA',
  'hong kong': 'HK',
  'hong kong sar china': 'HK',
  iran: 'IR',
  'iran islamic republic of': 'IR',
  'ivory coast': 'CI',
  'korea democratic people s republic of': 'KP',
  'korea republic of': 'KR',
  kosovo: 'XK',
  laos: 'LA',
  'lao people s democratic republic': 'LA',
  macao: 'MO',
  'macao sar china': 'MO',
  macau: 'MO',
  macedonia: 'MK',
  'micronesia federated states of': 'FM',
  moldova: 'MD',
  'moldova republic of': 'MD',
  'myanmar burma': 'MM',
  'north korea': 'KP',
  'palestinian territories': 'PS',
  palestine: 'PS',
  'republic of korea': 'KR',
  'republic of the congo': 'CG',
  russia: 'RU',
  'russian federation': 'RU',
  'south korea': 'KR',
  swaziland: 'SZ',
  syria: 'SY',
  'syrian arab republic': 'SY',
  taiwan: 'TW',
  'taiwan province of china': 'TW',
  tanzania: 'TZ',
  'tanzania united republic of': 'TZ',
  'the netherlands': 'NL',
  'timor leste': 'TL',
  turkey: 'TR',
  uae: 'AE',
  uk: 'GB',
  'united kingdom': 'GB',
  'united states': 'US',
  'united states of america': 'US',
  usa: 'US',
  vatican: 'VA',
  venezuela: 'VE',
  'venezuela bolivarian republic of': 'VE',
  'viet nam': 'VN',
  vietnam: 'VN',
  'virgin islands british': 'VG',
  'virgin islands us': 'VI',
} as const satisfies Record<string, string>

const currentRegionCodes = `
AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS XK YE YT ZA ZM ZW
`
  .trim()
  .split(/\s+/)

let englishRegionIndex: Map<string, string> | null = null

function normalizeCountryName(value: string) {
  return value
    .normalize('NFKD')
    .replaceAll(/[\u0300-\u036f]/g, '')
    .replaceAll('&', ' and ')
    .replaceAll(/[^a-zA-Z0-9]+/g, ' ')
    .trim()
    .toLowerCase()
}

function getEnglishRegionIndex() {
  if (englishRegionIndex) return englishRegionIndex

  const index = new Map<string, string>()
  if (typeof Intl.DisplayNames === 'function') {
    const displayNames = new Intl.DisplayNames(['en'], { type: 'region' })
    for (const code of currentRegionCodes) {
      const name = displayNames.of(code)
      if (!name || name === code || name === 'Unknown Region') continue
      index.set(normalizeCountryName(name), code)
    }
  }
  // Canonical aliases intentionally win over deprecated ISO entries (for
  // example, Intl may label the retired SU code as "Russia").
  for (const [name, code] of Object.entries(regionAliases)) {
    index.set(name, code)
  }
  englishRegionIndex = index
  return index
}

function isFavoritePair(value: unknown): value is HeroSmsFavoritePair {
  if (!value || typeof value !== 'object') return false
  const pair = value as Partial<HeroSmsFavoritePair>
  return (
    typeof pair.serviceCode === 'string' &&
    pair.serviceCode.trim().length > 0 &&
    pair.serviceCode.length <= 64 &&
    Number.isInteger(pair.countryId) &&
    Number(pair.countryId) >= 0
  )
}

function favoritePairKey(pair: HeroSmsFavoritePair) {
  return `${pair.serviceCode}:${pair.countryId}`
}

function getDefaultStorage() {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage
  } catch {
    return null
  }
}

export function loadHeroSmsFavorites(
  storage: HeroSmsStorage | null = getDefaultStorage()
) {
  if (!storage) return []
  try {
    const parsed = JSON.parse(
      storage.getItem(HERO_SMS_FAVORITES_STORAGE_KEY) ?? '[]'
    ) as unknown
    if (!Array.isArray(parsed)) return []
    const seen = new Set<string>()
    const favorites: HeroSmsFavoritePair[] = []
    for (const value of parsed) {
      if (!isFavoritePair(value)) continue
      const pair = {
        serviceCode: value.serviceCode.trim(),
        countryId: value.countryId,
      }
      const key = favoritePairKey(pair)
      if (seen.has(key)) continue
      seen.add(key)
      favorites.push(pair)
      if (favorites.length === HERO_SMS_MAX_FAVORITES) break
    }
    return favorites
  } catch {
    return []
  }
}

export function saveHeroSmsFavorites(
  favorites: HeroSmsFavoritePair[],
  storage: HeroSmsStorage | null = getDefaultStorage()
) {
  if (!storage) return false
  try {
    storage.setItem(
      HERO_SMS_FAVORITES_STORAGE_KEY,
      JSON.stringify(favorites.slice(0, HERO_SMS_MAX_FAVORITES))
    )
    return true
  } catch {
    return false
  }
}

export function toggleHeroSmsFavorite(
  favorites: HeroSmsFavoritePair[],
  pair: HeroSmsFavoritePair,
  storage?: HeroSmsStorage | null
): HeroSmsFavoriteUpdate {
  const key = favoritePairKey(pair)
  const existingIndex = favorites.findIndex(
    (favorite) => favoritePairKey(favorite) === key
  )
  if (existingIndex >= 0) {
    const items = favorites.filter((_, index) => index !== existingIndex)
    return {
      items,
      added: false,
      limitReached: false,
      persisted: saveHeroSmsFavorites(items, storage),
    }
  }
  if (favorites.length >= HERO_SMS_MAX_FAVORITES) {
    return {
      items: favorites,
      added: false,
      limitReached: true,
      persisted: true,
    }
  }
  const items = [pair, ...favorites]
  return {
    items,
    added: true,
    limitReached: false,
    persisted: saveHeroSmsFavorites(items, storage),
  }
}

export function hasHeroSmsFavorite(
  favorites: HeroSmsFavoritePair[],
  serviceCode: string,
  countryId: number
) {
  const key = favoritePairKey({ serviceCode, countryId })
  return favorites.some((favorite) => favoritePairKey(favorite) === key)
}

export function resolveHeroSmsCountryCode(country: HeroSmsSmsCountry) {
  const englishName = normalizeCountryName(country.english_name || '')
  if (!englishName) return null
  return getEnglishRegionIndex().get(englishName) ?? null
}

export function getHeroSmsCountryFlag(country: HeroSmsSmsCountry) {
  const code = resolveHeroSmsCountryCode(country)
  if (!code) return null
  return String.fromCodePoint(
    ...[...code].map((character) => 127397 + character.charCodeAt(0))
  )
}

function resolveDisplayLanguageTag(language: string) {
  if (language === 'zhCN') return 'zh-CN'
  if (language === 'zhTW') return 'zh-TW'
  return language || 'en'
}

function prefersProviderChineseName(languageTag: string) {
  const normalized = languageTag.toLowerCase()
  return (
    normalized === 'zh' ||
    normalized === 'zh-cn' ||
    normalized.startsWith('zh-hans')
  )
}

function localizedRegionName(code: string | null, languageTag: string) {
  if (!code || typeof Intl.DisplayNames !== 'function') return null
  try {
    const localized = new Intl.DisplayNames([languageTag], {
      type: 'region',
    }).of(code)
    if (localized && localized !== code) return localized
  } catch {
    // Fall through to provider-owned names for unsupported locales.
  }
  return null
}

export function getHeroSmsCountryName(
  country: HeroSmsSmsCountry,
  language: string
) {
  const languageTag = resolveDisplayLanguageTag(language)
  if (prefersProviderChineseName(languageTag)) {
    return country.chinese_name || country.name || country.english_name
  }
  return (
    localizedRegionName(resolveHeroSmsCountryCode(country), languageTag) ||
    country.english_name ||
    country.name ||
    country.chinese_name
  )
}

export function getHeroSmsCountrySearchText(country: HeroSmsSmsCountry) {
  return [
    country.name,
    country.english_name,
    country.chinese_name,
    String(country.id),
  ]
    .filter(Boolean)
    .join(' ')
}

export function getHeroSmsQuickIndex(value: string) {
  const first = value.trim().normalize('NFKD').charAt(0).toUpperCase()
  return /^[A-Z]$/.test(first) ? first : '#'
}

export function clampHeroSmsQuantity(quantity: number, inventory: number) {
  const available = Math.max(1, Math.min(HERO_SMS_MAX_QUANTITY, inventory || 1))
  if (!Number.isFinite(quantity)) return 1
  return Math.max(1, Math.min(available, Math.floor(quantity)))
}

export function isActiveHeroSmsSmsOrder(
  order: HeroSmsSmsOrder | null | undefined
) {
  return Boolean(order && activeStatuses.has(order.status))
}
