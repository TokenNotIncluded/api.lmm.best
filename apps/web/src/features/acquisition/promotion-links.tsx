/*
Copyright (C) 2026 LIghtJUNction
*/
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { QRCodeSVG } from 'qrcode.react'
import { type FormEvent, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { api } from '@/lib/api'

import { AcquisitionCostComparison } from './cost-comparison'
import { AcquisitionLinkPreview } from './link-preview'

const PAGE_SIZE = 20
const emptyForm = {
  name: '',
  source: '',
  medium: '',
  campaign: '',
  content: '',
  target: '/',
}
type LinkStatus = 'all' | 'active' | 'archived' | 'deleted'
type LinkRecord = typeof emptyForm & {
  id: string
  archived: boolean
  created_at: number
  deleted_at?: number
}
type LinkPage = { items: LinkRecord[]; total: number; page: number }

function promotionURL(link: LinkRecord, origin: string) {
  const url = new URL(link.target, origin)
  url.searchParams.set('lmm_source', link.id)
  for (const [key, value] of Object.entries({
    utm_source: link.source,
    utm_medium: link.medium,
    utm_campaign: link.campaign,
    utm_content: link.content,
  })) {
    if (value) url.searchParams.set(key, value)
  }
  return url.href
}

export function PromotionLinks({ canWrite }: { canWrite: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const searchId = useId()
  const { copiedText, copyToClipboard } = useCopyToClipboard()
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState<LinkStatus>('all')
  const [searchDraft, setSearchDraft] = useState('')
  const [search, setSearch] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const [form, setForm] = useState(emptyForm)
  const [creating, setCreating] = useState(false)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [renameId, setRenameId] = useState<string | null>(null)
  const [qrId, setQrId] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [deleteTarget, setDeleteTarget] = useState<LinkRecord | null>(null)
  const [deleteError, setDeleteError] = useState('')

  const links = useQuery({
    queryKey: ['acquisition-links', page, status, search],
    queryFn: async () => {
      const params = new URLSearchParams({
        page: String(page),
        page_size: String(PAGE_SIZE),
        status,
      })
      if (search) params.set('q', search)
      const response = await api.get(`/api/admin/acquisition/links?${params}`)
      if (!response.data.success) throw new Error(response.data.message)
      return response.data.data as LinkPage
    },
    retry: false,
  })

  const createLink = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (creating) return
    setCreating(true)
    setError('')
    try {
      const response = await api.post('/api/admin/acquisition/links', form)
      if (!response.data.success) throw new Error(response.data.message)
      setForm(emptyForm)
      setCreateOpen(false)
      setPage(1)
      setStatus('all')
      setSearch('')
      setSearchDraft('')
      await queryClient.invalidateQueries({ queryKey: ['acquisition-links'] })
      toast.success(t('Promotion link created.'))
    } catch {
      setError(t('Unable to save promotion link'))
    } finally {
      setCreating(false)
    }
  }

  const updateLink = async (
    link: LinkRecord,
    changes: { name?: string; archived?: boolean }
  ) => {
    if (busyId) return
    setBusyId(link.id)
    setError('')
    try {
      const response = await api.post('/api/admin/acquisition/links', {
        id: link.id,
        name: changes.name ?? link.name,
        archived: changes.archived ?? link.archived,
      })
      if (!response.data.success) throw new Error(response.data.message)
      setRenameId(null)
      if (
        changes.archived !== undefined &&
        page > 1 &&
        links.data?.items.length === 1 &&
        (status === 'active' || status === 'archived')
      ) {
        setPage(page - 1)
      }
      await queryClient.invalidateQueries({ queryKey: ['acquisition-links'] })
    } catch {
      setError(t('Unable to save promotion link'))
    } finally {
      setBusyId(null)
    }
  }

  const deleteLink = async () => {
    if (!deleteTarget || busyId) return
    const target = deleteTarget
    setBusyId(target.id)
    setDeleteError('')
    try {
      const response = await api.delete(
        `/api/admin/acquisition/links/${encodeURIComponent(target.id)}`
      )
      if (!response.data.success) throw new Error(response.data.message)
      setDeleteTarget(null)
      setQrId(null)
      setRenameId(null)
      if (links.data?.items.length === 1 && page > 1) setPage(page - 1)
      await queryClient.invalidateQueries({ queryKey: ['acquisition-links'] })
      toast.success(t('Promotion link deleted.'))
    } catch {
      setDeleteError(t('Unable to delete promotion link.'))
    } finally {
      setBusyId(null)
    }
  }

  const applySearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPage(1)
    setSearch(searchDraft.trim())
  }

  const pageCount = Math.max(1, Math.ceil((links.data?.total ?? 0) / PAGE_SIZE))

  return (
    <section id='promotion-links' className='space-y-5 border-b pb-8'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='space-y-1'>
          <h2 className='text-lg font-semibold'>{t('Promotion links')}</h2>
          <p className='text-muted-foreground max-w-2xl text-sm'>
            {t(
              'Each link keeps a stable identity. Create a new link for a different campaign; renaming or archiving does not rewrite past attribution.'
            )}
          </p>
        </div>
        {canWrite && (
          <Button
            type='button'
            variant={createOpen ? 'outline' : 'default'}
            onClick={() => {
              setCreateOpen(!createOpen)
              setError('')
            }}
            aria-expanded={createOpen}
            aria-controls='acquisition-create-link'
          >
            {t(createOpen ? 'Cancel' : 'Create promotion link')}
          </Button>
        )}
      </div>

      {canWrite && createOpen && (
        <form
          id='acquisition-create-link'
          onSubmit={(event) => void createLink(event)}
          className='bg-muted/30 grid gap-3 rounded-xl border p-4 sm:grid-cols-2 lg:grid-cols-3'
        >
          {(['name', 'source', 'medium', 'campaign', 'content'] as const).map(
            (field, index) => (
              <label key={field} className='min-w-0 space-y-1 text-sm'>
                <span>
                  {t(
                    [
                      'Display name',
                      'Source platform',
                      'Promotion method',
                      'Campaign',
                      'Content label',
                    ][index] ?? 'Content label'
                  )}
                </span>
                <Input
                  required={field === 'name' || field === 'source'}
                  maxLength={80}
                  value={form[field]}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      [field]: event.target.value,
                    }))
                  }
                />
              </label>
            )
          )}
          <label className='min-w-0 space-y-1 text-sm'>
            <span>{t('Target page')}</span>
            <NativeSelect
              className='border-input h-9 w-full rounded-md border bg-transparent px-3'
              value={form.target}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  target: event.target.value,
                }))
              }
            >
              {[
                '/',
                '/guide',
                '/pricing',
                '/challenges',
                '/sign-up',
                '/sign-in',
              ].map((path) => (
                <NativeSelectOption key={path} value={path}>
                  {path}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </label>
          <div className='flex items-end sm:col-span-2 lg:col-span-3'>
            <Button type='submit' disabled={creating}>
              {t('Create promotion link')}
            </Button>
          </div>
        </form>
      )}

      <div className='grid gap-3 sm:grid-cols-[minmax(0,1fr)_10rem] sm:items-end'>
        <form
          onSubmit={applySearch}
          className='grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-end gap-2 sm:flex sm:flex-wrap'
        >
          <label
            htmlFor={searchId}
            className='col-span-2 min-w-0 flex-1 space-y-1 text-sm sm:min-w-48'
          >
            <span>{t('Search promotion links')}</span>
            <Input
              id={searchId}
              value={searchDraft}
              maxLength={80}
              placeholder={t('Name, source, or campaign')}
              className='border-border bg-background rounded-lg'
              onChange={(event) => setSearchDraft(event.target.value)}
            />
          </label>
          <Button type='submit' variant='outline' className='w-full sm:w-auto'>
            {t('Search')}
          </Button>
          {search && (
            <Button
              type='button'
              variant='ghost'
              onClick={() => {
                setSearchDraft('')
                setSearch('')
                setPage(1)
              }}
            >
              {t('Clear')}
            </Button>
          )}
        </form>
        <label className='w-full space-y-1 text-sm'>
          <span>{t('Filter links')}</span>
          <NativeSelect
            className='border-border bg-background h-9 w-full rounded-lg border px-3'
            value={status}
            onChange={(event) => {
              setStatus(event.target.value as LinkStatus)
              setPage(1)
            }}
          >
            {(['all', 'active', 'archived', 'deleted'] as const).map(
              (value) => (
                <NativeSelectOption key={value} value={value}>
                  {t(
                    value === 'all'
                      ? 'All'
                      : value === 'active'
                        ? 'Active'
                        : value === 'archived'
                          ? 'Archived'
                          : 'Deleted'
                  )}
                </NativeSelectOption>
              )
            )}
          </NativeSelect>
        </label>
      </div>

      {error && (
        <p role='alert' className='text-destructive text-sm'>
          {error}
        </p>
      )}
      {status === 'deleted' && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Deleted links stay in historical reports, but their tracking IDs no longer record new visits.'
          )}
        </p>
      )}

      {links.isError ? (
        <div role='alert' className='space-y-2 text-sm'>
          <p>{t('Unable to load promotion links.')}</p>
          <Button variant='outline' onClick={() => void links.refetch()}>
            {t('Retry')}
          </Button>
        </div>
      ) : links.isPending ? (
        <div className='space-y-3' aria-label={t('Loading')}>
          <Skeleton className='h-24 w-full' />
          <Skeleton className='h-24 w-full' />
        </div>
      ) : links.data.items.length === 0 ? (
        <p className='text-muted-foreground rounded-xl border border-dashed p-6 text-sm'>
          {search || status !== 'all'
            ? t('No matching promotion links.')
            : t('No promotion links yet.')}
        </p>
      ) : (
        <div className='divide-y rounded-xl border'>
          {links.data.items.map((link) => {
            const deleted = Boolean(link.deleted_at)
            const url = promotionURL(link, window.location.origin)
            return (
              <article key={link.id} className='min-w-0 space-y-3 p-4'>
                <div className='flex flex-wrap items-start justify-between gap-2'>
                  <div className='min-w-0'>
                    <h3 className='[font-family:var(--font-body)] text-base font-semibold break-words'>
                      {link.name}
                    </h3>
                    <p className='text-muted-foreground text-xs break-words'>
                      {[link.source, link.medium, link.campaign, link.content]
                        .filter(Boolean)
                        .join(' · ')}
                      {link.created_at > 0 && (
                        <>
                          {' '}
                          · {t('Created')}{' '}
                          {new Date(
                            link.created_at * 1000
                          ).toLocaleDateString()}
                        </>
                      )}
                    </p>
                  </div>
                  <Badge
                    variant={
                      deleted
                        ? 'destructive'
                        : link.archived
                          ? 'secondary'
                          : 'outline'
                    }
                  >
                    {t(
                      deleted
                        ? 'Deleted'
                        : link.archived
                          ? 'Archived'
                          : 'Active'
                    )}
                  </Badge>
                </div>

                {!deleted && (
                  <div className='grid min-w-0 gap-2 sm:grid-cols-[minmax(0,1fr)_auto]'>
                    <Input
                      readOnly
                      value={url}
                      aria-label={t('Promotion link URL')}
                      className='border-border bg-background min-w-0 rounded-lg font-mono text-xs'
                      onFocus={(event) => event.currentTarget.select()}
                      onClick={(event) => event.currentTarget.select()}
                    />
                    <Button
                      type='button'
                      variant='outline'
                      onClick={() => void copyToClipboard(url)}
                    >
                      {t(copiedText === url ? 'Copied' : 'Copy link')}
                    </Button>
                  </div>
                )}

                <div className='flex flex-wrap items-center gap-2'>
                  {!deleted && (
                    <>
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        aria-expanded={qrId === link.id}
                        onClick={() =>
                          setQrId(qrId === link.id ? null : link.id)
                        }
                      >
                        {t('QR code')}
                      </Button>
                      <AcquisitionLinkPreview id={link.id} />
                    </>
                  )}
                  <AcquisitionCostComparison id={link.id} canWrite={canWrite} />
                  {canWrite && !deleted && (
                    <>
                      <Button
                        type='button'
                        size='sm'
                        variant='ghost'
                        disabled={busyId === link.id}
                        onClick={() =>
                          setRenameId(renameId === link.id ? null : link.id)
                        }
                      >
                        {t('Rename')}
                      </Button>
                      <Button
                        type='button'
                        size='sm'
                        variant='ghost'
                        disabled={busyId === link.id}
                        onClick={() =>
                          void updateLink(link, { archived: !link.archived })
                        }
                      >
                        {t(link.archived ? 'Restore' : 'Archive')}
                      </Button>
                      <Button
                        type='button'
                        size='sm'
                        variant='ghost'
                        className='text-destructive hover:text-destructive'
                        disabled={busyId === link.id}
                        onClick={() => {
                          setDeleteError('')
                          setDeleteTarget(link)
                        }}
                      >
                        {t('Delete')}
                      </Button>
                    </>
                  )}
                </div>

                {renameId === link.id && canWrite && !deleted && (
                  <form
                    className='flex flex-wrap gap-2'
                    onSubmit={(event) => {
                      event.preventDefault()
                      const name = new FormData(event.currentTarget).get('name')
                      if (typeof name === 'string') {
                        void updateLink(link, { name })
                      }
                    }}
                  >
                    <Input
                      name='name'
                      required
                      maxLength={80}
                      defaultValue={link.name}
                      aria-label={t('Display name')}
                      className='min-w-48 flex-1'
                    />
                    <Button type='submit' disabled={busyId === link.id}>
                      {t('Save')}
                    </Button>
                  </form>
                )}
                {qrId === link.id && !deleted && (
                  <div className='w-fit rounded-lg border bg-white p-3'>
                    <QRCodeSVG value={url} size={160} />
                  </div>
                )}
              </article>
            )
          })}
        </div>
      )}

      {!links.isPending && !links.isError && links.data.total > PAGE_SIZE && (
        <nav
          className='flex items-center justify-end gap-3'
          aria-label={t('Promotion links')}
        >
          <Button
            type='button'
            variant='outline'
            disabled={page <= 1}
            onClick={() => setPage(page - 1)}
          >
            {t('Previous')}
          </Button>
          <span className='text-muted-foreground text-sm tabular-nums'>
            {t('Page')} {page} / {pageCount}
          </span>
          <Button
            type='button'
            variant='outline'
            disabled={page >= pageCount}
            onClick={() => setPage(page + 1)}
          >
            {t('Next')}
          </Button>
        </nav>
      )}

      <AlertDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open && !busyId) setDeleteTarget(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Delete promotion link')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'Delete "{{name}}"? Its tracking ID will stop working. Historical attribution and spend stay in reports. This cannot be undone.',
                { name: deleteTarget?.name ?? '' }
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {deleteError && (
            <p role='alert' className='text-destructive text-sm'>
              {deleteError}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={Boolean(busyId)}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              variant='destructive'
              disabled={Boolean(busyId)}
              onClick={() => void deleteLink()}
            >
              {busyId ? t('Deleting...') : t('Delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}
