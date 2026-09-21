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
import {
  Bug01Icon,
  ExternalLinkIcon,
  GiftIcon,
  Idea01Icon,
  PaintBrush01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemMedia,
  ItemTitle,
} from '@/components/ui/item'
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Separator } from '@/components/ui/separator'

import {
  getFeedbackIssueUrl,
  type FeedbackIssueCategory,
} from './feedback-issue-links'

export function FeedbackRewardButton() {
  const { t, i18n } = useTranslation()
  const language = i18n.resolvedLanguage || i18n.language || 'en'
  const options: {
    category: FeedbackIssueCategory
    title: string
    description: string
    icon: typeof PaintBrush01Icon
  }[] = [
    {
      category: 'frontend',
      title: t('Frontend improvement'),
      description: t('Improve interface, accessibility, or mobile usability.'),
      icon: PaintBrush01Icon,
    },
    {
      category: 'feature',
      title: t('Feature request'),
      description: t('Suggest a useful capability or workflow.'),
      icon: Idea01Icon,
    },
    {
      category: 'bug',
      title: t('Bug report'),
      description: t('Report a reproducible problem and its impact.'),
      icon: Bug01Icon,
    },
  ]

  return (
    <div className='pointer-events-none fixed right-[calc(1rem+env(safe-area-inset-right))] bottom-[calc(1rem+env(safe-area-inset-bottom))] z-30 print:hidden'>
      <Popover>
        <PopoverTrigger
          render={
            <Button
              variant='outline'
              size='lg'
              className='bg-background pointer-events-auto h-11 min-w-11 rounded-sm border-2'
              aria-label={t('Report & earn')}
            />
          }
        >
          <HugeiconsIcon icon={GiftIcon} data-icon='inline-start' />
          <span className='hidden sm:inline'>{t('Report & earn')}</span>
          <Badge variant='secondary'>5+ USD</Badge>
        </PopoverTrigger>
        <PopoverContent
          side='top'
          align='end'
          sideOffset={10}
          collisionPadding={16}
          className='pointer-events-auto max-h-[calc(100dvh_-_6.5rem_-_env(safe-area-inset-top)_-_env(safe-area-inset-bottom))] w-[calc(100vw_-_2rem_-_env(safe-area-inset-left)_-_env(safe-area-inset-right))] max-w-sm gap-0 overflow-y-auto overscroll-contain rounded-sm p-0'
        >
          <PopoverHeader className='gap-1 p-4'>
            <PopoverTitle>{t('Feedback rewards')}</PopoverTitle>
            <PopoverDescription className='leading-relaxed'>
              {t(
                'Valid reports earn at least 5 USD after review. Submission does not guarantee a reward.'
              )}
            </PopoverDescription>
          </PopoverHeader>
          <Separator />
          <ItemGroup className='gap-1 p-2'>
            {options.map((option) => (
              <Item
                key={option.category}
                role='listitem'
                variant='default'
                render={
                  <a
                    href={getFeedbackIssueUrl(option.category, language)}
                    target='_blank'
                    rel='noopener noreferrer'
                  />
                }
              >
                <ItemMedia variant='icon'>
                  <HugeiconsIcon icon={option.icon} />
                </ItemMedia>
                <ItemContent>
                  <ItemTitle>{option.title}</ItemTitle>
                  <ItemDescription>{option.description}</ItemDescription>
                </ItemContent>
                <ItemActions aria-hidden='true'>
                  <HugeiconsIcon icon={ExternalLinkIcon} />
                </ItemActions>
              </Item>
            ))}
          </ItemGroup>
        </PopoverContent>
      </Popover>
    </div>
  )
}
