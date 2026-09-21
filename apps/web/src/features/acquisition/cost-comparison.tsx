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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'

import { costAmountMicros } from './cost-amount'

type Scope = {
  link_id: string
  from: number
  to: number
  observation_days: number
  currency: string
}
type Report = {
  scope: Scope
  spend: { amount_micros: number } | null
  registrations: number
  first_paying_accounts: number
  payment_rows_unclassified: number
  observing: boolean
  complete_after: number
  observed_until: number
  cost_per_registration_micros: number | null
  cost_per_first_payer_micros: number | null
}

function SpendEditor({
  scope,
  refresh,
}: {
  scope: Scope
  refresh: () => void
}) {
  const { t } = useTranslation()
  const [amount, setAmount] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(false)
  return (
    <form
      className='space-y-2'
      onSubmit={async (event) => {
        event.preventDefault()
        const micros = costAmountMicros(amount)
        if (micros == null) return
        setBusy(true)
        setError(false)
        try {
          const response = await api.put('/api/admin/acquisition/cost', {
            ...scope,
            amount_micros: micros,
          })
          if (!response.data.success) throw new Error()
          refresh()
          setAmount('')
        } catch {
          setError(true)
        } finally {
          setBusy(false)
        }
      }}
    >
      <Label htmlFor='campaign-spend'>
        {t('Spend for this exact cohort')} ({scope.currency})
      </Label>
      <Input
        id='campaign-spend'
        inputMode='decimal'
        value={amount}
        onChange={(event) => setAmount(event.target.value)}
        placeholder='0.00'
      />
      <Button type='submit' disabled={busy || costAmountMicros(amount) == null}>
        {t('Save spend')}
      </Button>
      {error && <p role='alert'>{t('Unable to save promotion spend.')}</p>}
    </form>
  )
}
export function AcquisitionCostComparison({
  id,
  canWrite,
}: {
  id: string
  canWrite: boolean
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [from, setFrom] = useState(() =>
    new Date(Date.now() - 30 * 86400000).toISOString().slice(0, 10)
  )
  const [to, setTo] = useState(() => new Date().toISOString().slice(0, 10))
  const [days, setDays] = useState(7)
  const [currency, setCurrency] = useState('USD')
  const scope: Scope = {
    link_id: id,
    from: Date.parse(`${from}T00:00:00Z`) / 1000,
    to: Date.parse(`${to}T00:00:00Z`) / 1000,
    observation_days: days,
    currency,
  }
  const valid =
    Number.isFinite(scope.from) &&
    Number.isFinite(scope.to) &&
    scope.from < scope.to &&
    /^[A-Z]{3}$/.test(currency) &&
    days >= 1 &&
    days <= 90
  const query = useQuery({
    queryKey: ['acquisition-cost', scope],
    enabled: open && valid,
    retry: false,
    queryFn: async () => {
      const params = new URLSearchParams(
        Object.entries(scope).map(([key, value]) => [key, String(value)])
      )
      const response = await api.get(`/api/admin/acquisition/cost?${params}`)
      if (!response.data.success) throw new Error()
      return response.data.data as Report
    },
  })
  const report = query.data
  const money = (micros: number | null) =>
    micros == null
      ? t('Not available')
      : `${currency} ${(micros / 1_000_000).toFixed(6)}`
  return (
    <>
      <Button variant='outline' onClick={() => setOpen(true)}>
        {t('Promotion cost')}
      </Button>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={t('Promotion cost comparison')}
        description={t(
          'Compare spend with attributed registrations for this promotion link only.'
        )}
        contentClassName='sm:max-w-xl'
      >
        <div className='space-y-4'>
          <div className='grid grid-cols-2 gap-3'>
            <div className='space-y-2'>
              <Label htmlFor='cost-from'>{t('Registration start (UTC)')}</Label>
              <Input
                id='cost-from'
                type='date'
                value={from}
                onChange={(event) => setFrom(event.target.value)}
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor='cost-to'>
                {t('Registration end, exclusive (UTC)')}
              </Label>
              <Input
                id='cost-to'
                type='date'
                value={to}
                onChange={(event) => setTo(event.target.value)}
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor='cost-days'>
                {t('Observation days after registration')}
              </Label>
              <Input
                id='cost-days'
                type='number'
                min={1}
                max={90}
                value={days}
                onChange={(event) => setDays(Number(event.target.value))}
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor='cost-currency'>{t('Currency')}</Label>
              <Input
                id='cost-currency'
                maxLength={3}
                value={currency}
                onChange={(event) =>
                  setCurrency(event.target.value.toUpperCase())
                }
              />
            </div>
          </div>
          {!valid ? (
            <p>{t('Select a valid cohort and currency.')}</p>
          ) : query.isPending ? (
            <p>{t('Loading')}</p>
          ) : query.isError ? (
            <div>
              <p>
                {t(
                  'Unable to load this cost comparison. Choose a cohort within the retained year.'
                )}
              </p>
              <Button variant='outline' onClick={() => void query.refetch()}>
                {t('Retry')}
              </Button>
            </div>
          ) : (
            report && (
              <>
                <dl className='grid grid-cols-2 gap-3 text-sm'>
                  {[
                    [
                      'Recorded spend',
                      report.spend
                        ? money(report.spend.amount_micros)
                        : t('Not provided'),
                    ],
                    ['Registrations', String(report.registrations)],
                    [
                      'First paying accounts',
                      String(report.first_paying_accounts),
                    ],
                    [
                      'Cost per registration',
                      money(report.cost_per_registration_micros),
                    ],
                    [
                      'Cost per first paying account',
                      money(report.cost_per_first_payer_micros),
                    ],
                  ].map(([label, value]) => (
                    <div key={label}>
                      <dt className='text-muted-foreground'>{t(label)}</dt>
                      <dd>{value}</dd>
                    </div>
                  ))}
                </dl>
                {report.observing && (
                  <p>
                    {t('Observing until {{date}}; payer cost is not final.', {
                      date: new Date(report.complete_after * 1000)
                        .toISOString()
                        .slice(0, 10),
                    })}
                  </p>
                )}
                {report.payment_rows_unclassified > 0 && (
                  <p>
                    {t(
                      'Some payment records lack settlement evidence. Payer cost is unavailable.'
                    )}
                  </p>
                )}
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Only retained, attributed accounts are counted. Successful cash payments count once per account; gifts and API usage are excluded. No currency conversion or profit calculation is performed. Overlapping cohorts must not be added together.'
                  )}
                </p>
                {canWrite && (
                  <SpendEditor
                    key={JSON.stringify(scope)}
                    scope={scope}
                    refresh={() => void query.refetch()}
                  />
                )}
              </>
            )
          )}
        </div>
      </Dialog>
    </>
  )
}
