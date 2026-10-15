/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'

import {
  getHoneypotOperation,
  getHoneypotPolicies,
  HoneypotFailure,
  saveReviewedHoneypotPolicies,
  type HoneypotScope,
} from './api'
import {
  configSchema,
  hashHoneypotConfig,
  DEFAULT_LIMITS,
  validatePolicyCapabilities,
  type HoneypotConfig,
  type HoneypotLimits,
  type HoneypotPolicies,
  type HoneypotPolicy,
  type HoneypotUpstream,
} from './contract'
import { HoneypotFeedback } from './feedback'

const limitLabels: Record<keyof HoneypotLimits, string> = {
  requests_per_minute_per_user: 'Requests per minute per user',
  requests_per_minute_global: 'Requests per minute globally',
  max_concurrent_per_user: 'Concurrent requests per user',
  max_concurrent_global: 'Concurrent requests globally',
  max_input_bytes: 'Maximum input bytes',
  max_output_bytes: 'Maximum output bytes',
  max_output_tokens: 'Maximum output tokens',
  output_bytes_per_second: 'Output bytes per second',
  total_timeout_seconds: 'Timeout (seconds)',
}
function Field(props: { label: string; children: ReactNode; wide?: boolean }) {
  const { t } = useTranslation()
  return (
    <Label
      className={`flex min-w-0 flex-col items-stretch gap-2 ${props.wide ? 'lg:col-span-2' : ''}`}
    >
      <span>{t(props.label)}</span>
      {props.children}
    </Label>
  )
}
function initialUpstream(snapshot: HoneypotPolicies): HoneypotUpstream {
  return {
    base_url: snapshot.capabilities.numeric_loopback_only
      ? 'http://127.0.0.1:8080/v1'
      : 'allowed_origins' in snapshot.capabilities &&
          snapshot.capabilities.allowed_origins?.[
            snapshot.capabilities.credential_references[0] ?? ''
          ]?.[0]
        ? `${snapshot.capabilities.allowed_origins[snapshot.capabilities.credential_references[0]][0]}/v1`
        : '',
    upstream_protocol:
      snapshot.capabilities.upstream_protocols[0] ?? 'openai_chat',
    model: '',
    credential_reference: snapshot.capabilities.credential_references[0] ?? '',
  }
}
export function HoneypotPolicyEditor(props: {
  initial: HoneypotPolicies
  scope: HoneypotScope
  onSnapshot?: (snapshot: HoneypotPolicies) => void
}) {
  const { t } = useTranslation()
  const [baseline, setBaseline] = useState(props.initial)
  const [draft, setDraft] = useState<HoneypotConfig>(() =>
    structuredClone(props.initial.config)
  )
  const [reason, setReason] = useState('')
  const [reviewing, setReviewing] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>()
  const [unknownOperation, setUnknownOperation] = useState<{
    id: string
    expectedRevision: number
    configHash: string
  }>()
  const [appliedRevision, setAppliedRevision] = useState<number>()
  const [needsReload, setNeedsReload] = useState(false)
  const valid =
    configSchema.safeParse(draft).success &&
    validatePolicyCapabilities(draft, baseline)
  const changed = JSON.stringify(draft) !== JSON.stringify(baseline.config)
  const blocked = pending || unknownOperation !== undefined
  const changeDraft = (updater: (value: HoneypotConfig) => HoneypotConfig) => {
    setDraft(updater)
    setReviewing(false)
    setAppliedRevision(undefined)
    setError(undefined)
  }
  const update = (index: number, policy: HoneypotPolicy) =>
    changeDraft((value) => ({
      ...value,
      policies: value.policies.map((item, i) => (i === index ? policy : item)),
    }))
  async function reload() {
    setPending(true)
    try {
      const current = await getHoneypotPolicies(props.scope)
      setBaseline(current)
      props.onSnapshot?.(current)
      setError(undefined)
      setReviewing(false)
      setNeedsReload(false)
    } catch (caught) {
      setError(caught)
    } finally {
      setPending(false)
    }
  }
  async function refreshAfterApplied(revision: number) {
    setAppliedRevision(revision)
    setUnknownOperation(undefined)
    setError(undefined)
    setReviewing(false)
    // The immutable receipt proves the write. A failed readback does not undo it.
    try {
      const current = await getHoneypotPolicies(props.scope)
      setBaseline(current)
      props.onSnapshot?.(current)
      setDraft(structuredClone(current.config))
      setReason('')
    } catch (caught) {
      setError(caught)
      setNeedsReload(true)
    }
  }
  async function save() {
    if (
      !reviewing ||
      blocked ||
      needsReload ||
      !valid ||
      !changed ||
      !reason.trim()
    ) {
      return
    }
    const id = `web-policy-${crypto.randomUUID()}`
    const expectedRevision = baseline.revision
    setPending(true)
    setError(undefined)
    let configHash: string | undefined
    try {
      configHash = await hashHoneypotConfig(draft)
      const result = await saveReviewedHoneypotPolicies(
        {
          operation_id: id,
          reason: reason.trim(),
          expected_revision: expectedRevision,
          config: draft,
        },
        props.scope,
        baseline
      )
      await refreshAfterApplied(result.policy_revision)
    } catch (caught) {
      setError(caught)
      if (
        caught instanceof HoneypotFailure &&
        caught.kind === 'unknown' &&
        configHash
      ) {
        setUnknownOperation({ id, expectedRevision, configHash })
      } else setReviewing(false)
      if (caught instanceof HoneypotFailure && caught.kind === 'conflict') {
        setNeedsReload(true)
      }
    } finally {
      setPending(false)
    }
  }
  async function checkOriginal() {
    if (!unknownOperation || pending) return
    setPending(true)
    try {
      const result = await getHoneypotOperation(
        unknownOperation.id,
        props.scope
      )
      if (
        result.kind !== 'policies' ||
        result.operation_id !== unknownOperation.id ||
        result.users.length ||
        result.policy_revision !== unknownOperation.expectedRevision + 1
      ) {
        throw new HoneypotFailure('unknown', unknownOperation.id)
      }
      if (
        result.kind !== 'policies' ||
        result.applied_config_hash !== unknownOperation.configHash
      ) {
        throw new HoneypotFailure('conflict', unknownOperation.id)
      }
      await refreshAfterApplied(result.policy_revision)
    } catch (caught) {
      setError(caught)
      if (caught instanceof HoneypotFailure && caught.kind === 'conflict') {
        setUnknownOperation(undefined)
        setNeedsReload(true)
        setReviewing(false)
      }
    } finally {
      setPending(false)
    }
  }
  return (
    <div className='space-y-5'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Only selected accounts use these policies. No users are selected automatically.'
        )}
      </p>
      <HoneypotFeedback
        error={error}
        operationID={unknownOperation?.id}
        check={unknownOperation ? () => void checkOriginal() : undefined}
        checking={pending}
      />
      {appliedRevision !== undefined && (
        <p role='status'>
          {t('Applied at revision {{revision}}', { revision: appliedRevision })}
        </p>
      )}
      <fieldset disabled={blocked || reviewing} className='min-w-0 space-y-5'>
        {draft.policies.length === 0 && <p>{t('No policies configured.')}</p>}
        {draft.policies.map((policy, index) => (
          <section key={index} className='space-y-4 border-b pb-5'>
            <div className='grid min-w-0 gap-4 lg:grid-cols-2'>
              <Field label='Policy ID'>
                <Input
                  value={policy.id}
                  maxLength={64}
                  onChange={(event) =>
                    update(index, { ...policy, id: event.target.value })
                  }
                />
              </Field>
              <Field label='Name'>
                <Input
                  value={policy.name}
                  maxLength={128}
                  onChange={(event) =>
                    update(index, { ...policy, name: event.target.value })
                  }
                />
              </Field>
              <Field label='Mode'>
                <NativeSelect
                  className='w-full'
                  value={policy.mode}
                  onChange={(event) =>
                    update(
                      index,
                      event.target.value === 'fixed_text'
                        ? {
                            id: policy.id,
                            name: policy.name,
                            mode: 'fixed_text',
                            public_models: policy.public_models,
                            fixed_text: '',
                            clients: {},
                            limits: policy.limits,
                          }
                        : {
                            id: policy.id,
                            name: policy.name,
                            mode: 'custom_upstream',
                            public_models: policy.public_models,
                            clients: {
                              openai_chat: initialUpstream(baseline),
                              openai_responses: initialUpstream(baseline),
                            },
                            limits: policy.limits,
                          }
                    )
                  }
                >
                  {baseline.capabilities.modes.map((mode) => (
                    <NativeSelectOption key={mode} value={mode}>
                      {t(
                        mode === 'fixed_text' ? 'Fixed text' : 'Custom upstream'
                      )}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </Field>
              <Field label='Public models'>
                <Textarea
                  rows={3}
                  value={policy.public_models.join('\n')}
                  onChange={(event) =>
                    update(index, {
                      ...policy,
                      public_models: event.target.value.split('\n'),
                    })
                  }
                  placeholder={t('One model name per line')}
                />
              </Field>
              {policy.mode === 'fixed_text' && (
                <Field label='Fixed text' wide>
                  <Textarea
                    rows={5}
                    value={policy.fixed_text}
                    maxLength={16384}
                    onChange={(event) =>
                      update(index, {
                        ...policy,
                        fixed_text: event.target.value,
                      })
                    }
                  />
                </Field>
              )}
            </div>
            {policy.mode === 'custom_upstream' && (
              <>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    baseline.capabilities.numeric_loopback_only
                      ? 'Use a numeric loopback HTTP server and a credential alias already loaded on the host.'
                      : 'Use HTTPS at an origin allowed for the selected host credential alias. Local connections require host approval.'
                  )}
                </p>
                {(['openai_chat', 'openai_responses'] as const).map(
                  (client) => {
                    const upstream = policy.clients[client]
                    const patch = (value: Partial<HoneypotUpstream>) =>
                      update(index, {
                        ...policy,
                        clients: {
                          ...policy.clients,
                          [client]: { ...upstream, ...value },
                        },
                      })
                    return (
                      <fieldset
                        key={client}
                        className='grid min-w-0 gap-4 border p-3 lg:grid-cols-2'
                      >
                        <legend className='px-1 text-sm font-medium'>
                          {client === 'openai_chat'
                            ? 'OpenAI Chat Completions'
                            : 'OpenAI Responses'}
                        </legend>
                        {!baseline.capabilities.numeric_loopback_only &&
                          'allowed_origins' in baseline.capabilities && (
                            <p className='text-xs break-all whitespace-pre-wrap lg:col-span-2'>
                              {t('Allowed origins for this alias')}:{' '}
                              {(
                                baseline.capabilities.allowed_origins?.[
                                  upstream.credential_reference
                                ] ?? []
                              ).join(', ') || t('None')}
                            </p>
                          )}
                        <Field label='Upstream URL'>
                          <Input
                            value={upstream.base_url}
                            onChange={(event) =>
                              patch({ base_url: event.target.value })
                            }
                          />
                        </Field>
                        <Field label='Upstream protocol'>
                          <NativeSelect
                            className='w-full'
                            value={upstream.upstream_protocol}
                            onChange={(event) =>
                              patch({
                                upstream_protocol: event.target
                                  .value as HoneypotUpstream['upstream_protocol'],
                              })
                            }
                          >
                            {baseline.capabilities.upstream_protocols.map(
                              (protocol) => (
                                <NativeSelectOption
                                  key={protocol}
                                  value={protocol}
                                >
                                  {protocol}
                                </NativeSelectOption>
                              )
                            )}
                          </NativeSelect>
                        </Field>
                        <Field label='Upstream model'>
                          <Input
                            value={upstream.model}
                            onChange={(event) =>
                              patch({ model: event.target.value })
                            }
                          />
                        </Field>
                        <Field label='Credential alias'>
                          <NativeSelect
                            className='w-full'
                            value={upstream.credential_reference}
                            onChange={(event) =>
                              patch({
                                credential_reference: event.target.value,
                              })
                            }
                          >
                            <NativeSelectOption value=''>
                              {t('Select a credential alias')}
                            </NativeSelectOption>
                            {baseline.capabilities.credential_references.map(
                              (alias) => (
                                <NativeSelectOption key={alias} value={alias}>
                                  {alias}
                                </NativeSelectOption>
                              )
                            )}
                          </NativeSelect>
                        </Field>
                      </fieldset>
                    )
                  }
                )}
              </>
            )}
            <div className='grid gap-4 lg:grid-cols-2'>
              {(Object.keys(limitLabels) as (keyof HoneypotLimits)[]).map(
                (key) => (
                  <Field key={key} label={limitLabels[key]}>
                    <Input
                      type='number'
                      step={1}
                      min={key === 'output_bytes_per_second' ? 0 : 1}
                      value={
                        Number.isFinite(policy.limits[key])
                          ? policy.limits[key]
                          : ''
                      }
                      onChange={(event) =>
                        update(index, {
                          ...policy,
                          limits: {
                            ...policy.limits,
                            [key]:
                              event.target.value === ''
                                ? Number.NaN
                                : Number(event.target.value),
                          },
                        })
                      }
                    />
                  </Field>
                )
              )}
            </div>
            <Button
              type='button'
              variant='outline'
              onClick={() =>
                changeDraft((value) => ({
                  ...value,
                  policies: value.policies.filter((_, i) => i !== index),
                }))
              }
            >
              {t('Remove policy')}
            </Button>
          </section>
        ))}
        <Button
          type='button'
          variant='outline'
          disabled={draft.policies.length >= 32}
          onClick={() =>
            changeDraft((value) => ({
              ...value,
              policies: [
                ...value.policies,
                {
                  id: '',
                  name: '',
                  mode: 'fixed_text',
                  public_models: [''],
                  fixed_text: '',
                  clients: {},
                  limits: { ...DEFAULT_LIMITS },
                },
              ],
            }))
          }
        >
          {t('Add policy')}
        </Button>
        <Field label='Audit reason'>
          <Textarea
            value={reason}
            maxLength={1000}
            onChange={(event) => setReason(event.target.value)}
          />
        </Field>
      </fieldset>
      {!valid && (
        <p role='alert' className='text-destructive text-sm'>
          {t(
            'Configuration or selection is invalid. Check the fields and limits.'
          )}
        </p>
      )}
      {reviewing && (
        <div className='space-y-2 border p-3'>
          <h3 className='font-medium'>{t('Review changes')}</h3>
          <p>{t('Revision {{revision}}', { revision: baseline.revision })}</p>
          <p className='break-words'>{reason}</p>
          <pre className='max-h-80 overflow-auto text-xs break-all whitespace-pre-wrap'>
            {JSON.stringify(draft, null, 2)}
          </pre>
        </div>
      )}
      <div className='flex flex-wrap gap-2'>
        {reviewing ? (
          <>
            <Button
              type='button'
              disabled={blocked}
              onClick={() => void save()}
            >
              {t('Save changes')}
            </Button>
            <Button
              type='button'
              variant='outline'
              disabled={blocked}
              onClick={() => setReviewing(false)}
            >
              {t('Back to editing')}
            </Button>
          </>
        ) : (
          <Button
            type='button'
            disabled={
              blocked || needsReload || !valid || !changed || !reason.trim()
            }
            onClick={() => {
              setReviewing(true)
              setError(undefined)
            }}
          >
            {t('Review changes')}
          </Button>
        )}
        <Button
          type='button'
          variant='outline'
          disabled={blocked}
          onClick={() => void reload()}
        >
          {t('Keep draft and reload revision')}
        </Button>
      </div>
    </div>
  )
}
