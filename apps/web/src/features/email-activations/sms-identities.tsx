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
import { Globe2 } from 'lucide-react'
import type { ComponentType, SVGProps } from 'react'

import { IconDiscord } from '@/assets/brand-icons/icon-discord'
import { IconFacebook } from '@/assets/brand-icons/icon-facebook'
import { IconFigma } from '@/assets/brand-icons/icon-figma'
import { IconGithub } from '@/assets/brand-icons/icon-github'
import { IconGitlab } from '@/assets/brand-icons/icon-gitlab'
import { IconGmail } from '@/assets/brand-icons/icon-gmail'
import { IconGoogle } from '@/assets/brand-icons/icon-google'
import { IconMedium } from '@/assets/brand-icons/icon-medium'
import { IconNotion } from '@/assets/brand-icons/icon-notion'
import { IconSkype } from '@/assets/brand-icons/icon-skype'
import { IconSlack } from '@/assets/brand-icons/icon-slack'
import { IconStripe } from '@/assets/brand-icons/icon-stripe'
import { IconTelegram } from '@/assets/brand-icons/icon-telegram'
import { IconTrello } from '@/assets/brand-icons/icon-trello'
import { IconWeChat } from '@/assets/brand-icons/icon-wechat'
import { IconWhatsapp } from '@/assets/brand-icons/icon-whatsapp'
import { IconZoom } from '@/assets/brand-icons/icon-zoom'
import { cn } from '@/lib/utils'

import type { HeroSmsSmsCountry, HeroSmsSmsService } from './sms-api.js'
import {
  getHeroSmsCountryFlag,
  getHeroSmsCountryName,
} from './sms-selection.js'

type BrandIcon = ComponentType<SVGProps<SVGSVGElement>>

const serviceIconRules: Array<{
  codes: string[]
  names: string[]
  icon: BrandIcon
}> = [
  { codes: ['tg'], names: ['telegram'], icon: IconTelegram },
  { codes: ['wa'], names: ['whatsapp'], icon: IconWhatsapp },
  { codes: ['go'], names: ['google'], icon: IconGoogle },
  { codes: [], names: ['gmail'], icon: IconGmail },
  { codes: ['fb'], names: ['facebook'], icon: IconFacebook },
  { codes: ['ds'], names: ['discord'], icon: IconDiscord },
  { codes: ['wb'], names: ['wechat', 'weixin'], icon: IconWeChat },
  { codes: ['sk'], names: ['skype'], icon: IconSkype },
  { codes: [], names: ['slack'], icon: IconSlack },
  { codes: ['gh'], names: ['github'], icon: IconGithub },
  { codes: [], names: ['gitlab'], icon: IconGitlab },
  { codes: [], names: ['zoom'], icon: IconZoom },
  { codes: [], names: ['notion'], icon: IconNotion },
  { codes: [], names: ['medium'], icon: IconMedium },
  { codes: [], names: ['trello'], icon: IconTrello },
  { codes: [], names: ['stripe'], icon: IconStripe },
  { codes: [], names: ['figma'], icon: IconFigma },
]

function getServiceBrandIcon(service: HeroSmsSmsService) {
  const code = service.code.trim().toLowerCase()
  const name = service.name.trim().toLowerCase()
  return serviceIconRules.find(
    (rule) =>
      rule.codes.includes(code) ||
      rule.names.some((candidate) => name.includes(candidate))
  )?.icon
}

export function SmsServiceIdentity({
  service,
  className,
}: {
  service: HeroSmsSmsService
  className?: string
}) {
  const Icon = getServiceBrandIcon(service)
  const fallback = (service.name || service.code)
    .trim()
    .slice(0, 2)
    .toUpperCase()
  return (
    <span
      className={cn(
        'bg-muted/60 text-foreground flex size-8 items-center justify-center rounded-lg border',
        className
      )}
      aria-hidden='true'
    >
      {Icon ? (
        <Icon className='size-4' />
      ) : (
        <span className='text-[10px] font-semibold tracking-tight'>
          {fallback}
        </span>
      )}
    </span>
  )
}

export function SmsCountryIdentity({
  country,
  language,
  className,
}: {
  country: HeroSmsSmsCountry
  language: string
  className?: string
}) {
  const flag = getHeroSmsCountryFlag(country)
  const label = getHeroSmsCountryName(country, language)
  return (
    <span
      className={cn(
        'bg-muted/60 flex size-8 items-center justify-center rounded-lg border',
        className
      )}
      title={label}
    >
      {flag ? (
        <span
          role='img'
          aria-label={label}
          className='font-["Apple_Color_Emoji","Segoe_UI_Emoji","Noto_Color_Emoji"] text-lg leading-none'
        >
          {flag}
        </span>
      ) : (
        <Globe2 aria-label={label} className='text-muted-foreground size-4' />
      )}
    </span>
  )
}
