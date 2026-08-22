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
/**
 * Stable colour palette for vendors, used in both the share chart and the
 * legend dots. Falls back to a neutral palette for unknown vendors so that
 * future additions still render. Concrete hex values (rather than CSS
 * variables) because canvas-backed charts receive colour strings.
 */
const VENDOR_COLOURS = {
  OpenAI: '#10a37f',
  Anthropic: '#d97757',
  Google: '#4285f4',
  DeepSeek: '#7c5cff',
  Alibaba: '#ff9900',
  xAI: '#1f2937',
  Meta: '#1877f2',
  Moonshot: '#ec4899',
  Zhipu: '#06b6d4',
  Mistral: '#ff7000',
  ByteDance: '#3b82f6',
  Tencent: '#22c55e',
  MiniMax: '#a855f7',
  Cohere: '#fb923c',
  Baidu: '#ef4444',
  Others: '#94a3b8',
} satisfies Record<string, string>

const FALLBACK_PALETTE = [
  '#0ea5e9',
  '#22c55e',
  '#a855f7',
  '#f97316',
  '#14b8a6',
  '#eab308',
  '#ec4899',
  '#84cc16',
  '#6366f1',
  '#10b981',
  '#f43f5e',
  '#0891b2',
  '#94a3b8',
]

export function buildVendorColourMap(names: string[]): Record<string, string> {
  const known: Record<string, string | undefined> = VENDOR_COLOURS
  const result: Record<string, string> = {}
  let fallbackIdx = 0
  for (const name of names) {
    const knownColour = known[name]
    if (knownColour) {
      result[name] = knownColour
    } else {
      result[name] = FALLBACK_PALETTE[fallbackIdx % FALLBACK_PALETTE.length]
      fallbackIdx += 1
    }
  }
  return result
}

/** Vendor colours for VChart specs, resolved per theme. */
export function getVendorChartColour(
  map: Record<string, string>,
  vendor: string,
  resolvedTheme?: string
) {
  const base = map[vendor] ?? '#94a3b8'
  // The xAI ink is unreadable on a dark canvas; lift it for dark themes.
  if (resolvedTheme === 'dark' && base === '#1f2937') return '#64748b'
  return base
}

/** Theme-aware axis/grid colours for canvas charts. */
export function getChartTextColour(resolvedTheme?: string) {
  return resolvedTheme === 'dark'
    ? 'rgba(255, 255, 255, 0.68)'
    : 'rgba(15, 23, 42, 0.58)'
}

export function getChartGridColour(resolvedTheme?: string) {
  return resolvedTheme === 'dark'
    ? 'rgba(255, 255, 255, 0.12)'
    : 'rgba(15, 23, 42, 0.12)'
}

/** Series palette for non-vendor charts (model bars, ranking rows). */
export function getChartPalette(resolvedTheme?: string) {
  if (resolvedTheme === 'dark') {
    return ['#38bdf8', '#4ade80', '#c084fc', '#fb923c', '#2dd4bf']
  }
  return ['#0284c7', '#16a34a', '#9333ea', '#ea580c', '#0d9488']
}
