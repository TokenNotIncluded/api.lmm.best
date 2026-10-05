/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldLabel } from '@/components/ui/field'
import { Textarea } from '@/components/ui/textarea'
import { useAuthStore } from '@/stores/auth-store'

import { marketAPI, type MarketReport } from './api'

export function ReportCallButton({ callID }: { callID: string }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [reason, setReason] = useState('')
  const id = useId()
  const report = useMutation({
    retry: false,
    mutationFn: () => marketAPI.report(callID, reason.trim()),
  })
  return (
    <>
      <Button type='button' variant='outline' onClick={() => setOpen(true)}>
        {t('Report this bill')}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('Report this bill')}</DialogTitle>
            <DialogDescription>
              {t(
                'Describe the suspected incorrect usage or charge. The bill and reported usage will be attached for review.'
              )}
            </DialogDescription>
          </DialogHeader>
          {report.isSuccess ? (
            <p role='status'>{t('Report submitted for review.')}</p>
          ) : (
            <form
              onSubmit={(event) => {
                event.preventDefault()
                if (reason.trim()) report.mutate()
              }}
            >
              <Field>
                <FieldLabel htmlFor={id}>{t('Report reason')}</FieldLabel>
                <Textarea
                  id={id}
                  required
                  maxLength={2000}
                  disabled={report.isPending}
                  value={reason}
                  onChange={(event) => setReason(event.target.value)}
                />
              </Field>
              {report.isError && (
                <p role='alert'>
                  {t('Could not submit report. Retry with the same reason.')}
                </p>
              )}
              <Button
                className='mt-3'
                type='submit'
                disabled={!reason.trim() || report.isPending}
              >
                {t('Submit report')}
              </Button>
            </form>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}

function ReportReview({ report }: { report: MarketReport }) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const [note, setNote] = useState('')
  const id = useId()
  const review = useMutation({
    retry: false,
    mutationFn: (confirmed: boolean) =>
      marketAPI.reviewReport(report.call_id, confirmed, note.trim()),
    onSuccess: () =>
      cache.invalidateQueries({ queryKey: ['tool-market-reports'] }),
  })
  return (
    <article className='space-y-3 border-b py-4'>
      <p className='font-mono text-xs break-all'>{report.call_id}</p>
      <p className='text-sm break-all'>
        {t('Provider account {{id}}', { id: report.owner_id })} ·{' '}
        {report.service_id}
      </p>
      <p>{report.reason}</p>
      <details>
        <summary>{t('Billing evidence')}</summary>
        <pre className='overflow-auto text-xs break-all whitespace-pre-wrap'>
          {report.evidence}
        </pre>
      </details>
      {report.status === 'pending' ? (
        <>
          <Field>
            <FieldLabel htmlFor={id}>{t('Review note')}</FieldLabel>
            <Textarea
              id={id}
              maxLength={2000}
              value={note}
              onChange={(event) => setNote(event.target.value)}
              disabled={review.isPending}
            />
          </Field>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Confirming a false bill suspends the service. Settled funds are not automatically refunded.'
            )}
          </p>
          <div className='flex flex-wrap gap-2'>
            <Button
              disabled={!note.trim() || review.isPending}
              onClick={() => review.mutate(true)}
            >
              {t('Confirm and suspend service')}
            </Button>
            <Button
              variant='outline'
              disabled={!note.trim() || review.isPending}
              onClick={() => review.mutate(false)}
            >
              {t('Dismiss report')}
            </Button>
          </div>
          {review.isError && (
            <p role='alert'>{t('Could not review report.')}</p>
          )}
        </>
      ) : (
        <p>
          {t(
            report.status === 'confirmed'
              ? 'Report confirmed'
              : 'Report dismissed'
          )}{' '}
          · {report.review_note}
        </p>
      )}
    </article>
  )
}

export function MarketReports() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [offset, setOffset] = useState(0)
  const reports = useQuery({
    queryKey: ['tool-market-reports', user?.id, offset],
    queryFn: () => marketAPI.reports(offset),
    enabled: (user?.role ?? 0) >= 10,
  })
  if ((user?.role ?? 0) < 10) return null
  return (
    <section className='space-y-3'>
      <h3 className='font-semibold'>{t('Billing reports')}</h3>
      {reports.isPending && <p>{t('Loading...')}</p>}
      {reports.isError && (
        <Button variant='outline' onClick={() => void reports.refetch()}>
          {t('Retry')}
        </Button>
      )}
      {reports.data?.length === 0 && <p>{t('No billing reports')}</p>}
      {reports.data?.map((report) => (
        <ReportReview key={report.call_id} report={report} />
      ))}
      <div className='flex gap-2'>
        <Button
          variant='outline'
          disabled={offset === 0 || reports.isPending}
          onClick={() => setOffset(Math.max(0, offset - 30))}
        >
          {t('Previous')}
        </Button>
        <Button
          variant='outline'
          disabled={reports.data?.length !== 30 || reports.isPending}
          onClick={() => setOffset(offset + 30)}
        >
          {t('Next')}
        </Button>
      </div>
    </section>
  )
}
