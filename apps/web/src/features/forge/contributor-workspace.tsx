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
  ArrowDown01Icon,
  CheckmarkCircle01Icon,
  GitPullRequestIcon,
  ShieldCheck,
  Wallet01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Link } from '@tanstack/react-router'
import type { ComponentProps } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'

import { AcceptedChallengeList } from './accepted-challenge-list'
import { ChallengeList } from './challenge-list'

type IconType = ComponentProps<typeof HugeiconsIcon>['icon']

function Step(props: { icon: IconType; step: string; label: string }) {
  return (
    <li className='border-border/60 bg-card hover:border-primary/40 flex items-center gap-3 rounded-lg border px-3 py-2.5 transition-colors duration-200 motion-reduce:transition-none'>
      <span className='bg-primary/10 text-primary flex size-7 shrink-0 items-center justify-center rounded-md'>
        <HugeiconsIcon
          icon={props.icon}
          strokeWidth={2}
          aria-hidden='true'
          className='size-4'
        />
      </span>
      <span className='min-w-0'>
        <span className='text-muted-foreground block text-[10px] font-semibold tracking-wide uppercase'>
          {props.step}
        </span>
        <span className='block truncate text-sm font-medium'>
          {props.label}
        </span>
      </span>
    </li>
  )
}

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
        <div className='mx-auto w-full max-w-6xl space-y-8'>
          <section className='border-border/60 bg-card grid gap-6 rounded-xl border p-4 shadow-sm sm:p-6 md:grid-cols-[minmax(0,1fr)_300px]'>
            <div className='min-w-0'>
              <h2 className='text-xl font-semibold tracking-tight sm:text-2xl'>
                {t('Pick funded work and show your trail.')}
              </h2>
              <p className='text-muted-foreground mt-2 max-w-2xl text-sm leading-6'>
                {t(
                  'Accept a challenge, link the issue and pull request, then follow review and payout in one timeline.'
                )}
              </p>
              <ol className='mt-5 grid gap-2 sm:grid-cols-3'>
                <Step
                  icon={CheckmarkCircle01Icon}
                  step={t('Step 1')}
                  label={t('Accept open work')}
                />
                <Step
                  icon={GitPullRequestIcon}
                  step={t('Step 2')}
                  label={t('Submit evidence')}
                />
                <Step
                  icon={Wallet01Icon}
                  step={t('Step 3')}
                  label={t('Get paid to balance')}
                />
              </ol>
              <div className='mt-5 flex flex-wrap gap-2'>
                <Button
                  type='button'
                  onClick={() =>
                    document.getElementById('open-work')?.scrollIntoView({
                      // The global reduced-motion rule only zeroes transition
                      // duration, so the scroll behaviour is chosen explicitly.
                      behavior: window.matchMedia(
                        '(prefers-reduced-motion: reduce)'
                      ).matches
                        ? 'auto'
                        : 'smooth',
                      block: 'start',
                    })
                  }
                >
                  {t('Browse open work')}
                  <HugeiconsIcon
                    icon={ArrowDown01Icon}
                    data-icon='inline-end'
                    strokeWidth={2}
                    aria-hidden='true'
                  />
                </Button>
              </div>
            </div>
            <aside className='border-border/60 bg-muted/20 min-w-0 rounded-lg border p-4'>
              <div className='flex items-center gap-2'>
                <HugeiconsIcon
                  icon={ShieldCheck}
                  strokeWidth={2}
                  aria-hidden='true'
                  className='text-primary size-5 shrink-0'
                />
                <h3 className='font-medium'>{t('Build trust')}</h3>
              </div>
              <p className='text-muted-foreground mt-2 text-sm leading-6'>
                {t(
                  'Finished deliveries raise your rating. Higher trust unlocks better-paying work.'
                )}
              </p>
              <Button
                variant='outline'
                size='sm'
                className='mt-3 w-full'
                render={<Link to='/wallet' />}
              >
                {t('View trust level')}
              </Button>
            </aside>
          </section>

          <div id='open-work' className='scroll-mt-4'>
            <ChallengeList limit={12} />
          </div>
          <AcceptedChallengeList />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
