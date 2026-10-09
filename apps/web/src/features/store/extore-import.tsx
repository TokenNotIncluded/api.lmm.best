/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import { storeApi } from './api'
import { EXTORE_COPY as copy } from './extore-import-copy'
import {
  EXTORE_DEFAULT_ORIGIN,
  extoreOrigin,
  extoreText,
  extoreImage,
  parseExtoreCatalog,
  extoreProductDraft,
  type ExtoreCatalog,
  type ExtoreDraft,
} from './extore-import-protocol'
import { CopyStoreValue, StoreError, StoreLoading } from './shared'

function preferences(userId: number) {
  try {
    const value = JSON.parse(
      localStorage.getItem(`store:extore:${userId}`) || '{}'
    )
    return {
      baseUrl: extoreOrigin(
        typeof value.baseUrl === 'string' ? value.baseUrl : ''
      ),
      clientId: typeof value.clientId === 'string' ? value.clientId : '',
    }
  } catch {
    return { baseUrl: EXTORE_DEFAULT_ORIGIN, clientId: '' }
  }
}

export function StoreExtoreImport({
  userId,
  callbackUrl,
  redirectUri,
  accessSupported,
  onClose,
  onImport,
}: {
  userId: number
  callbackUrl: string | null
  redirectUri: string
  accessSupported: boolean
  onClose: () => void
  onImport: (draft: ExtoreDraft) => void
}) {
  const { t, i18n } = useTranslation()
  const [settings, setSettings] = useState(() => preferences(userId))
  const [catalog, setCatalog] = useState<ExtoreCatalog | null>(null)
  const [productId, setProductId] = useState('')
  const [variantId, setVariantId] = useState('')
  const [busy, setBusy] = useState(!!callbackUrl)
  const [error, setError] = useState<unknown>(null)
  const [returned, setReturned] = useState(!!callbackUrl)
  const request = useRef<Promise<unknown> | null>(null)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])
  useEffect(() => {
    if (!callbackUrl) return
    // Reuse one request across StrictMode effect replays. Never retry a code exchange.
    request.current ??= storeApi.extoreCatalog(callbackUrl)
    let active = true
    void request.current
      .then((value) => {
        if (!active) return
        setCatalog(parseExtoreCatalog(value))
      })
      .catch((issue: unknown) => {
        if (active) setError(issue)
      })
      .finally(() => {
        if (active) setBusy(false)
      })
    return () => {
      active = false
    }
  }, [callbackUrl])

  const listing = catalog?.products.find((item) => item.id === productId)
  const variant = listing?.variants.find(
    (item) => !!item.id && item.id === variantId && item.enabled === true
  )
  const supportedVisibility =
    listing?.product.public === true || accessSupported
  async function connect(event: React.FormEvent) {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      const baseUrl = extoreOrigin(settings.baseUrl)
      if (redirectUri !== `${window.location.origin}/store/manage`) {
        throw new Error(copy.unsupported)
      }
      const authorization = await storeApi.extoreAuthorize(
        baseUrl,
        settings.clientId.trim()
      )
      if (!alive.current) return
      const url = new URL(authorization.authorization_url)
      if (
        authorization.base_url !== baseUrl ||
        authorization.redirect_uri !== redirectUri ||
        url.origin !== baseUrl ||
        url.pathname !== '/oauth/authorize' ||
        url.username ||
        url.password ||
        url.hash
      ) {
        throw new Error(copy.unsupported)
      }
      try {
        localStorage.setItem(
          `store:extore:${userId}`,
          JSON.stringify({ baseUrl, clientId: settings.clientId.trim() })
        )
      } catch {
        /* Storage is optional; authorization still works. */
      }
      window.location.assign(url.href)
    } catch (issue) {
      if (alive.current) {
        setError(issue)
        setBusy(false)
      }
    }
  }
  function fill() {
    if (!catalog || !listing || !variant || !supportedVisibility) return
    try {
      onImport(extoreProductDraft(catalog, listing, variant, i18n.language))
    } catch (issue) {
      setError(issue)
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent className='max-h-[90svh] overflow-y-auto sm:max-w-2xl'>
        <DialogTitle>{t(copy.title)}</DialogTitle>
        <DialogDescription>{t(copy.help)}</DialogDescription>
        <StoreError error={error} />
        {busy ? (
          <StoreLoading />
        ) : catalog ? (
          <div className='space-y-6'>
            <p className='text-muted-foreground text-sm'>
              {catalog.shop.name} · {catalog.issuer}
            </p>
            {catalog.products.length === 0 ? (
              <p>{t(copy.empty)}</p>
            ) : (
              <>
                <div className='space-y-2'>
                  <Label htmlFor='extore-product'>
                    {t(copy.selectProduct)}
                  </Label>
                  <select
                    id='extore-product'
                    className='bg-background h-11 w-full rounded-md border px-3'
                    value={productId}
                    onChange={(event) => {
                      setProductId(event.target.value)
                      setVariantId('')
                      setError(null)
                    }}
                  >
                    <option value=''>{t('Select')}</option>
                    {catalog.products.map((item) => (
                      <option key={item.id} value={item.id}>
                        {extoreText(item.product.name, i18n.language) ||
                          item.id}
                      </option>
                    ))}
                  </select>
                </div>
                {listing && (
                  <>
                    <div className='space-y-2'>
                      <Label htmlFor='extore-variant'>
                        {t(copy.selectVariant)}
                      </Label>
                      <select
                        id='extore-variant'
                        className='bg-background h-11 w-full rounded-md border px-3'
                        value={variantId}
                        onChange={(event) => setVariantId(event.target.value)}
                      >
                        <option value=''>{t('Select')}</option>
                        {listing.variants.map((item, index) => (
                          <option
                            key={item.id ?? `missing-${index}`}
                            value={item.id ?? ''}
                            disabled={!item.id || item.enabled !== true}
                          >
                            {extoreText(item.name, i18n.language) ||
                              item.id ||
                              t('Not provided')}
                            {!item.id || item.enabled !== true
                              ? ` · ${t('Disabled')}`
                              : ''}
                          </option>
                        ))}
                      </select>
                      {!listing.variants.some(
                        (item) => item.id && item.enabled === true
                      ) && (
                        <p className='text-muted-foreground text-sm'>
                          {t(copy.noVariants)}
                        </p>
                      )}
                    </div>
                    <p className='text-sm leading-7 whitespace-pre-wrap'>
                      {extoreText(listing.product.description, i18n.language)}
                    </p>
                    {variant && (
                      <p className='text-sm'>
                        {t(copy.reference)}:{' '}
                        <strong>
                          {variant.price == null
                            ? t(copy.unknownPrice)
                            : `${variant.price} ${variant.currency ?? t('Not provided')}`}
                        </strong>
                      </p>
                    )}
                    {listing.product.public !== true && (
                      <p className='text-sm' role='note'>
                        {t(
                          supportedVisibility
                            ? copy.privacyWarning
                            : copy.privateUnsupported
                        )}
                      </p>
                    )}
                    {[listing.product.logo, listing.product.image].some(
                      (image) => image && !extoreImage(image)
                    ) && (
                      <p className='text-sm' role='note'>
                        {t(copy.imageWarning)}
                      </p>
                    )}
                    <details>
                      <summary className='text-muted-foreground cursor-pointer py-2 text-sm'>
                        {t(copy.fields)}
                      </summary>
                      <p className='my-3 text-sm leading-6'>
                        {t(copy.sourceHelp)}
                      </p>
                      <pre className='bg-muted/40 max-h-64 overflow-auto rounded-lg p-4 text-xs break-all whitespace-pre-wrap'>
                        {JSON.stringify(listing, null, 2)}
                      </pre>
                    </details>
                  </>
                )}
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(copy.draftHelp)}
                </p>
                <Button
                  onClick={fill}
                  disabled={!variant || !supportedVisibility}
                >
                  {t(copy.import)}
                </Button>
              </>
            )}
            <Button
              variant='ghost'
              onClick={() => {
                setCatalog(null)
                setReturned(false)
                setError(null)
              }}
            >
              {t(copy.reconnect)}
            </Button>
          </div>
        ) : returned ? (
          <Button
            onClick={() => {
              setReturned(false)
              setError(null)
            }}
          >
            {t(copy.reconnect)}
          </Button>
        ) : (
          <form onSubmit={(event) => void connect(event)} className='space-y-6'>
            <div className='space-y-2'>
              <Label htmlFor='extore-base-url'>{t(copy.baseUrl)}</Label>
              <Input
                id='extore-base-url'
                value={settings.baseUrl}
                maxLength={253}
                onChange={(event) =>
                  setSettings({ ...settings, baseUrl: event.target.value })
                }
                placeholder={EXTORE_DEFAULT_ORIGIN}
                autoCapitalize='none'
                spellCheck={false}
                required
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor='extore-client-id'>{t(copy.clientId)}</Label>
              <Input
                id='extore-client-id'
                value={settings.clientId}
                maxLength={200}
                onChange={(event) =>
                  setSettings({ ...settings, clientId: event.target.value })
                }
                autoCapitalize='none'
                spellCheck={false}
                required
              />
            </div>
            <details>
              <summary className='text-muted-foreground cursor-pointer py-2 text-sm'>
                {t(copy.registration)}
              </summary>
              <p className='my-3 text-sm leading-6'>
                {t(copy.registrationHelp)}
              </p>
              <Label>{t(copy.callback)}</Label>
              <p className='my-2 font-mono text-xs break-all'>{redirectUri}</p>
              <CopyStoreValue value={redirectUri} />
            </details>
            <p className='text-muted-foreground text-sm'>{t(copy.readOnly)}</p>
            <Button type='submit' disabled={busy || !settings.clientId.trim()}>
              {t(copy.connect)}
            </Button>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
