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

import { PROFILE_SHARE_URL } from './share-card'

export type BadgeTheme = 'paper' | 'dark' | 'transparent'
export type BadgeLayout = 'profile' | 'badge' | 'models'
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
    bg: '#faf9f5',
    fg: '#17231f',
    accent: '#d97757',
    muted: '#6c746d',
    border: '#d9ded5',
  },
  dark: {
    bg: '#202020',
    fg: '#f3f3f3',
    accent: '#dedede',
    muted: '#aaa9a8',
    border: '#363636',
  },
  transparent: {
    bg: '#faf9f5',
    fg: '#17231f',
    accent: '#d97757',
    muted: '#6c746d',
    border: '#d9ded5',
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
    radius: 20,
    colors: BADGE_PALETTES.dark,
  },
  {
    name: 'Paper',
    theme: 'paper',
    animation: 'none',
    font: 'serif',
    radius: 16,
    colors: BADGE_PALETTES.paper,
  },
  {
    name: 'Terminal',
    theme: 'dark',
    animation: 'pulse',
    font: 'mono',
    radius: 14,
    colors: {
      bg: '#0d1117',
      fg: '#e6edf3',
      accent: '#3fb950',
      muted: '#8b949e',
      border: '#30363d',
    },
  },
  {
    name: 'Ocean',
    theme: 'dark',
    animation: 'wave',
    font: 'sans',
    radius: 28,
    colors: {
      bg: '#0b1320',
      fg: '#eaf3ff',
      accent: '#6fb7ff',
      muted: '#8ea3b8',
      border: '#26384c',
    },
  },
  {
    name: 'Mint',
    theme: 'paper',
    animation: 'pulse',
    font: 'sans',
    radius: 28,
    colors: {
      bg: '#f1f8f4',
      fg: '#163228',
      accent: '#2f8f6b',
      muted: '#6a7f75',
      border: '#c9ddd2',
    },
  },
  {
    name: 'Ember',
    theme: 'dark',
    animation: 'pulse',
    font: 'serif',
    radius: 24,
    colors: {
      bg: '#211713',
      fg: '#fff2e9',
      accent: '#ff8a5b',
      muted: '#c0a399',
      border: '#4b342c',
    },
  },
  {
    name: 'Violet',
    theme: 'dark',
    animation: 'wave',
    font: 'sans',
    radius: 30,
    colors: {
      bg: '#181524',
      fg: '#f3efff',
      accent: '#a78bfa',
      muted: '#aaa0c2',
      border: '#3b3451',
    },
  },
  {
    name: 'Clear',
    theme: 'transparent',
    animation: 'none',
    font: 'sans',
    radius: 24,
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
  radius: 20,
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
  language: string
): string {
  const url = new URL(baseURL)
  url.search = ''
  url.hash = ''
  for (const [key, value] of Object.entries({
    layout: options.layout,
    theme: options.theme,
    period: options.period,
    animation: options.animation,
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
    height: layout === 'profile' ? 865 : layout === 'models' ? 900 : 240,
  }
}

export function canShareBadge(
  state: { enabled: boolean; model_usage_enabled?: boolean } | undefined,
  layout: BadgeLayout
): boolean {
  return Boolean(
    state?.enabled && (layout !== 'models' || state.model_usage_enabled)
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
    options.layout === 'models'
      ? 'LMM Best model usage'
      : 'LMM Best token usage'
  return {
    markdown: `[![${alt}](${url})](${PROFILE_SHARE_URL})`,
    html: `<a href="${PROFILE_SHARE_URL}" target="_blank" rel="noopener noreferrer"><img src="${escape(url)}" alt="${alt}" width="${options.width}" height="${options.height}"></a>`,
  }
}
