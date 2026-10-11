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
import { normalizeInterfaceLanguage } from '@/i18n/languages'
import type { WalletDisplayCurrency } from '@/lib/currency'

import { PROFILE_SHARE_URL } from './share-card'

export type BadgeTheme = 'paper' | 'dark' | 'transparent'
export type BadgeLayout = 'profile' | 'badge' | 'models' | 'aggregate'
export type BadgePeriod = '7d' | '30d' | '365d' | 'all'
export type BadgeAnimation = 'wave' | 'pulse' | 'none'
export type BadgeFont = 'sans' | 'mono' | 'serif'
export type BadgeFormat = 'compact' | 'full'

export interface BadgeColors {
  bg: string
  fg: string
  accent: string
  muted: string
  border: string
}

export interface BadgeOptions {
  layout: BadgeLayout
  theme: BadgeTheme
  period: BadgePeriod
  animation: BadgeAnimation
  font: BadgeFont
  format: BadgeFormat
  width: number
  height: number
  radius: number
  requests: boolean
  top: number
  title: string
  label: string
  footer: string
  colors: BadgeColors
}

export const BADGE_PALETTES: Record<BadgeTheme, BadgeColors> = {
  paper: {
    bg: '#ffffff',
    fg: '#1d2023',
    accent: '#555c64',
    muted: '#666d75',
    border: '#dfe2e6',
  },
  dark: {
    bg: '#1d2023',
    fg: '#ffffff',
    accent: '#d9dde2',
    muted: '#aeb4bc',
    border: '#3b4046',
  },
  transparent: {
    bg: '#ffffff',
    fg: '#1d2023',
    accent: '#555c64',
    muted: '#666d75',
    border: '#dfe2e6',
  },
}

interface BadgeStylePreset {
  name: string
  theme: BadgeTheme
  animation: BadgeAnimation
  font: BadgeFont
  radius: number
  colors: BadgeColors
}

export const BADGE_STYLE_PRESETS: BadgeStylePreset[] = [
  {
    name: 'Graphite',
    theme: 'dark',
    animation: 'wave',
    font: 'sans',
    radius: 16,
    colors: BADGE_PALETTES.dark,
  },
  {
    name: 'Paper',
    theme: 'paper',
    animation: 'wave',
    font: 'sans',
    radius: 16,
    colors: BADGE_PALETTES.paper,
  },
  {
    name: 'Clear',
    theme: 'transparent',
    animation: 'none',
    font: 'sans',
    radius: 16,
    colors: BADGE_PALETTES.transparent,
  },
]

export function isPresetActive(
  options: BadgeOptions,
  preset: BadgeStylePreset
): boolean {
  return (
    options.theme === preset.theme &&
    options.animation === preset.animation &&
    options.font === preset.font &&
    options.radius === preset.radius &&
    Object.entries(preset.colors).every(
      ([key, value]) => options.colors[key as keyof BadgeColors] === value
    )
  )
}

export const INITIAL_OPTIONS: BadgeOptions = {
  layout: 'models',
  theme: 'dark',
  period: '30d',
  animation: 'wave',
  font: 'sans',
  format: 'compact',
  width: 1200,
  height: 900,
  radius: 16,
  requests: true,
  top: 6,
  title: '',
  label: '',
  footer: '',
  colors: BADGE_PALETTES.dark,
}

export function buildBadgeURL(
  baseURL: string,
  options: BadgeOptions,
  language: string,
  currency?: WalletDisplayCurrency
): string {
  const url = new URL(baseURL)
  url.search = ''
  url.hash = ''
  for (const [key, value] of Object.entries({
    layout: options.layout,
    theme: options.theme,
    period: options.period,
    animation: options.layout === 'aggregate' ? 'none' : options.animation,
    font: options.font,
    format: options.format,
    width: options.width,
    height: options.height,
    radius: options.radius,
    requests: options.requests ? '1' : '0',
    lang: badgeLanguage(language),
  })) {
    url.searchParams.set(key, String(value))
  }
  if (options.layout === 'models') {
    url.searchParams.set('top', String(options.top))
    if (currency) url.searchParams.set('currency', currency)
  }
  for (const [key, value] of Object.entries(options.colors)) {
    if (key !== 'bg' || options.theme !== 'transparent') {
      url.searchParams.set(key, value)
    }
  }
  for (const key of ['title', 'label', 'footer'] as const) {
    if (options[key].trim()) url.searchParams.set(key, options[key].trim())
  }
  return url.toString()
}

export function clampNumber(
  value: string,
  fallback: number,
  min: number,
  max: number
) {
  const number = Number(value)
  return Number.isFinite(number)
    ? Math.min(max, Math.max(min, Math.round(number)))
    : fallback
}

export function badgeLanguage(language: string): string {
  const normalized = normalizeInterfaceLanguage(language)
  return normalized === 'zhCN'
    ? 'zh'
    : normalized === 'zhTW'
      ? 'zh-TW'
      : normalized
}

export function changeBadgeLayout(
  options: BadgeOptions,
  layout: BadgeLayout
): BadgeOptions {
  return {
    ...options,
    layout,
    period:
      layout === 'profile'
        ? '365d'
        : options.period === 'all'
          ? '30d'
          : options.period,
    width: layout === 'badge' ? 800 : 1200,
    height:
      layout === 'profile'
        ? 865
        : layout === 'models'
          ? 900
          : layout === 'aggregate'
            ? 720
            : 240,
  }
}

export function canShareBadge(
  state:
    | {
        enabled: boolean
        model_usage_enabled?: boolean
        aggregate_usage_enabled?: boolean
      }
    | undefined,
  layout: BadgeLayout
): boolean {
  return Boolean(
    state?.enabled &&
    (layout !== 'models' || state.model_usage_enabled) &&
    (layout !== 'aggregate' || state.aggregate_usage_enabled)
  )
}

export function buildBadgeEmbedCode(url: string, options: BadgeOptions) {
  if (!url) return { markdown: '', html: '' }
  const escape = (text: string) =>
    text
      .replaceAll('&', '&amp;')
      .replaceAll('"', '&quot;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
  const alt =
    options.layout === 'aggregate'
      ? 'AI usage across linked profiles'
      : options.layout === 'models'
        ? 'LMM API model usage'
        : 'LMM API token usage'
  return {
    markdown: `[![${alt}](${url})](${PROFILE_SHARE_URL})`,
    html: `<a href="${PROFILE_SHARE_URL}" target="_blank" rel="noopener noreferrer"><img src="${escape(url)}" alt="${alt}" width="${options.width}" height="${options.height}"></a>`,
  }
}
