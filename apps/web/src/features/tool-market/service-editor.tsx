/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
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
  marketQuota,
  type DraftInput,
  type MarketDetail,
  type ToolInput,
} from './api'

export function ServiceEditor({
  initial,
  units,
  onSaved,
  onCancel,
}: {
  initial?: MarketDetail
  units: number
  onSaved: (id: string) => void
  onCancel: () => void
}) {
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
        input_schema: JSON.parse(tool.input_schema),
        ...(tool.output_schema
          ? { output_schema: JSON.parse(tool.output_schema) }
          : {}),
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
  const [inspectedEndpoint, setInspectedEndpoint] = useState(
    initial?.version.endpoint ?? ''
  )
  const [inspectVersion, setInspectVersion] = useState(0)
  const inspect = useMutation({
    retry: false,
    mutationFn: () => marketAPI.inspect(endpoint),
    onSuccess: (data) => {
      setTools(data)
      setSelected(data.map((tool) => tool.name))
      setPrices(Object.fromEntries(data.map((tool) => [tool.name, '0'])))
      setInspectedEndpoint(endpoint)
      setInspectVersion((value) => value + 1)
    },
  })
  const save = useMutation({
    retry: false,
    mutationFn: async () => {
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
      if (!inspectVersion || inspectedEndpoint !== endpoint) {
        throw new Error('Inspect the current endpoint before saving')
      }
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
      return marketAPI.save(initial?.service.id, input)
    },
    onSuccess: (service) => {
      void cache.invalidateQueries({ queryKey: ['tool-market'] })
      onSaved(service.id)
    },
  })
  const pending = inspect.isPending || save.isPending
  return (
    <section className='max-w-3xl space-y-6'>
      <div>
        <h3 className='text-lg font-semibold'>{t('Publish a tool service')}</h3>
        <p className='text-muted-foreground mt-2 text-sm'>
          {t(
            'Connect a public HTTPS MCP service. Services requiring credentials are not supported yet.'
          )}
        </p>
      </div>
      <form
        onSubmit={(event) => {
          event.preventDefault()
          save.mutate()
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
              placeholder='https://example.com/mcp'
              onChange={(e) => {
                setEndpoint(e.target.value)
                setTools([])
                setSelected([])
                setInspectedEndpoint('')
                setInspectVersion(0)
              }}
            />
            <FieldDescription>
              {t('Do not include API keys or tokens in the URL.')}
            </FieldDescription>
            <Button
              type='button'
              variant='outline'
              disabled={pending || !endpoint}
              onClick={() => inspect.mutate()}
            >
              {inspect.isPending ? t('Checking…') : t('Read tool definitions')}
            </Button>
          </Field>
          <Field>
            <FieldLabel htmlFor='market-visibility'>
              {t('Visibility')}
            </FieldLabel>
            <select
              id='market-visibility'
              className='border-input bg-background h-9 rounded-md border px-3 text-sm'
              value={visibility}
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
                onChange={(e) => setShared(e.target.value)}
              />
            </Field>
          )}
          {tools.length > 0 && (
            <fieldset className='space-y-4'>
              <legend className='mb-3 font-medium'>
                {t('Tools and prices')}
              </legend>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Select the tools to publish and review their permissions. Prices are per successful call in platform credits; failed and expired calls are refunded.'
                )}
              </p>
              {tools.map((tool) => (
                <div
                  key={tool.name}
                  className='border-border space-y-3 border-b pb-4'
                >
                  <div className='flex items-start gap-3'>
                    <Checkbox
                      id={`select-${tool.name}`}
                      checked={selected.includes(tool.name)}
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
                      <span className='text-muted-foreground mt-1 block whitespace-pre-wrap'>
                        {tool.description}
                      </span>
                    </label>
                  </div>
                  {selected.includes(tool.name) && (
                    <>
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
                          onChange={(e) =>
                            setPrices((current) => ({
                              ...current,
                              [tool.name]: e.target.value,
                            }))
                          }
                        />
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
                          {JSON.stringify(tool.input_schema, null, 2)}
                        </pre>
                      </details>
                    </>
                  )}
                </div>
              ))}
            </fieldset>
          )}
          {(save.isError || inspect.isError) && (
            <p role='alert' className='text-destructive text-sm'>
              {t(
                'The operation failed. Check the fields, endpoint and supported tool definitions, then retry.'
              )}
            </p>
          )}
          <div className='flex gap-2'>
            <Button
              type='submit'
              disabled={pending || !selected.length || !inspectVersion}
            >
              {save.isPending ? t('Saving…') : t('Save draft')}
            </Button>
            <Button
              type='button'
              variant='outline'
              onClick={onCancel}
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
