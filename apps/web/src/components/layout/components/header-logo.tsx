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
import { BrandLogo } from '@/components/brand-logo'
import { isDefaultLogo } from '@/lib/constants'
import { cn } from '@/lib/utils'

interface HeaderLogoProps {
  src: string
  alt?: string
  loading: boolean
  logoLoaded: boolean
  className?: string
  width?: number
  height?: number
  decoding?: 'async' | 'auto' | 'sync'
  fetchPriority?: 'high' | 'low' | 'auto'
}

/**
 * Logo component for header with loading state
 * Shows image only when fully loaded for smooth UX
 */
export function HeaderLogo({
  src,
  alt = '',
  loading,
  logoLoaded,
  className,
  width,
  height,
  decoding,
  fetchPriority,
}: HeaderLogoProps) {
  const isCustomLogo = !isDefaultLogo(src)
  let visibilityClassName: string | undefined
  if (isCustomLogo) {
    visibilityClassName = loading || !logoLoaded ? 'opacity-0' : 'opacity-100'
  }

  return (
    <BrandLogo
      src={src}
      alt={alt}
      width={width}
      height={height}
      decoding={decoding}
      fetchPriority={fetchPriority}
      className={cn(
        'h-7 w-7 transition-opacity duration-200',
        visibilityClassName,
        className
      )}
    />
  )
}
