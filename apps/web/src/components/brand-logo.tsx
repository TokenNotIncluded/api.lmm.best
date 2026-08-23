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
import { LmmBrandMark } from '@/components/lmm-brand-mark'
import { DEFAULT_LOGO } from '@/lib/constants'
import { isSafeResourceUrl } from '@/lib/content-format'

type BrandLogoProps = {
  /** An empty or default value renders the built-in inline mark. */
  src?: string
  /** Omit when adjacent text already names the brand. */
  alt?: string
  className?: string
  width?: number
  height?: number
  decoding?: 'async' | 'auto' | 'sync'
  fetchPriority?: 'high' | 'low' | 'auto'
}

/**
 * Renders the built-in mark inline and only creates an image request for a
 * tenant-provided logo. The legacy DEFAULT_LOGO sentinel remains the config
 * fallback but is never emitted as an image URL.
 */
export function BrandLogo({
  src,
  alt = '',
  className,
  width,
  height,
  decoding,
  fetchPriority,
}: BrandLogoProps) {
  const resolvedSrc = src?.trim() || DEFAULT_LOGO

  if (resolvedSrc === DEFAULT_LOGO || !isSafeResourceUrl(resolvedSrc)) {
    return (
      <LmmBrandMark
        title={alt || undefined}
        width={width}
        height={height}
        className={className}
      />
    )
  }

  return (
    <img
      src={resolvedSrc}
      alt={alt}
      width={width}
      height={height}
      decoding={decoding}
      fetchPriority={fetchPriority}
      className={className}
    />
  )
}
