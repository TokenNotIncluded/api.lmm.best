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
export interface ForgeMetalWindowOrnamentCapabilities {
  appleWebKit: boolean
  coarsePointer: boolean
  forcedColors: boolean
  narrowViewport: boolean
  reducedMotion: boolean
  saveData: boolean
  supportsAnimationFrame: boolean
  supportsCanvas2D: boolean
  supportsIntersectionObserver: boolean
  supportsResizeObserver: boolean
  supportsRoundRect: boolean
  supportsWebGL: boolean
}

export function shouldEnableForgeMetalWindowOrnament(
  capabilities: ForgeMetalWindowOrnamentCapabilities
) {
  try {
    return (
      capabilities.supportsAnimationFrame &&
      capabilities.supportsCanvas2D &&
      capabilities.supportsIntersectionObserver &&
      capabilities.supportsResizeObserver &&
      capabilities.supportsRoundRect &&
      capabilities.supportsWebGL &&
      !capabilities.appleWebKit &&
      !capabilities.coarsePointer &&
      !capabilities.forcedColors &&
      !capabilities.narrowViewport &&
      !capabilities.reducedMotion &&
      !capabilities.saveData
    )
  } catch {
    return false
  }
}

export function isAppleWebKitBrowser(userAgent: string) {
  if (!/AppleWebKit\//u.test(userAgent)) return false
  if (/(?:iPhone|iPad|iPod)/u.test(userAgent)) return true

  return (
    /Safari\//u.test(userAgent) &&
    !/(?:Chrome|Chromium|Edg|OPR)\//u.test(userAgent)
  )
}
