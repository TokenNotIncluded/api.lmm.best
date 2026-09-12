/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { Archive, Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { formatTimestamp } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  listDeveloperAccessRecommendationArchives,
  type DeveloperAccessRecommendationArchive,
} from '../api'
import type { User } from '../types'

export function UserRecommendationArchiveDialog(props: { user: User }) {
  const { t } = useTranslation()
  const viewer = useAuthStore((state) => state.auth.user)
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [archives, setArchives] = useState<
    DeveloperAccessRecommendationArchive[]
  >([])

  const canView =
    viewer !== null &&
    viewer !== undefined &&
    viewer.role >= ROLE.ADMIN &&
    viewer.role > props.user.role
  if (!canView) return null

  const load = async () => {
    setLoading(true)
    try {
      const response = await listDeveloperAccessRecommendationArchives(
        props.user.id
      )
      if (!response.success) {
        throw new Error(response.message || t('Unable to load archive'))
      }
      setArchives(response.data ?? [])
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Unable to load archive')
      )
    } finally {
      setLoading(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Button
        type='button'
        variant='ghost'
        size='icon-sm'
        className='size-11 sm:size-7'
        aria-label={t('View L1 recommendation archive')}
        onClick={() => {
          setOpen(true)
          void load()
        }}
      >
        <Archive aria-hidden='true' />
      </Button>
      <DialogContent className='max-h-[min(44rem,calc(100svh-2rem))] sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('L1 recommendation archive')}</DialogTitle>
          <DialogDescription>
            {props.user.username} · {t('User ID')}: {props.user.id}
          </DialogDescription>
        </DialogHeader>
        <div className='max-h-[60vh] overflow-y-auto pr-1'>
          {loading ? (
            <div className='text-muted-foreground flex items-center gap-2 py-8 text-sm'>
              <Loader2 className='animate-spin' size={16} />
              {t('Loading...')}
            </div>
          ) : archives.length === 0 ? (
            <p className='text-muted-foreground py-8 text-sm'>
              {t('No approved recommendation archive yet.')}
            </p>
          ) : (
            <div className='divide-border divide-y'>
              {archives.map((archive) => (
                <article key={archive.id} className='space-y-3 py-5 first:pt-1'>
                  <div className='text-muted-foreground flex flex-wrap justify-between gap-2 text-xs'>
                    <span>
                      {t('Approved')} · {formatTimestamp(archive.approved_at)}
                    </span>
                    <span>
                      {t('Request')} #{archive.request_id}
                    </span>
                  </div>
                  <p className='text-sm whitespace-pre-wrap'>
                    {archive.recommendation}
                  </p>
                  {archive.admin_note ? (
                    <p className='text-muted-foreground text-sm whitespace-pre-wrap'>
                      {t('Administrator note')}: {archive.admin_note}
                    </p>
                  ) : null}
                </article>
              ))}
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
