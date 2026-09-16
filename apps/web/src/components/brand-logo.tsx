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
import { Component } from 'react'

import { LmmBrandMark } from '@/components/lmm-brand-mark'
import { DEFAULT_LOGO, isDefaultLogo } from '@/lib/constants'
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

function BuiltInLogo({ alt, className, width, height }: BrandLogoProps) {
  return (
    <LmmBrandMark
      title={alt || undefined}
      width={width}
      height={height}
      className={className}
    />
  )
}

class ConfiguredLogo extends Component<
  BrandLogoProps & { src: string },
  { failed: boolean }
> {
  override state = { failed: false }

  override render() {
    if (this.state.failed) return <BuiltInLogo {...this.props} />

    return (
      <img
        {...this.props}
        alt={this.props.alt ?? ''}
        onError={() => this.setState({ failed: true })}
      />
    )
  }
}

/**
 * Use one mark across public and authenticated pages. Default, unsafe and
 * failed image sources fall back to the inline mark without another request.
 */
export function BrandLogo({ src, ...props }: BrandLogoProps) {
  const resolvedSrc = src?.trim() || DEFAULT_LOGO

  if (isDefaultLogo(resolvedSrc) || !isSafeResourceUrl(resolvedSrc)) {
    return <BuiltInLogo {...props} />
  }

  // A new URL gets fresh error state; an old image error cannot hide it.
  return <ConfiguredLogo key={resolvedSrc} {...props} src={resolvedSrc} />
}
