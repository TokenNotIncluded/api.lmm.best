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
import { Link } from '@tanstack/react-router'
import { ArrowRight, Coins, ReceiptText } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Button } from '@/components/ui/button'
import { ForgePublicShell } from '@/features/forge/forge-public-shell'
import { PurchaseJourney } from '@/features/forge/purchase-journey'
import { usePurchaseEntry } from '@/features/forge/use-purchase-entry'

const QUESTIONS = [
  [
    'Can I pay before approval?',
    'Developer access requires approval. Payment does not unlock access.',
  ],
  [
    'How much will I pay?',
    'Model rates and purchase options are available after approval. Your final payment depends on the amount, payment method and applicable discounts shown at checkout.',
  ],
  [
    'Is platform credit the same as money?',
    'Platform credit is your usage balance. The checkout shows the actual payment separately, with its settlement currency.',
  ],
  [
    'Should I choose credit or a subscription?',
    'Start with pay-as-you-go credit for flexible usage. If subscriptions are available, compare their included quota, expiry and reset rules before choosing.',
  ],
] as const

export function PublicAccessPricing() {
  const { t } = useTranslation()
  const entry = usePurchaseEntry()

  return (
    <ForgePublicShell>
      <main className='bg-background text-foreground'>
        <section className='border-border border-b'>
          <div className='mx-auto grid max-w-7xl gap-10 px-5 py-12 md:px-10 md:py-20 lg:grid-cols-[1.15fr_1fr] lg:items-center lg:gap-16'>
            <div>
              <p className='text-muted-foreground mb-5 text-sm font-medium'>
                {t('Pay as you go')}
              </p>
              <h1 className='max-w-2xl text-4xl leading-tight font-semibold tracking-tight md:text-5xl'>
                {t('Know what you are buying')}
              </h1>
              <p className='text-muted-foreground mt-6 max-w-xl text-base leading-7'>
                {t(
                  'Choose a model, understand the cost, then decide how much to spend.'
                )}
              </p>
              <div className='mt-8 flex flex-col gap-3 sm:flex-row'>
                <Button
                  size='lg'
                  className='h-auto min-h-12 px-6 py-3 text-base whitespace-normal'
                  render={
                    <Link
                      to={entry.to}
                      search={
                        entry.to === '/sign-in'
                          ? { redirect: '/wallet' }
                          : undefined
                      }
                    />
                  }
                >
                  {t(entry.label)}
                  <ArrowRight className='size-4' aria-hidden='true' />
                </Button>
                <Button
                  size='lg'
                  variant='outline'
                  className='h-auto min-h-12 px-6 py-3 text-base whitespace-normal'
                  render={<Link to='/guide' />}
                >
                  {t('Read the guide')}
                </Button>
              </div>
              <p className='text-muted-foreground mt-4 max-w-xl text-sm leading-6'>
                {t(
                  'Developer access requires approval. Payment does not unlock access.'
                )}
              </p>
            </div>

            <div className='border-border bg-card divide-border divide-y rounded-xl border px-6'>
              <div className='py-6'>
                <div className='mb-3 flex items-center gap-3'>
                  <Coins className='size-5' aria-hidden='true' />
                  <h2 className='text-lg font-semibold'>
                    {t('Pay as you go')}
                  </h2>
                </div>
                <p className='text-muted-foreground text-base leading-7'>
                  {t(
                    'Add a balance for flexible usage. Choose from the available amounts and review your quote before paying.'
                  )}
                </p>
              </div>
              <div className='py-6'>
                <div className='mb-3 flex items-center gap-3'>
                  <ReceiptText className='size-5' aria-hidden='true' />
                  <h2 className='text-lg font-semibold'>
                    {t('Subscriptions')}
                  </h2>
                </div>
                <p className='text-muted-foreground text-base leading-7'>
                  {t(
                    'When available, compare each plan by price, included quota, duration and reset rules.'
                  )}
                </p>
              </div>
              <p className='text-muted-foreground py-5 text-sm leading-6'>
                {t(
                  'Available models, plans and payment methods depend on your account. Check them after access approval.'
                )}
              </p>
            </div>
          </div>
        </section>

        <section
          className='border-border border-b'
          aria-labelledby='purchase-steps-title'
        >
          <div className='mx-auto max-w-7xl px-5 py-10 md:px-10 md:py-14'>
            <h2
              id='purchase-steps-title'
              className='mb-8 text-2xl font-semibold'
            >
              {t('A clear path to your first request')}
            </h2>
            <PurchaseJourney />
          </div>
        </section>

        <section
          className='mx-auto grid max-w-7xl gap-6 px-5 py-12 md:grid-cols-[1fr_2fr] md:gap-16 md:px-10 md:py-16'
          aria-labelledby='purchase-questions-title'
        >
          <h2 id='purchase-questions-title' className='text-2xl font-semibold'>
            {t('Before you pay')}
          </h2>
          <Accordion>
            {QUESTIONS.map(([question, answer]) => (
              <AccordionItem key={question} value={question}>
                <AccordionTrigger className='gap-4 py-5 text-base'>
                  {t(question)}
                </AccordionTrigger>
                <AccordionContent className='text-muted-foreground pb-5 text-base leading-7'>
                  {t(answer)}
                </AccordionContent>
              </AccordionItem>
            ))}
          </Accordion>
        </section>
      </main>
    </ForgePublicShell>
  )
}
