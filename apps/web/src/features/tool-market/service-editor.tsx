/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import {
  marketAPI,
  MarketAPIError,
  marketQuota,
  type DraftInput,
  type MarketDetail,
  type MarketService,
  type ToolInput,
} from './api'
import { creditAmount, marketNetQuota } from './money'
import {
  editorCredentialWrite,
  refreshToolDefinitions,
  type StoredEditorCredentials,
  type ToolDefinitionChanges,
} from './service-editor-utils'

export function ServiceEditor({
  initial: initialDetail,
  units,
  feeBps,
  onSaved,
  onCancel,
}: {
  initial?: MarketDetail
  units: number
  feeBps?: number
  onSaved: (id: string) => void
  onCancel: () => void
}) {
  // Draft-query refreshes during a partial save must not replace the credential
  // source version or reset the owner's in-progress editor session.
  const initial = useRef(initialDetail).current
  const { t } = useTranslation()
  const cache = useQueryClient()
  const [name, setName] = useState(initial?.version.name ?? '')
  const [description, setDescription] = useState(
    initial?.version.description ?? ''
  )
  const [endpoint, setEndpoint] = useState(initial?.version.endpoint ?? '')
  const [visibility, setVisibility] = useState(
    initial?.version.visibility ?? 'private'
  )
  const [shared, setShared] = useState(initial?.allowed_users?.join(', ') ?? '')
  const [tools, setTools] = useState<ToolInput[]>(
    () =>
      initial?.tools.map((tool) => ({
        name: tool.name,
        description: tool.description,
        input_schema: tool.input_schema,
        ...(tool.output_schema ? { output_schema: tool.output_schema } : {}),
        permissions: JSON.parse(tool.permissions) ?? [],
        price_quota: tool.price_quota,
      })) ?? []
  )
  const [selected, setSelected] = useState<string[]>(
    tools.map((tool) => tool.name)
  )
  const [prices, setPrices] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      tools.map((tool) => [tool.name, String(tool.price_quota / units)])
    )
  )
  const [billingModes, setBillingModes] = useState<
    Record<string, 'free' | 'paid'>
  >(() =>
    Object.fromEntries(
      tools.map((tool) => [tool.name, tool.price_quota > 0 ? 'paid' : 'free'])
    )
  )
  const [inspectedEndpoint, setInspectedEndpoint] = useState(
    initial?.version.endpoint ?? ''
  )
  const [inspectVersion, setInspectVersion] = useState(0)
  const [changes, setChanges] = useState<ToolDefinitionChanges>()
  const [newToolsNeedSelection, setNewToolsNeedSelection] = useState(false)
  const [reviewedChanges, setReviewedChanges] = useState(false)
  const [authMode, setAuthMode] =
    useState<StoredEditorCredentials['mode']>('none')
  const [secret, setSecret] = useState('')
  const authEdited = useRef(false)
  const hasInspected = useRef(Boolean(initial))
  const operationLock = useRef(false)
  const savedServiceID = useRef(initial?.service.id)
  const savedDraft = useRef<
    { fingerprint: string; service: MarketService } | undefined
  >(undefined)
  const [inspectPending, setInspectPending] = useState(false)
  const [savePending, setSavePending] = useState(false)
  const [error, setError] = useState<
    | 'operation'
    | 'credentials'
    | 'credential-required'
    | 'credential-invalid'
    | 'credentials-unavailable'
  >()
  const credentials = useQuery({
    queryKey: [
      'tool-market',
      'service-credentials',
      initial?.service.id,
      initial?.version.id,
    ],
    queryFn: () => {
      if (!initial) throw new Error('Missing service')
      return marketAPI.credentials(initial.service.id, initial.version.id)
    },
    enabled: Boolean(initial),
    retry: false,
  })
  useEffect(() => {
    if (credentials.data && !authEdited.current) {
      setAuthMode(credentials.data.mode)
    }
  }, [credentials.data])
  const credentialsReady = !initial || credentials.isSuccess
  const pending =
    inspectPending || savePending || (Boolean(initial) && credentials.isPending)
  const requiresReview = Boolean(
    changes?.endpointChanged ||
    changes?.changed.length ||
    changes?.removed.length
  )
  const priceQuotas: Record<string, number | undefined> = Object.fromEntries(
    tools.map((tool) => {
      try {
        const raw = prices[tool.name] ?? '0'
        const quota = marketQuota(raw, units)
        if (
          Number(raw) > 1000000 ||
          (billingModes[tool.name] === 'paid' ? quota <= 0 : quota !== 0)
        ) {
          throw new Error('Invalid price')
        }
        return [tool.name, quota]
      } catch {
        return [tool.name, undefined]
      }
    })
  )
  const hasInvalidPrice = selected.some(
    (name) => priceQuotas[name] === undefined
  )
  const credentialChoice = () =>
    editorCredentialWrite({
      mode: authMode,
      secret,
      stored: credentials.data,
      storedVersionID: initial?.version.id,
      sameEndpoint: initial?.version.endpoint === endpoint,
    })
  const inspect = async () => {
    if (operationLock.current || !credentialsReady) return
    operationLock.current = true
    setInspectPending(true)
    setInspectVersion(0)
    setError(undefined)
    try {
      const choice = credentialChoice()
      const reference =
        choice.copy_from_version_id && initial
          ? {
              service_id: initial.service.id,
              version_id: choice.copy_from_version_id,
            }
          : undefined
      const authentication = reference
        ? undefined
        : { mode: choice.mode, secret: choice.secret }
      const data = await marketAPI.inspect(endpoint, authentication, reference)
      const refreshed = refreshToolDefinitions(
        { tools, selected, prices },
        data,
        {
          firstDiscovery: !hasInspected.current,
          endpointChanged: Boolean(
            inspectedEndpoint && inspectedEndpoint !== endpoint
          ),
        }
      )
      setNewToolsNeedSelection(
        hasInspected.current && refreshed.changes.added.length > 0
      )
      setTools(refreshed.tools)
      setSelected(refreshed.selected)
      setPrices(refreshed.prices)
      setBillingModes((current) =>
        Object.fromEntries(
          refreshed.tools.map((tool) => [
            tool.name,
            current[tool.name] ?? 'free',
          ])
        )
      )
      setChanges((previous) =>
        previous && !reviewedChanges
          ? {
              added: refreshed.changes.added,
              removed: [
                ...new Set([...previous.removed, ...refreshed.changes.removed]),
              ].filter((name) => !data.some((tool) => tool.name === name)),
              changed: [
                ...new Set([...previous.changed, ...refreshed.changes.changed]),
              ].filter((name) => data.some((tool) => tool.name === name)),
              endpointChanged:
                previous.endpointChanged || refreshed.changes.endpointChanged,
            }
          : refreshed.changes
      )
      setReviewedChanges(false)
      hasInspected.current = true
      setInspectedEndpoint(endpoint)
      setInspectVersion((value) => value + 1)
    } catch (cause) {
      setError(
        cause instanceof Error && cause.message === 'Credential required'
          ? 'credential-required'
          : cause instanceof Error && cause.message === 'Invalid credential'
            ? 'credential-invalid'
            : 'operation'
      )
    } finally {
      setInspectPending(false)
      operationLock.current = false
    }
  }
  const save = async () => {
    if (operationLock.current || !credentialsReady || hasInvalidPrice) return
    operationLock.current = true
    setSavePending(true)
    setError(undefined)
    let draftSaved = false
    try {
      const ids =
        visibility === 'shared'
          ? shared.split(',').map((value) => Number(value.trim()))
          : []
      if (
        !name.trim() ||
        (visibility === 'shared' &&
          (ids.length === 0 ||
            ids.some((id) => !Number.isSafeInteger(id) || id <= 0)))
      ) {
        throw new Error('Invalid input')
      }
      if (
        !inspectVersion ||
        inspectedEndpoint !== endpoint ||
        (requiresReview && !reviewedChanges)
      ) {
        throw new Error('Inspect the current endpoint before saving')
      }
      const authentication = credentialChoice()
      const input: DraftInput = {
        name,
        description,
        endpoint,
        execution_type: 'remote',
        visibility,
        allowed_users: ids,
        tools: tools
          .filter((tool) => selected.includes(tool.name))
          .map((tool) => ({
            ...tool,
            price_quota: marketQuota(prices[tool.name] ?? '0', units),
          })),
      }
      if (!input.tools.length) throw new Error('Select a tool')
      const fingerprint = JSON.stringify(input)
      const service =
        savedDraft.current?.fingerprint === fingerprint
          ? savedDraft.current.service
          : await marketAPI.save(savedServiceID.current, input)
      savedServiceID.current = service.id
      savedDraft.current = { fingerprint, service }
      draftSaved = true
      await marketAPI.setCredentials(service.id, {
        version_id: service.draft_version_id,
        ...authentication,
      })
      setSecret('')
      void cache.invalidateQueries({ queryKey: ['tool-market'] })
      onSaved(service.id)
    } catch (cause) {
      setError(
        draftSaved &&
          cause instanceof MarketAPIError &&
          cause.code === 'TOOL_MARKET_CREDENTIALS_UNAVAILABLE'
          ? 'credentials-unavailable'
          : draftSaved
            ? 'credentials'
            : cause instanceof Error && cause.message === 'Credential required'
              ? 'credential-required'
              : cause instanceof Error && cause.message === 'Invalid credential'
                ? 'credential-invalid'
                : 'operation'
      )
      if (draftSaved) {
        void cache.invalidateQueries({ queryKey: ['tool-market'] })
      }
    } finally {
      setSavePending(false)
      operationLock.current = false
    }
  }
  return (
    <section className='max-w-3xl space-y-6'>
      <div>
        <h3 className='text-lg font-semibold'>{t('Publish a tool service')}</h3>
        <p className='text-muted-foreground mt-2 text-sm'>
          {t(
            'Connect a public HTTPS MCP service. Add a Bearer token or API key when the service requires authentication.'
          )}
        </p>
      </div>
      <form
        onSubmit={(event) => {
          event.preventDefault()
          void save()
        }}
      >
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor='market-name'>{t('Name')}</FieldLabel>
            <Input
              id='market-name'
              required
              maxLength={120}
              value={name}
              disabled={pending}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor='market-description'>
              {t('Description')}
            </FieldLabel>
            <Textarea
              id='market-description'
              maxLength={8000}
              value={description}
              disabled={pending}
              onChange={(e) => setDescription(e.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor='market-endpoint'>
              {t('Remote MCP endpoint')}
            </FieldLabel>
            <Input
              id='market-endpoint'
              required
              type='url'
              value={endpoint}
              disabled={pending}
              placeholder='https://example.com/mcp'
              onChange={(e) => {
                setEndpoint(e.target.value)
                setSecret('')
                setInspectVersion(0)
              }}
            />
            <FieldDescription>
              {t('Do not include API keys or tokens in the URL.')}
            </FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor='market-authentication'>
              {t('Authentication')}
            </FieldLabel>
            <select
              id='market-authentication'
              className='border-input bg-background h-9 rounded-md border px-3 text-sm'
              value={authMode}
              disabled={pending || !credentialsReady}
              onChange={(event) => {
                authEdited.current = true
                setAuthMode(
                  event.target.value as StoredEditorCredentials['mode']
                )
                setSecret('')
                setInspectVersion(0)
              }}
            >
              <option value='none'>{t('No authentication')}</option>
              <option value='bearer'>{t('Bearer token')}</option>
              <option value='api_key'>{t('API key (X-API-Key)')}</option>
            </select>
            {authMode !== 'none' && (
              <>
                <Input
                  id='market-secret'
                  type='password'
                  autoComplete='new-password'
                  aria-label={t('Service credential')}
                  value={secret}
                  maxLength={4096}
                  disabled={pending || !credentialsReady}
                  onChange={(event) => {
                    setSecret(event.target.value)
                    setInspectVersion(0)
                  }}
                />
                <FieldDescription>
                  {credentials.data?.configured &&
                  credentials.data.mode === authMode &&
                  initial?.version.endpoint === endpoint
                    ? t(
                        'A credential is saved. Leave this field empty to keep it, or enter a replacement.'
                      )
                    : t(
                        'Enter the service credential. It is stored securely and is not included in client configurations.'
                      )}
                </FieldDescription>
              </>
            )}
            {initial && credentials.isError && (
              <>
                <p role='alert' className='text-destructive text-sm'>
                  {t(
                    'Could not read the saved authentication settings. Retry before editing this service.'
                  )}
                </p>
                <Button
                  type='button'
                  variant='outline'
                  onClick={() => void credentials.refetch()}
                >
                  {t('Retry')}
                </Button>
              </>
            )}
            <Button
              type='button'
              variant='outline'
              disabled={pending || !endpoint || !credentialsReady}
              onClick={() => void inspect()}
            >
              {inspectPending ? t('Checking…') : t('Read tool definitions')}
            </Button>
            {tools.length > 0 &&
              (!inspectVersion || inspectedEndpoint !== endpoint) && (
                <FieldDescription>
                  {t(
                    'Read the current endpoint again before saving. Your prices and selections are kept.'
                  )}
                </FieldDescription>
              )}
          </Field>
          <Field>
            <FieldLabel htmlFor='market-visibility'>
              {t('Visibility')}
            </FieldLabel>
            <select
              id='market-visibility'
              className='border-input bg-background h-9 rounded-md border px-3 text-sm'
              value={visibility}
              disabled={pending}
              onChange={(e) => setVisibility(e.target.value)}
            >
              <option value='private'>{t('Only me')}</option>
              <option value='public'>{t('Public')}</option>
              <option value='shared'>{t('Specific users')}</option>
            </select>
          </Field>
          {visibility === 'shared' && (
            <Field>
              <FieldLabel htmlFor='market-shared'>
                {t('User IDs, separated by commas')}
              </FieldLabel>
              <Input
                id='market-shared'
                required
                value={shared}
                disabled={pending}
                onChange={(e) => setShared(e.target.value)}
              />
            </Field>
          )}
          <fieldset className='space-y-4'>
            <legend className='mb-3 font-medium'>
              {t('Tools and prices')}
            </legend>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Choose free or paid pricing for each tool. Charges apply only to successful calls; failed and expired calls are refunded.'
              )}
            </p>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Earnings stay in your platform balance and cannot be withdrawn.'
              )}
            </p>
            {tools.length === 0 && (
              <p className='bg-muted rounded-md p-3 text-sm'>
                {t(
                  'Read the MCP tool definitions first to choose free or paid pricing for each tool.'
                )}
              </p>
            )}
            {changes && (
              <div
                className='bg-muted space-y-2 rounded-md p-3 text-sm'
                role='status'
              >
                <p>
                  {t(
                    'Existing prices, selections and declared permissions are preserved when definitions are refreshed.'
                  )}
                </p>
                {newToolsNeedSelection && (
                  <p>
                    {t(
                      'New tools are not selected automatically. Review and select the tools you want to publish.'
                    )}
                  </p>
                )}
                {changes.endpointChanged && (
                  <p>
                    {t(
                      'The endpoint changed. Review the tools and permissions before saving.'
                    )}
                  </p>
                )}
                {changes.changed.length > 0 && (
                  <p>
                    {t('Changed tool definitions')}:{' '}
                    <span className='break-all'>
                      {changes.changed.join(', ')}
                    </span>
                  </p>
                )}
                {changes.removed.length > 0 && (
                  <p>
                    {t('Removed tools')}:{' '}
                    <span className='break-all'>
                      {changes.removed.join(', ')}
                    </span>
                  </p>
                )}
                {requiresReview && (
                  <label className='flex items-center gap-2'>
                    <Checkbox
                      checked={reviewedChanges}
                      disabled={pending}
                      onCheckedChange={(checked) =>
                        setReviewedChanges(Boolean(checked))
                      }
                    />
                    {t('I reviewed the endpoint and tool definition changes.')}
                  </label>
                )}
              </div>
            )}
            {tools.map((tool) => {
              const priceQuota = priceQuotas[tool.name]
              return (
                <div
                  key={tool.name}
                  className='border-border space-y-3 border-b pb-4'
                >
                  <div className='flex items-start gap-3'>
                    <Checkbox
                      id={`select-${tool.name}`}
                      checked={selected.includes(tool.name)}
                      disabled={pending}
                      onCheckedChange={(checked) =>
                        setSelected((current) =>
                          checked
                            ? [...current, tool.name]
                            : current.filter((name) => name !== tool.name)
                        )
                      }
                    />
                    <label
                      htmlFor={`select-${tool.name}`}
                      className='min-w-0 text-sm'
                    >
                      <strong className='break-all'>{tool.name}</strong>
                      {changes?.added.includes(tool.name) && (
                        <span className='text-muted-foreground ms-2'>
                          {t('New')}
                        </span>
                      )}
                      {changes?.changed.includes(tool.name) && (
                        <span className='text-muted-foreground ms-2'>
                          {t('Definition changed')}
                        </span>
                      )}
                      <span className='text-muted-foreground mt-1 block whitespace-pre-wrap'>
                        {tool.description}
                      </span>
                    </label>
                  </div>
                  {selected.includes(tool.name) && (
                    <>
                      <Field>
                        <FieldLabel htmlFor={`billing-mode-${tool.name}`}>
                          {t('Billing mode')}
                        </FieldLabel>
                        <select
                          id={`billing-mode-${tool.name}`}
                          className='border-input bg-background h-9 rounded-md border px-3 text-sm'
                          value={billingModes[tool.name] ?? 'free'}
                          disabled={pending}
                          onChange={(event) => {
                            const mode =
                              event.target.value === 'paid' ? 'paid' : 'free'
                            setBillingModes((current) => ({
                              ...current,
                              [tool.name]: mode,
                            }))
                            setPrices((current) => ({
                              ...current,
                              [tool.name]: mode === 'free' ? '0' : '',
                            }))
                          }}
                        >
                          <option value='free'>{t('Free tool')}</option>
                          <option value='paid'>{t('Paid tool')}</option>
                        </select>
                      </Field>
                      <Field>
                        <FieldLabel htmlFor={`price-${tool.name}`}>
                          {t('Price per successful call')}
                        </FieldLabel>
                        <Input
                          id={`price-${tool.name}`}
                          inputMode='decimal'
                          type='number'
                          min='0'
                          max='1000000'
                          step='0.000001'
                          value={prices[tool.name] ?? '0'}
                          aria-invalid={priceQuota === undefined}
                          disabled={pending}
                          onChange={(e) => {
                            const raw = e.target.value
                            setPrices((current) => ({
                              ...current,
                              [tool.name]: raw,
                            }))
                            if (Number(raw) > 0) {
                              setBillingModes((current) => ({
                                ...current,
                                [tool.name]: 'paid',
                              }))
                            }
                          }}
                        />
                        {billingModes[tool.name] === 'paid' &&
                          priceQuota === undefined && (
                            <FieldDescription className='text-destructive'>
                              {t('Enter a positive price for a paid tool.')}
                            </FieldDescription>
                          )}
                        {priceQuota !== undefined &&
                          feeBps !== undefined &&
                          Number.isSafeInteger(feeBps) &&
                          feeBps >= 0 &&
                          feeBps <= 10000 && (
                            <FieldDescription>
                              {t(
                                'You receive {{amount}} credits per successful call after the {{fee}}% platform fee.',
                                {
                                  amount: creditAmount(
                                    marketNetQuota(priceQuota, feeBps),
                                    units
                                  ),
                                  fee: feeBps / 100,
                                }
                              )}
                            </FieldDescription>
                          )}
                      </Field>
                      <fieldset className='flex flex-wrap gap-3 text-sm'>
                        <legend className='mb-2'>
                          {t('Declared permissions')}
                        </legend>
                        {(
                          [
                            'read',
                            'write',
                            'delete',
                            'send',
                            'network',
                            'files',
                            'external_account',
                          ] as const
                        ).map((permission) => (
                          <label
                            key={permission}
                            className='flex items-center gap-2'
                          >
                            <Checkbox
                              checked={tool.permissions.includes(permission)}
                              disabled={pending}
                              onCheckedChange={(checked) =>
                                setTools((current) =>
                                  current.map((item) =>
                                    item.name === tool.name
                                      ? {
                                          ...item,
                                          permissions: checked
                                            ? [...item.permissions, permission]
                                            : item.permissions.filter(
                                                (p) => p !== permission
                                              ),
                                        }
                                      : item
                                  )
                                )
                              }
                            />
                            {t(
                              permission === 'external_account'
                                ? 'External account'
                                : {
                                    read: 'Read',
                                    write: 'Write',
                                    delete: 'Delete',
                                    send: 'Send',
                                    network: 'Network',
                                    files: 'Files',
                                  }[permission]
                            )}
                          </label>
                        ))}
                      </fieldset>
                      <details className='text-sm'>
                        <summary className='cursor-pointer'>
                          {t('Parameter schema')}
                        </summary>
                        <pre className='bg-muted mt-2 max-h-52 overflow-auto p-3 text-xs'>
                          {typeof tool.input_schema === 'string'
                            ? tool.input_schema
                            : JSON.stringify(tool.input_schema, null, 2)}
                        </pre>
                      </details>
                    </>
                  )}
                </div>
              )
            })}
          </fieldset>
          {error && (
            <p role='alert' className='text-destructive text-sm'>
              {error === 'credentials'
                ? t(
                    'The draft was saved, but authentication could not be saved. Retry saving to finish; the same draft will be reused.'
                  )
                : error === 'credential-required'
                  ? t(
                      'Enter a credential for this endpoint and authentication method, then read the tool definitions again.'
                    )
                  : error === 'credential-invalid'
                    ? t(
                        'The credential is invalid or too long. Use a Bearer token without spaces or a valid API key.'
                      )
                    : error === 'credentials-unavailable'
                      ? t(
                          'The draft was saved, but credential storage is not configured. Ask an administrator to configure encryption, then retry saving.'
                        )
                      : t(
                          'The operation failed. Check the fields, endpoint and supported tool definitions, then retry.'
                        )}
            </p>
          )}
          <div className='flex gap-2'>
            <Button
              type='submit'
              disabled={
                pending ||
                !credentialsReady ||
                !selected.length ||
                hasInvalidPrice ||
                !inspectVersion ||
                (requiresReview && !reviewedChanges)
              }
            >
              {savePending ? t('Saving…') : t('Save draft')}
            </Button>
            <Button
              type='button'
              variant='outline'
              onClick={() => {
                setSecret('')
                onCancel()
              }}
              disabled={pending}
            >
              {t('Cancel')}
            </Button>
          </div>
        </FieldGroup>
      </form>
    </section>
  )
}
