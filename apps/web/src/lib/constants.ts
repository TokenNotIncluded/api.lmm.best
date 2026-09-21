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
 * Application-wide constants
 */

// System Configuration Defaults
export const DEFAULT_SYSTEM_NAME = 'LMM Best'
export const DEFAULT_LOGO = '/lmm-best-mark.svg'

/** Normalize only our shipped defaults; preserve tenant-provided artwork. */
export function isDefaultLogo(src?: string): boolean {
  if (!src?.trim()) return true
  try {
    const url = new URL(src.trim(), 'https://api.lmm.best')
    const ownOrigin =
      url.hostname === 'api.lmm.best' ||
      (typeof window !== 'undefined' && url.origin === window.location.origin)
    return (
      ownOrigin &&
      [
        DEFAULT_LOGO,
        '/logo.png',
        '/favicon.ico',
        '/lmm-forge-mark.svg',
      ].includes(url.pathname)
    )
  } catch {
    return false
  }
}

export function resolveSystemName(value?: string): string {
  const name = value?.trim()
  return !name || ['New API', 'NewAPI', 'LMM API', 'LMM Forge'].includes(name)
    ? DEFAULT_SYSTEM_NAME
    : name
}
