/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'

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
import { useWalletCurrency } from '@/hooks/use-wallet-currency'

import {
  marketAPI,
  MarketAPIError,
  marketQuota,
  type DraftInput,
  type MarketDetail,
  type MarketService,
  type ToolInput,
} from './api'
import { marketNetQuota } from './money'
import { useMarketTranslation as useTranslation } from './provider-i18n'
import { ProviderPresetFields } from './provider-preset-fields'
import {
  editorCredentialWrite,
  refreshToolDefinitions,
  type StoredEditorCredentials,
  type ToolDefinitionChanges,
} from './service-editor-utils'
import { maximumUsageQuota, usageMetrics } from './usage-pricing'
import { UsagePricingEditor } from './usage-pricing-editor'

export function ServiceEditor({
  initial: initialDetail,
  feeBps,
  onSaved,
  onCancel,
}: {
  initial?: MarketDetail
  units?: number
  feeBps?: number
  onSaved: (id: string) => void
  onCancel: () => void
}) {
  // Draft-query refreshes during a partial save must not replace the credential
  // source version or reset the owner's in-progress editor session.
  const initial = useRef(initialDetail).current
  const { t } = useTranslation()
  const {
    formatQuota: formatRawQuota,
    quotaToInput,
    amountToQuota,
    currency,
    label,
    step,
  } = useWalletCurrency()
  const formatQuota = (quota: number) =>
    formatRawQuota(quota, { digitsLarge: 8, digitsSmall: 8 })
  const inputCurrencyKey = `${currency}:${quotaToInput(1)}`
  const [priceDrafts, setPriceDrafts] = useState<
    Record<string, { key: string; input: string }>
  >({})
  const cache = useQueryClient()
  const [name, setName] = useState(initial?.version.name ?? '')
  const [description, setDescription] = useState(
    initial?.version.description ?? ''
  )
  const [endpoint, setEndpoint] = useState(initial?.version.endpoint ?? '')
  const [presetID, setPresetID] = useState(
    initial?.tools.find((tool) => tool.provider_pricing)?.provider_pricing
      ?.provider ?? ''
  )
  const [multiplier, setMultiplier] = useState(
    initial?.tools.find((tool) => tool.provider_pricing)?.provider_pricing
      ?.multiplier ?? '1.2'
  )
  const config = useQuery({
    queryKey: ['tool-market', 'config'],
    queryFn: marketAPI.config,
  })
  const validMultiplier =
    /^(?:[1-9][0-9]*)(?:\.[0-9]+)?$/.test(multiplier) &&
    Number(multiplier) >= 1 &&
    Number(multiplier) <= 100

  const [visibility, setVisibility] = useState(
    initial?.version.visibility ?? 'private'
  )
  const [shared, setShared] = useState(initial?.allowed_users?.join(', ') ?? '')
  const [tools, setTools] = useState<ToolInput[]>(
    () =>
      initial?.tools.map((tool) => ({
        name: tool.name,
        provider_pricing: tool.provider_pricing,
        description: tool.description,
        input_schema: tool.input_schema,
        ...(tool.output_schema ? { output_schema: tool.output_schema } : {}),
        permissions: JSON.parse(tool.permissions) ?? [],
        price_quota: tool.price_quota,
        billing_mode: tool.billing_mode,
        input_token_price_quota: tool.input_token_price_quota,
        max_input_tokens: tool.max_input_tokens,
        billing_rules: tool.billing_rules,
        available_metering_metrics: usageMetrics.map((metric) => metric.metric),
      })) ?? []
  )
  const [selected, setSelected] = useState<string[]>(
    tools.map((tool) => tool.name)
  )
  const [prices, setPrices] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      tools.map((tool) => [
        tool.name,
        String(
          tool.billing_mode === 'input_tokens'
            ? (tool.input_token_price_quota ?? 0)
            : tool.price_quota
        ),
      ])
    )
  )
  const [billingModes, setBillingModes] = useState<
    Record<string, 'free' | 'paid' | 'input_tokens' | 'metered'>
  >(() =>
    Object.fromEntries(
      tools.map((tool) => [
        tool.name,
        tool.billing_mode === 'metered'
          ? 'metered'
          : tool.billing_mode === 'input_tokens'
            ? 'input_tokens'
            : tool.price_quota > 0
              ? 'paid'
              : 'free',
      ])
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
        if (billingModes[tool.name] === 'metered') {
          return [tool.name, maximumUsageQuota(tool.billing_rules ?? [])]
        }
        const raw = prices[tool.name] ?? '0'
        const quota = marketQuota(raw, Number)
        if (billingModes[tool.name] !== 'free' ? quota <= 0 : quota !== 0) {
          throw new Error('Invalid price')
        }
        if (billingModes[tool.name] === 'input_tokens') {
          const cap = tool.max_input_tokens ?? 200000
          if (!Number.isSafeInteger(cap) || cap < 1 || cap > 1000000) {
            throw new Error('Invalid token limit')
          }
          return [
            tool.name,
            Number((BigInt(quota) * BigInt(cap) + 999999n) / 1000000n),
          ]
        }
        return [tool.name, quota]
      } catch {
        return [tool.name, undefined]
      }
    })
  )
  const hasInvalidPrice =
    (Boolean(presetID) &&
      (!/^(?:[1-9][0-9]*)(?:\.[0-9]+)?$/.test(multiplier) ||
        Number(multiplier) > 100)) ||
    selected.some((name) => {
      const tool = tools.find((item) => item.name === name)
      const mode = billingModes[name]
      return (
        priceQuotas[name] === undefined ||
        (mode === 'input_tokens' &&
          !tool?.available_metering_metrics?.includes('input_tokens')) ||
        (mode === 'metered' &&
          (tool?.billing_rules ?? []).some(
            (rule) => !tool?.available_metering_metrics?.includes(rule.metric)
          ))
      )
    })
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
      const discovered = await marketAPI.inspect(
        endpoint,
        authentication,
        reference,
        presetID
      )
      const ceiling = Math.ceil(Number(config.data?.credits_per_usd ?? '0'))
      const data = discovered.map((tool) =>
        tool.provider_pricing
          ? {
              ...tool,
              provider_pricing: { ...tool.provider_pricing, multiplier },
              price_quota:
                Number.isSafeInteger(ceiling) && ceiling > 0 ? ceiling : 0,
            }
          : tool
      )
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
      setTools(
        refreshed.tools.map((tool) => ({
          ...tool,
          available_metering_metrics: usageMetrics.map(
            (metric) => metric.metric
          ),
        }))
      )
      setSelected(refreshed.selected)
      setPrices(refreshed.prices)
      setBillingModes((current) =>
        Object.fromEntries(
          refreshed.tools.map((tool) => [
            tool.name,
            tool.provider_pricing ? 'paid' : (current[tool.name] ?? 'free'),
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
            provider_pricing: tool.provider_pricing
              ? { ...tool.provider_pricing, multiplier }
              : undefined,
            available_metering_metrics: undefined,
            price_quota: priceQuotas[tool.name] ?? 0,
            billing_mode:
              billingModes[tool.name] === 'metered'
                ? 'metered'
                : billingModes[tool.name] === 'input_tokens'
                  ? 'input_tokens'
                  : '',
            billing_rules:
              billingModes[tool.name] === 'metered'
                ? tool.billing_rules
                : undefined,
            input_token_price_quota:
              billingModes[tool.name] === 'input_tokens'
                ? marketQuota(prices[tool.name] ?? '0', Number)
                : 0,
            max_input_tokens:
              billingModes[tool.name] === 'input_tokens'
                ? (tool.max_input_tokens ?? 200000)
                : 0,
          })),
      }
      if (!input.tools.length) throw new Error('Select a tool')
      if (
        input.tools.some((tool) => tool.provider_pricing) &&
        !validMultiplier
      ) {
        throw new Error('Invalid multiplier')
      }
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
    <section className='max-w-4xl space-y-6'>
      <div>
        <h3 className='text-lg font-semibold'>{t('Publish a tool service')}</h3>
        <p className='text-muted-foreground mt-2 max-w-[70ch] text-sm leading-6'>
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
        <FieldGroup className='[&_input]:min-h-11'>
          <ProviderPresetFields
            presets={config.data?.provider_presets ?? []}
            selected={presetID}
            multiplier={multiplier}
            disabled={pending}
            onMultiplier={setMultiplier}
            onSelect={(preset) => {
              setPresetID(preset?.id ?? '')
              setEndpoint(preset?.endpoint ?? '')
              if (!name && preset) setName(preset.name)
              authEdited.current = true
              setAuthMode(preset ? 'bearer' : 'none')
              setSecret('')
              setTools([])
              setSelected([])
              setPrices({})
              setBillingModes({})
              setPriceDrafts({})
              setInspectVersion(0)
              hasInspected.current = false
              setChanges(undefined)
              setReviewedChanges(false)
            }}
          />
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
              disabled={pending || Boolean(presetID)}
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
              className='border-input bg-background focus-visible:ring-ring min-h-11 w-full rounded-md border px-3 text-base outline-none focus-visible:ring-2 sm:text-sm'
              value={authMode}
              disabled={pending || !credentialsReady || Boolean(presetID)}
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
                  placeholder={
                    credentials.data?.configured &&
                    credentials.data.mode === authMode &&
                    initial?.version.endpoint === endpoint
                      ? t(
                          'Saved. Leave empty to keep it, or enter a replacement.'
                        )
                      : authMode === 'bearer'
                        ? t('Paste a Bearer token')
                        : t('Paste an API key')
                  }
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
              className='border-input bg-background focus-visible:ring-ring min-h-11 w-full rounded-md border px-3 text-base outline-none focus-visible:ring-2 sm:text-sm'
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
          <fieldset className='min-w-0 space-y-4 border-t pt-6'>
            <legend className='bg-background pe-3 text-base font-semibold'>
              {t('Tools and prices')}
            </legend>
            <p className='text-muted-foreground max-w-[70ch] text-sm leading-6'>
              {t(
                'Choose free or paid pricing for each tool. Charges apply only to successful calls; failed and expired calls are refunded.'
              )}
            </p>
            <p className='text-muted-foreground max-w-[70ch] text-sm leading-6'>
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
                  className='border-border min-w-0 space-y-4 border-b py-5'
                >
                  <div className='flex items-start gap-3'>
                    <Checkbox
                      className='mt-0.5 shrink-0'
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
                      className='min-w-0 cursor-pointer text-sm leading-6'
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
                    <div className='grid min-w-0 gap-4 sm:grid-cols-2 sm:ps-7'>
                      <Field>
                        <FieldLabel htmlFor={`billing-mode-${tool.name}`}>
                          {t('Billing mode')}
                        </FieldLabel>
                        <select
                          id={`billing-mode-${tool.name}`}
                          className='border-input bg-background focus-visible:ring-ring min-h-11 w-full rounded-md border px-3 text-base outline-none focus-visible:ring-2 sm:text-sm'
                          value={billingModes[tool.name] ?? 'free'}
                          disabled={pending || Boolean(tool.provider_pricing)}
                          onChange={(event) => {
                            const mode =
                              event.target.value === 'metered'
                                ? 'metered'
                                : event.target.value === 'input_tokens'
                                  ? 'input_tokens'
                                  : event.target.value === 'paid'
                                    ? 'paid'
                                    : 'free'
                            setBillingModes((current) => ({
                              ...current,
                              [tool.name]: mode,
                            }))
                            setPriceDrafts((current) => ({
                              ...current,
                              [tool.name]: {
                                key: inputCurrencyKey,
                                input: mode === 'free' ? '0' : '',
                              },
                            }))
                            setPrices((current) => ({
                              ...current,
                              [tool.name]: mode === 'free' ? '0' : '',
                            }))
                          }}
                        >
                          <option value='free'>{t('Free tool')}</option>
                          <option value='paid'>
                            {t(
                              tool.provider_pricing
                                ? 'Upstream quote × multiplier'
                                : 'Paid tool'
                            )}
                          </option>
                          <option
                            value='input_tokens'
                            disabled={
                              !tool.available_metering_metrics?.includes(
                                'input_tokens'
                              )
                            }
                          >
                            {t('Input token usage')}
                          </option>
                          <option
                            value='metered'
                            disabled={!tool.available_metering_metrics?.length}
                          >
                            {t('Combined usage pricing')}
                          </option>
                        </select>
                      </Field>
                      {billingModes[tool.name] !== 'metered' && (
                        <Field>
                          <FieldLabel htmlFor={`price-${tool.name}`}>
                            {t(
                              tool.provider_pricing
                                ? 'Maximum charge per call'
                                : billingModes[tool.name] === 'input_tokens'
                                  ? 'Price per million input tokens'
                                  : 'Price per successful call'
                            )}{' '}
                            ({label})
                          </FieldLabel>
                          <Input
                            id={`price-${tool.name}`}
                            inputMode='decimal'
                            type='number'
                            min='0'
                            step={step}
                            value={
                              priceDrafts[tool.name]?.key === inputCurrencyKey
                                ? priceDrafts[tool.name].input
                                : prices[tool.name] === ''
                                  ? ''
                                  : quotaToInput(
                                      Number(prices[tool.name] ?? '0')
                                    )
                            }
                            aria-invalid={priceQuota === undefined}
                            disabled={pending}
                            onChange={(e) => {
                              const raw = e.target.value
                              setPriceDrafts((current) => ({
                                ...current,
                                [tool.name]: {
                                  key: inputCurrencyKey,
                                  input: raw,
                                },
                              }))
                              let quota = ''
                              try {
                                quota = String(marketQuota(raw, amountToQuota))
                              } catch {
                                /* Invalid draft cannot be submitted. */
                              }
                              setPrices((current) => ({
                                ...current,
                                [tool.name]: quota,
                              }))
                              if (
                                Number(raw) > 0 &&
                                billingModes[tool.name] !== 'input_tokens'
                              ) {
                                setBillingModes((current) => ({
                                  ...current,
                                  [tool.name]: 'paid',
                                }))
                              }
                            }}
                          />
                          {billingModes[tool.name] !== 'free' &&
                            priceQuota === undefined && (
                              <FieldDescription className='text-destructive'>
                                {t('Enter a positive price for a paid tool.')}
                              </FieldDescription>
                            )}
                          {!tool.provider_pricing &&
                            billingModes[tool.name] !== 'input_tokens' &&
                            priceQuota !== undefined &&
                            feeBps !== undefined &&
                            Number.isSafeInteger(feeBps) &&
                            feeBps >= 0 &&
                            feeBps <= 10000 && (
                              <FieldDescription>
                                {t(
                                  'You receive {{amount}} per successful call after the {{fee}}% platform fee.',
                                  {
                                    amount: formatQuota(
                                      marketNetQuota(priceQuota, feeBps)
                                    ),
                                    fee: feeBps / 100,
                                  }
                                )}
                              </FieldDescription>
                            )}
                        </Field>
                      )}
                      {tool.provider_pricing && (
                        <p className='text-muted-foreground col-span-full text-sm'>
                          {t(
                            'The server checks the current USD quote before each call. This amount is a spending ceiling, not a fixed charge. Variable-price tools are blocked until a spending bound can be verified.'
                          )}
                        </p>
                      )}
                      {!tool.provider_pricing &&
                        !tool.available_metering_metrics?.length && (
                          <p className='text-muted-foreground col-span-full text-sm'>
                            {t(
                              'Choose the usage units the tool reports. Missing or invalid usage is not charged.'
                            )}
                          </p>
                        )}
                      {billingModes[tool.name] === 'metered' && (
                        <UsagePricingEditor
                          rules={tool.billing_rules ?? []}
                          metrics={tool.available_metering_metrics ?? []}
                          disabled={pending}
                          onChange={(rules) =>
                            setTools((current) =>
                              current.map((item) =>
                                item.name === tool.name
                                  ? { ...item, billing_rules: rules }
                                  : item
                              )
                            )
                          }
                        />
                      )}
                      {billingModes[tool.name] === 'input_tokens' && (
                        <Field>
                          <FieldLabel htmlFor={`token-limit-${tool.name}`}>
                            {t('Maximum input tokens per call')}
                          </FieldLabel>
                          <Input
                            id={`token-limit-${tool.name}`}
                            type='number'
                            min='1'
                            max='1000000'
                            step='1'
                            value={tool.max_input_tokens ?? 200000}
                            disabled={pending}
                            onChange={(e) =>
                              setTools((current) =>
                                current.map((item) =>
                                  item.name === tool.name
                                    ? {
                                        ...item,
                                        max_input_tokens: Number(
                                          e.target.value
                                        ),
                                      }
                                    : item
                                )
                              )
                            }
                          />
                          <FieldDescription>
                            {t(
                              'Reserve up to {{amount}}; charge actual input usage and release the remainder.',
                              { amount: formatQuota(priceQuota ?? 0) }
                            )}
                          </FieldDescription>
                        </Field>
                      )}
                      <fieldset className='flex min-w-0 flex-wrap gap-x-4 gap-y-2 text-sm sm:col-span-2'>
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
                            className='flex min-h-9 cursor-pointer items-center gap-2'
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
                      <details className='min-w-0 text-sm sm:col-span-2'>
                        <summary className='focus-visible:ring-ring w-fit cursor-pointer rounded-sm py-2 font-medium outline-none focus-visible:ring-2'>
                          {t('Parameter schema')}
                        </summary>
                        <pre
                          className='bg-muted mt-2 max-h-52 overflow-auto rounded-lg p-3 text-xs leading-5'
                          tabIndex={0}
                          aria-label={t('Parameter schema')}
                        >
                          {typeof tool.input_schema === 'string'
                            ? tool.input_schema
                            : JSON.stringify(tool.input_schema, null, 2)}
                        </pre>
                      </details>
                    </div>
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
          <div className='flex flex-wrap gap-2 border-t pt-4'>
            <Button
              type='submit'
              className='min-h-11'
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
              className='min-h-11'
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
