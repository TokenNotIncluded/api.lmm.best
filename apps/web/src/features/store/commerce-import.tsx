/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from '@/components/ui/empty'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Separator } from '@/components/ui/separator'
import { useAuthStore } from '@/stores/auth-store'

import {
  commerceImportApi,
  CommerceImportAPIError,
} from './commerce-import-api'
import { STORE_COMMERCE_IMPORT_COPY as copy } from './commerce-import-copy'
import { StoreCommerceImportPreview } from './commerce-import-preview'
import type {
  CommerceImportAuthScope,
  CommerceImportConfig,
  CommerceImportConnection,
  CommerceImportRequest,
} from './commerce-import-types'
import {
  commerceImportAuthorizationUrl,
  commerceImportText,
  consumeCommerceImportCallback,
} from './commerce-import-utils'
import { StoreLoading } from './shared'

const connectionStatus = {
  pending: copy.pendingConnection,
  active: copy.activeConnection,
  reauthorize: copy.reauthorizeConnection,
  disconnected: copy.disconnectedConnection,
} as const
const requestStatus = {
  pending: copy.pendingRequest,
  received: copy.receivedRequest,
  imported: copy.importedRequest,
  manual_recovery: copy.manualRequest,
} as const
function canRecoverRequest(request: CommerceImportRequest) {
  return (
    ['pending', 'received'].includes(request.status) &&
    !(
      typeof request.recovery_expires === 'number' &&
      request.recovery_expires > 0 &&
      request.recovery_expires <= Date.now() / 1000
    )
  )
}

function authIsCurrent(authScope: CommerceImportAuthScope) {
  const { auth } = useAuthStore.getState()
  return (
    auth.user?.id === authScope.userId &&
    auth.session?.sid === authScope.sessionId
  )
}
function importKey(authScope: CommerceImportAuthScope) {
  return [
    'store',
    'commerce-import',
    authScope.userId,
    authScope.sessionId,
  ] as const
}

export function StoreCommerceImport() {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const authScope = { userId, sessionId }
  const client = useQueryClient()
  const [open, setOpen] = useState(false)
  const [callback, setCallback] = useState<string | null>(null)
  const config = useQuery({
    queryKey: [...importKey(authScope), 'config'],
    queryFn: ({ signal }) => commerceImportApi.config(authScope, signal),
    enabled: userId !== undefined,
    retry: false,
    gcTime: 0,
  })
  useEffect(() => {
    const result = consumeCommerceImportCallback()
    if (!result) return
    setCallback(result)
    setOpen(true)
    void client.invalidateQueries({
      queryKey: importKey({ userId, sessionId }),
    })
  }, [client, userId, sessionId])
  if (config.data?.enabled !== true) return null
  return (
    <>
      <Button variant='outline' onClick={() => setOpen(true)}>
        {t(copy.title)}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='max-h-[90svh] max-w-5xl overflow-y-auto'>
          <DialogHeader>
            <DialogTitle>{t(copy.title)}</DialogTitle>
            <DialogDescription>{t(copy.description)}</DialogDescription>
          </DialogHeader>
          {callback && (
            <Alert>
              <AlertDescription>
                {t(
                  callback === 'connected'
                    ? copy.connected
                    : callback === 'denied'
                      ? copy.denied
                      : copy.failed
                )}
              </AlertDescription>
            </Alert>
          )}
          {open && (
            <StoreCommerceImportManager
              key={`${userId}:${sessionId}`}
              authScope={authScope}
              config={config.data}
            />
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}

export function StoreCommerceImportManager({
  authScope,
  config,
  navigate = (url) => window.location.assign(url),
}: {
  authScope: CommerceImportAuthScope
  config: CommerceImportConfig
  navigate?: (url: string) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const client = useQueryClient()
  const key = importKey(authScope)
  const [origin, setOrigin] = useState('')
  const [clientId, setClientId] = useState('')
  const [connectionId, setConnectionId] = useState('')
  const [productId, setProductId] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error | null>(null)
  const [saved, setSaved] = useState(false)
  const [disconnecting, setDisconnecting] = useState(false)
  const [uncertainConnections, setUncertainConnections] = useState<
    Record<string, boolean>
  >({})
  const inFlight = useRef(false)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const connections = useQuery({
    queryKey: [...key, 'connections'],
    queryFn: ({ signal }) => commerceImportApi.connections(authScope, signal),
    enabled: config.enabled,
    retry: false,
    gcTime: 0,
  })
  const connection = connections.data?.find((item) => item.id === connectionId)
  const canRead =
    connection?.status === 'active' &&
    connection.scope.split(/\s+/).includes('products.read')
  const catalog = useQuery({
    queryKey: [...key, connectionId, 'catalog'],
    queryFn: ({ signal }) =>
      commerceImportApi.catalog(connectionId, authScope, signal),
    enabled: config.enabled && !!canRead,
    retry: false,
    gcTime: 0,
  })
  const requests = useQuery({
    queryKey: [...key, connectionId, 'requests'],
    queryFn: ({ signal }) =>
      commerceImportApi.requests(connectionId, authScope, signal),
    enabled: config.enabled && !!connection,
    retry: false,
    gcTime: 0,
  })
  const listing = catalog.data?.products.find((item) => item.id === productId)
  const mapping = catalog.data?.mappings?.find(
    (item) => item.external_product_id === productId
  )
  const unresolved =
    requests.data?.some(
      (request) =>
        request.status !== 'imported' &&
        !(
          request.status === 'manual_recovery' &&
          request.issuance_uncertain === false
        )
    ) === true
  const uncertain =
    uncertainConnections[connectionId] === true ||
    unresolved ||
    !requests.isSuccess
  const current = () => mounted.current && authIsCurrent(authScope)
  async function refresh() {
    if (inFlight.current || !current()) return
    const results = await Promise.all([
      connections.refetch(),
      ...(canRead ? [catalog.refetch()] : []),
      ...(connection ? [requests.refetch()] : []),
    ])
    if (current() && results.every((result) => !result.error)) setError(null)
  }
  async function mutate(run: () => Promise<void>) {
    if (inFlight.current || !current()) return
    inFlight.current = true
    setBusy(true)
    setError(null)
    setSaved(false)
    try {
      await run()
    } catch (issue) {
      if (
        current() &&
        issue instanceof CommerceImportAPIError &&
        issue.requestId &&
        connection
      ) {
        const refreshed = await requests.refetch()
        if (
          current() &&
          !refreshed.error &&
          refreshed.data?.some(
            (request) =>
              request.id === issue.requestId &&
              request.issuance_uncertain === false
          )
        ) {
          setUncertainConnections((values) => ({
            ...values,
            [connection.id]: false,
          }))
        }
      }
      if (current()) {
        setError(
          issue instanceof CommerceImportAPIError
            ? issue
            : new Error(
                issue instanceof Error &&
                  [
                    copy.invalidAuthorization,
                    copy.invalidCatalog,
                    copy.requestUnknown,
                  ].includes(issue.message as typeof copy.invalidAuthorization)
                  ? issue.message
                  : copy.requestFailed
              )
        )
      }
    } finally {
      inFlight.current = false
      if (current()) setBusy(false)
    }
  }
  async function authorize(
    target: CommerceImportConnection,
    cardsIssue: boolean
  ) {
    const result = await commerceImportApi.authorize(
      target.id,
      cardsIssue,
      authScope
    )
    if (!current()) return
    const url = commerceImportAuthorizationUrl(
      result.authorization_url,
      target.issuer
    )
    if (!url) throw new Error(copy.invalidAuthorization)
    navigate(url)
  }
  async function acceptBatch(request: CommerceImportRequest) {
    if (!current()) return
    client.setQueryData<CommerceImportRequest[]>(
      [...key, connectionId, 'requests'],
      (previous) => [
        request,
        ...(previous || []).filter((item) => item.id !== request.id),
      ]
    )
    if (request.status === 'imported' || request.issuance_uncertain === false) {
      setUncertainConnections((values) => ({
        ...values,
        [connectionId]: false,
      }))
    }
    await Promise.all([
      requests.refetch(),
      client.invalidateQueries({
        queryKey: ['store', 'my-products', authScope.userId],
      }),
      client.invalidateQueries({ queryKey: ['store', 'product'] }),
      client.invalidateQueries({ queryKey: ['store', 'product-preview'] }),
    ])
  }
  if (!config.enabled) return null
  const shownError =
    error || connections.error || catalog.error || requests.error
  return (
    <div className='space-y-6'>
      {shownError && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              shownError instanceof Error
                ? shownError.message
                : copy.requestFailed
            )}
          </AlertDescription>
        </Alert>
      )}
      {saved && (
        <Alert>
          <AlertDescription>{t(copy.saved)}</AlertDescription>
        </Alert>
      )}
      <form
        onSubmit={(event) => {
          event.preventDefault()
          if (!config.trusted_origins.includes(origin) || !clientId.trim()) {
            return
          }
          void mutate(async () => {
            const target = await commerceImportApi.connect(
              { origin, client_id: clientId.trim() },
              authScope
            )
            if (!current()) return
            setConnectionId(target.id)
            await connections.refetch()
            await authorize(target, false)
          })
        }}
      >
        <FieldGroup>
          <h3 className='font-semibold'>{t(copy.connectTitle)}</h3>
          <Field>
            <FieldLabel htmlFor={`${id}-origin`}>{t(copy.origin)}</FieldLabel>
            <NativeSelect
              id={`${id}-origin`}
              value={origin}
              disabled={busy}
              onChange={(event) => setOrigin(event.target.value)}
            >
              <NativeSelectOption value=''>{t(copy.origin)}</NativeSelectOption>
              {config.trusted_origins.map((value) => (
                <NativeSelectOption key={value} value={value}>
                  {value}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </Field>
          <Field data-disabled={busy}>
            <FieldLabel htmlFor={`${id}-client`}>{t(copy.clientId)}</FieldLabel>
            <Input
              id={`${id}-client`}
              autoComplete='off'
              maxLength={200}
              value={clientId}
              disabled={busy}
              onChange={(event) => setClientId(event.target.value)}
            />
            <FieldDescription>{t(copy.clientHelp)}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor={`${id}-callback`}>
              {t(copy.callback)}
            </FieldLabel>
            <Input id={`${id}-callback`} value={config.redirect_uri} readOnly />
          </Field>
          <Button
            type='submit'
            disabled={
              busy ||
              !config.trusted_origins.includes(origin) ||
              !clientId.trim()
            }
          >
            {t(copy.connect)}
          </Button>
        </FieldGroup>
      </form>
      <Separator />
      <section className='space-y-4' aria-label={t(copy.connections)}>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <h3 className='font-semibold'>{t(copy.connections)}</h3>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={busy}
            onClick={() => void refresh()}
          >
            {t(copy.refresh)}
          </Button>
        </div>
        {connections.isPending && <StoreLoading />}
        {connections.data?.length === 0 && (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>{t(copy.noConnections)}</EmptyTitle>
            </EmptyHeader>
          </Empty>
        )}
        {!!connections.data?.length && (
          <Field>
            <FieldLabel htmlFor={`${id}-connection`}>
              {t(copy.selectConnection)}
            </FieldLabel>
            <NativeSelect
              id={`${id}-connection`}
              value={connectionId}
              disabled={busy}
              onChange={(event) => {
                setConnectionId(event.target.value)
                setProductId('')
                setSaved(false)
                setError(null)
              }}
            >
              <NativeSelectOption value=''>
                {t(copy.selectConnection)}
              </NativeSelectOption>
              {connections.data.map((item) => (
                <NativeSelectOption key={item.id} value={item.id}>
                  {item.shop_name || item.issuer} ·{' '}
                  {t(
                    connectionStatus[item.status] || copy.reauthorizeConnection
                  )}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </Field>
        )}
        {connection && (
          <>
            <div className='flex flex-wrap items-center gap-2'>
              <Badge variant='secondary'>
                {t(
                  connectionStatus[connection.status] ||
                    copy.reauthorizeConnection
                )}
              </Badge>
              <Badge variant='outline'>
                {t(
                  connection.scope.split(/\s+/).includes('cards.issue')
                    ? copy.issueScope
                    : copy.readOnly
                )}
              </Badge>
              <span className='text-muted-foreground text-xs break-all'>
                {connection.issuer}
              </span>
            </div>
            <div className='flex flex-wrap gap-2'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={busy}
                onClick={() => void mutate(() => authorize(connection, false))}
              >
                {t(copy.readAuthorize)}
              </Button>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={busy}
                onClick={() => void mutate(() => authorize(connection, true))}
              >
                {t(copy.issueAuthorize)}
              </Button>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={busy}
                onClick={() => setDisconnecting(true)}
              >
                {t(copy.disconnect)}
              </Button>
            </div>
            <p className='text-muted-foreground text-sm'>{t(copy.issueHelp)}</p>
            {canRead && (
              <section className='space-y-4' aria-label={t(copy.catalog)}>
                <h3 className='font-semibold'>{t(copy.catalog)}</h3>
                {catalog.isPending && <StoreLoading />}
                {catalog.data?.products.length === 0 && (
                  <Empty>
                    <EmptyHeader>
                      <EmptyTitle>{t(copy.noProducts)}</EmptyTitle>
                      <EmptyDescription>
                        {t(copy.productsHelp)}
                      </EmptyDescription>
                    </EmptyHeader>
                  </Empty>
                )}
                <div className='grid gap-2 sm:grid-cols-2'>
                  {catalog.data?.products.map((product) => (
                    <Button
                      type='button'
                      key={product.id}
                      variant='outline'
                      className='h-auto min-h-11 justify-start py-3 text-left whitespace-normal'
                      disabled={busy}
                      onClick={() => {
                        setProductId(product.id)
                        setSaved(false)
                      }}
                    >
                      {commerceImportText(product.product.name)}
                      <span className='text-muted-foreground ml-2 text-xs break-all'>
                        {product.id}
                      </span>
                    </Button>
                  ))}
                </div>
                {listing && (
                  <StoreCommerceImportPreview
                    key={`${connection.id}:${listing.id}:${listing.revision}`}
                    listing={listing}
                    mapping={mapping}
                    connection={connection}
                    busy={busy}
                    uncertain={uncertain}
                    onSave={async (body) => {
                      await mutate(async () => {
                        await commerceImportApi.import(
                          connection.id,
                          body,
                          authScope
                        )
                        if (!current()) return
                        setSaved(true)
                        await Promise.all([
                          catalog.refetch(),
                          client.invalidateQueries({
                            queryKey: [
                              'store',
                              'my-products',
                              authScope.userId,
                            ],
                          }),
                        ])
                      })
                    }}
                    onRestock={async (body) => {
                      if (uncertain) return
                      await mutate(async () => {
                        setUncertainConnections((values) => ({
                          ...values,
                          [connection.id]: true,
                        }))
                        try {
                          const request = await commerceImportApi.restock(
                            connection.id,
                            body,
                            authScope
                          )
                          await acceptBatch(request)
                        } catch (issue) {
                          if (current()) await requests.refetch()
                          throw issue
                        }
                      })
                    }}
                  />
                )}
              </section>
            )}
            <Separator />
            <section className='space-y-3' aria-label={t(copy.batchHistory)}>
              <h3 className='font-semibold'>{t(copy.batchHistory)}</h3>
              <p className='text-muted-foreground text-sm'>
                {t(copy.recoveryHelp)}
              </p>
              {uncertain && (
                <Alert>
                  <AlertDescription>{t(copy.requestUnknown)}</AlertDescription>
                </Alert>
              )}
              {requests.isPending && <StoreLoading />}
              {requests.data?.length === 0 && (
                <p className='text-muted-foreground text-sm'>
                  {t(copy.noRequests)}
                </p>
              )}
              <div className='divide-y rounded-lg border'>
                {requests.data?.map((request) => (
                  <article key={request.id} className='space-y-2 p-3'>
                    <div className='flex flex-wrap items-center justify-between gap-2'>
                      <Badge
                        variant={
                          request.status === 'manual_recovery'
                            ? 'destructive'
                            : 'secondary'
                        }
                      >
                        {t(
                          ['pending', 'received'].includes(request.status) &&
                            !canRecoverRequest(request)
                            ? copy.manualRequest
                            : requestStatus[request.status] ||
                                copy.manualRequest
                        )}
                      </Badge>
                      {canRecoverRequest(request) && (
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          disabled={busy}
                          onClick={() =>
                            void mutate(async () => {
                              const result = await commerceImportApi.recover(
                                connection.id,
                                request.id,
                                authScope
                              )
                              await acceptBatch(result)
                            })
                          }
                        >
                          {t(copy.recover)}
                        </Button>
                      )}
                    </div>
                    <p className='text-muted-foreground text-xs break-all'>
                      {request.product_id} · {request.variant_id} ·{' '}
                      {request.count}
                    </p>
                    {request.label && (
                      <p className='text-sm break-words'>{request.label}</p>
                    )}
                    <p className='text-muted-foreground font-mono text-xs break-all'>
                      {request.id}
                    </p>
                    {request.error_code && (
                      <p className='text-destructive text-sm'>
                        {t(
                          new CommerceImportAPIError(request.error_code).message
                        )}
                      </p>
                    )}
                  </article>
                ))}
              </div>
            </section>
            <AlertDialog open={disconnecting} onOpenChange={setDisconnecting}>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>{t(copy.disconnectTitle)}</AlertDialogTitle>
                  <AlertDialogDescription render={<div />}>
                    <p>{t(copy.disconnectHelp)}</p>
                    <p className='mt-2'>{t(copy.disconnectRetentionHelp)}</p>
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>{t(copy.cancel)}</AlertDialogCancel>
                  <AlertDialogAction
                    disabled={busy}
                    onClick={() =>
                      void mutate(async () => {
                        await commerceImportApi.disconnect(
                          connection.id,
                          authScope
                        )
                        if (!current()) return
                        setConnectionId('')
                        setProductId('')
                        setDisconnecting(false)
                        client.removeQueries({
                          queryKey: [...key, connection.id],
                        })
                        await connections.refetch()
                      })
                    }
                  >
                    {t(copy.disconnect)}
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          </>
        )}
      </section>
    </div>
  )
}
