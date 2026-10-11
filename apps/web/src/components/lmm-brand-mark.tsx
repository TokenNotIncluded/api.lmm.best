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
import { useId, type SVGProps } from 'react'

import { cn } from '@/lib/utils'

export const LMM_BRAND_NAME = 'LMM API'

type LmmBrandMarkProps = SVGProps<SVGSVGElement> & {
  title?: string
}

/** Folded ribbon adaptation of the supplied Language Model Mesh identity. */
export function LmmBrandMark({
  className,
  title,
  ...props
}: LmmBrandMarkProps) {
  const gradientId = useId()
  return (
    <svg
      viewBox='0 -89 448 448'
      xmlns='http://www.w3.org/2000/svg'
      role={title ? 'img' : undefined}
      aria-label={title}
      aria-hidden={title ? undefined : true}
      focusable='false'
      className={cn('shrink-0', className)}
      {...props}
    >
      <defs>
        <linearGradient id={gradientId} x1='0' y1='0' x2='1' y2='1'>
          <stop stopColor='currentColor' />
          <stop offset='1' stopColor='currentColor' stopOpacity='.65' />
        </linearGradient>
      </defs>
      <path
        d='M8 67C8 53 12 45 24 38L76 7C79 5 80 7 80 12V125C80 135 85 138 92 134L25 174C15 180 8 174 8 164Z'
        fill='currentColor'
      />
      <path
        d='M8 163C8 173 16 178 26 172L119 118C132 110 144 110 156 118L169 127C178 133 183 142 183 153V174C183 187 178 194 167 201L119 229C108 236 96 236 85 229L24 193C13 186 8 178 8 163Z'
        fill='currentColor'
        opacity='.65'
      />
      <path
        d='M145 98L193 70C207 62 218 62 231 71L294 113C307 122 314 133 314 148V194C314 210 308 220 295 228L244 257C241 259 239 257 239 253V184C239 171 234 161 223 153Z'
        fill={`url(#${gradientId})`}
      />
      <path
        d='M282 98L330 70C344 62 355 62 368 71L418 105C430 114 437 125 437 140V194C437 210 431 220 418 228L367 257C364 259 362 257 362 253V184C362 171 357 161 346 153Z'
        fill={`url(#${gradientId})`}
      />
    </svg>
  )
}
