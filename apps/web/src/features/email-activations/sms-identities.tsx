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
import {
  DiscordIcon,
  Facebook02Icon,
  FigmaIcon,
  GithubIcon,
  GitlabIcon,
  GoogleIcon,
  MediumIcon,
  Notion02Icon,
  SkypeIcon,
  SlackIcon,
  StripeIcon,
  TelegramIcon,
  TrelloIcon,
  WechatIcon,
  WhatsappIcon,
  ZoomIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon, type IconSvgElement } from '@hugeicons/react'
import OpenAI from '@lobehub/icons/es/OpenAI/components/Mono.js'
import { Globe2 } from 'lucide-react'
import type { ComponentType } from 'react'

import { cn } from '@/lib/utils'

import type { HeroSmsSmsCountry, HeroSmsSmsService } from './sms-api.js'
import {
  getHeroSmsCountryFlag,
  getHeroSmsCountryName,
} from './sms-selection.js'

type BrandIcon =
  | { kind: 'hugeicons'; value: IconSvgElement }
  | {
      kind: 'component'
      value: ComponentType<{ className?: string }>
    }

const hugeicon = (value: IconSvgElement): BrandIcon => ({
  kind: 'hugeicons',
  value,
})

const serviceIconRules: Array<{
  codes: string[]
  names: string[]
  icon: BrandIcon
}> = [
  { codes: ['tg'], names: ['telegram'], icon: hugeicon(TelegramIcon) },
  { codes: ['wa'], names: ['whatsapp'], icon: hugeicon(WhatsappIcon) },
  { codes: ['go'], names: ['google'], icon: hugeicon(GoogleIcon) },
  {
    codes: ['dr'],
    names: ['openai', 'chatgpt'],
    icon: { kind: 'component', value: OpenAI },
  },
  { codes: [], names: ['gmail'], icon: hugeicon(GoogleIcon) },
  { codes: ['fb'], names: ['facebook'], icon: hugeicon(Facebook02Icon) },
  { codes: ['ds'], names: ['discord'], icon: hugeicon(DiscordIcon) },
  { codes: ['wb'], names: ['wechat', 'weixin'], icon: hugeicon(WechatIcon) },
  { codes: ['sk'], names: ['skype'], icon: hugeicon(SkypeIcon) },
  { codes: [], names: ['slack'], icon: hugeicon(SlackIcon) },
  { codes: ['gh'], names: ['github'], icon: hugeicon(GithubIcon) },
  { codes: [], names: ['gitlab'], icon: hugeicon(GitlabIcon) },
  { codes: [], names: ['zoom'], icon: hugeicon(ZoomIcon) },
  { codes: [], names: ['notion'], icon: hugeicon(Notion02Icon) },
  { codes: [], names: ['medium'], icon: hugeicon(MediumIcon) },
  { codes: [], names: ['trello'], icon: hugeicon(TrelloIcon) },
  { codes: [], names: ['stripe'], icon: hugeicon(StripeIcon) },
  { codes: [], names: ['figma'], icon: hugeicon(FigmaIcon) },
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
  const brandIcon = getServiceBrandIcon(service)
  const ComponentIcon =
    brandIcon?.kind === 'component' ? brandIcon.value : undefined
  const fallback = (service.name || service.code)
    .trim()
    .slice(0, 2)
    .toUpperCase()
  let content = (
    <span className='text-[10px] font-semibold tracking-tight'>{fallback}</span>
  )
  if (brandIcon?.kind === 'hugeicons') {
    content = <HugeiconsIcon icon={brandIcon.value} className='size-4' />
  } else if (ComponentIcon) {
    content = <ComponentIcon className='size-4' />
  }
  return (
    <span
      className={cn(
        'bg-muted/60 text-foreground flex size-8 items-center justify-center rounded-lg border',
        className
      )}
      aria-hidden='true'
    >
      {content}
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
