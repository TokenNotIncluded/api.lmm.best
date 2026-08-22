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
  ArrowRight01Icon,
  ShieldCheck,
  Wallet01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'

import { AcceptedChallengeList } from './accepted-challenge-list'
import { ChallengeList } from './challenge-list'

export function ContributorWorkspace() {
  const { t } = useTranslation()

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Contributor workspace')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button variant='outline' render={<Link to='/wallet' />}>
          <HugeiconsIcon
            icon={Wallet01Icon}
            data-icon='inline-start'
            strokeWidth={2}
            aria-hidden='true'
          />
          {t('Wallet')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='border-foreground/25 bg-background text-foreground mx-auto max-w-6xl overflow-hidden border'>
          <section className='border-foreground bg-accent grid gap-8 border-b px-6 py-9 md:grid-cols-[1fr_280px] md:px-10 md:py-12'>
            <div>
              <p className='mb-4 text-xs font-bold uppercase'>
                {t('Delivery workspace')}
              </p>
              <h1 className='mb-4 max-w-2xl font-serif text-4xl leading-tight font-normal md:text-5xl'>
                {t('Choose funded work and make progress visible.')}
              </h1>
              <p className='max-w-2xl text-sm leading-6 md:text-base'>
                {t(
                  'Accept a challenge, link the issue and pull request, then follow review and settlement in one evidence trail.'
                )}
              </p>
            </div>
            <div className='border-foreground/40 flex flex-col justify-end border-t pt-5 md:border-t-0 md:border-l md:pt-0 md:pl-7'>
              <HugeiconsIcon
                icon={ShieldCheck}
                className='mb-5 size-7'
                strokeWidth={2}
                aria-hidden='true'
              />
              <p className='mb-5 text-sm leading-6'>
                {t(
                  'Build account trust to unlock more workspace tools and better rates.'
                )}
              </p>
              <Button
                className='bg-foreground text-background hover:bg-foreground/85 w-full rounded-sm'
                render={<Link to='/wallet' />}
              >
                {t('View trust level')}
                <HugeiconsIcon
                  icon={ArrowRight01Icon}
                  data-icon='inline-end'
                  strokeWidth={2}
                  aria-hidden='true'
                />
              </Button>
            </div>
          </section>
          <div className='px-6 py-9 md:px-10 md:py-12'>
            <ChallengeList limit={12} />
            <AcceptedChallengeList />
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
