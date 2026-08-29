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
import { useEffect, useMemo, useState } from 'react'

import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import {
  getGravatarUrl,
  getUserAvatarFallback,
  getUserAvatarStyle,
} from '@/lib/avatar'
import { cn } from '@/lib/utils'

type UserAvatarProps = React.ComponentProps<typeof Avatar> & {
  name: string
  email?: string | null
  alt?: string
  imageClassName?: string
  fallbackClassName?: string
  gravatarSize?: number
}

export function UserAvatar({
  name,
  email,
  alt,
  className,
  imageClassName,
  fallbackClassName,
  gravatarSize = 192,
  ...avatarProps
}: UserAvatarProps) {
  const [gravatarUrl, setGravatarUrl] = useState<string | null>(null)
  const [imageLoaded, setImageLoaded] = useState(false)
  const fallback = getUserAvatarFallback(name)
  const fallbackStyle = useMemo(() => getUserAvatarStyle(name), [name])

  useEffect(() => {
    let active = true
    setGravatarUrl(null)
    setImageLoaded(false)

    void getGravatarUrl(email, gravatarSize)
      .then((url) => {
        if (active) setGravatarUrl(url)
      })
      .catch(() => {
        if (active) setGravatarUrl(null)
      })

    return () => {
      active = false
    }
  }, [email, gravatarSize])

  return (
    <Avatar className={cn('overflow-hidden', className)} {...avatarProps}>
      {gravatarUrl ? (
        <img
          data-slot='avatar-image'
          src={gravatarUrl}
          alt={alt ?? name}
          referrerPolicy='no-referrer'
          onLoad={() => setImageLoaded(true)}
          onError={() => setImageLoaded(false)}
          className={cn(
            'absolute inset-0 z-[1] aspect-square size-full object-cover',
            !imageLoaded && 'invisible',
            imageClassName
          )}
        />
      ) : null}
      <AvatarFallback
        className={cn(
          'font-semibold text-white',
          imageLoaded && 'invisible',
          fallbackClassName
        )}
        style={fallbackStyle}
      >
        {fallback}
      </AvatarFallback>
    </Avatar>
  )
}
