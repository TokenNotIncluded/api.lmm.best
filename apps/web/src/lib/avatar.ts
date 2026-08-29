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
import type { CSSProperties } from 'react'

export type UserAvatarStyle = Pick<CSSProperties, 'backgroundColor' | 'color'>

function hashString(value: string): number {
  let hash = 0
  for (let i = 0; i < value.length; i++) {
    hash = (hash * 31 + value.charCodeAt(i)) >>> 0
  }
  return hash
}

export function getUserAvatarStyle(name: string): UserAvatarStyle {
  const hash = hashString(name)
  const hue = hash % 360
  const saturation = 54 + (hash % 8)
  const lightness = 52 + ((hash >> 4) % 8)

  return {
    backgroundColor: `hsl(${hue} ${saturation}% ${lightness}%)`,
    color: 'white',
  }
}

export function getUserAvatarFallback(name: string): string {
  return name.trim().charAt(0).toUpperCase() || '?'
}

export function normalizeGravatarEmail(email: string): string {
  return email.trim().toLowerCase()
}

/**
 * Build a Gravatar URL without adding an MD5 dependency. Gravatar accepts the
 * browser-native SHA-256 digest and returns 404 when the email has no avatar,
 * allowing the local AvatarFallback to remain authoritative.
 */
export async function getGravatarUrl(
  email: string | null | undefined,
  size = 192
): Promise<string | null> {
  const normalizedEmail = normalizeGravatarEmail(email ?? '')
  if (!normalizedEmail || !globalThis.crypto?.subtle) return null

  const digest = await globalThis.crypto.subtle.digest(
    'SHA-256',
    new TextEncoder().encode(normalizedEmail)
  )
  const hash = Array.from(new Uint8Array(digest), (byte) =>
    byte.toString(16).padStart(2, '0')
  ).join('')
  const normalizedSize = Math.min(2048, Math.max(1, Math.round(size)))

  return `https://gravatar.com/avatar/${hash}?d=404&r=g&s=${normalizedSize}`
}
