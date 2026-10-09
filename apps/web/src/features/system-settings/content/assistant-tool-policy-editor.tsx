/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { api } from '@/lib/api'

import {
  DEFAULT_ASSISTANT_TOOL_POLICY,
  assistantPolicyMatchesCatalog,
  isAssistantToolEnabled,
  parseAssistantToolCatalog,
  parseAssistantToolPolicy,
  updateAssistantToolPolicy,
  type AssistantToolAccess,
  type AssistantToolEffect,
} from './assistant-tool-policy'

const effectLabels: Record<AssistantToolEffect, string> = {
  read_only: 'Read only',
  navigation: 'Console navigation',
  confirmation: 'Requires confirmation',
  server_guarded: 'Writes after server checks',
}
const accessLabels: Record<AssistantToolAccess, string> = {
  user: 'Signed-in users',
  l0: 'L0 users',
  l1: 'L1 users',
  admin: 'Administrators',
  root: 'Super administrators',
  mixed: 'Depends on the action',
}

export function AssistantToolPolicyEditor(props: {
  value: string
  onChange: (value: string) => void
  active: boolean
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const catalog = useQuery({
    queryKey: ['assistant-admin-tool-catalog'],
    queryFn: async () => {
      const response = await api.get<unknown>(
        '/api/assistant/admin/tool-catalog'
      )
      const groups = parseAssistantToolCatalog(response.data)
      if (!groups) throw new Error('Invalid assistant tool catalog')
      return groups
    },
    enabled: props.active,
    staleTime: 60_000,
    retry: false,
  })
  const policy = parseAssistantToolPolicy(props.value)
  const groups = catalog.data
  const valid =
    policy !== null &&
    (!groups || assistantPolicyMatchesCatalog(policy, groups))
  const query = search.trim().toLocaleLowerCase()
  const filteredGroups = groups
    ?.map((group) => ({
      ...group,
      visibleTools: group.tools.filter((tool) =>
        [
          group.id,
          t(group.label),
          tool.name,
          t(tool.label),
          t(tool.description),
          t(effectLabels[tool.effect]),
          t(accessLabels[tool.access]),
        ].some((text) => text.toLocaleLowerCase().includes(query))
      ),
    }))
    .filter((group) => group.visibleTools.length > 0)
  const total =
    groups?.reduce((count, group) => count + group.tools.length, 0) ?? 0
  const enabled =
    policy &&
    groups?.reduce(
      (count, group) =>
        count +
        group.tools.filter((tool) =>
          isAssistantToolEnabled(policy, group.id, tool.name)
        ).length,
      0
    )

  return (
    <div className='space-y-4' data-testid='assistant-tool-policy-editor'>
      <div className='space-y-1'>
        <h3 className='text-sm font-medium'>{t('Built-in assistant tools')}</h3>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Choose which tools the assistant can use. Turning a group off preserves its individual tool settings.'
          )}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Enabled tools still require the user permissions, confirmation, and server checks shown below.'
          )}
        </p>
      </div>
      {!valid && (
        <div
          className='border-destructive/30 rounded-lg border p-3'
          role='alert'
        >
          <p className='text-destructive text-sm'>
            {t(
              'The saved tool policy is invalid. Tool switches are unavailable until you reset the policy or reload valid settings.'
            )}
          </p>
          <Button
            className='mt-2'
            type='button'
            variant='outline'
            size='sm'
            disabled={props.disabled}
            onClick={() => props.onChange(DEFAULT_ASSISTANT_TOOL_POLICY)}
          >
            {t('Reset tool policy to defaults')}
          </Button>
        </div>
      )}
      {catalog.isError ? (
        <div
          className='border-destructive/30 flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3'
          role='alert'
        >
          <p className='text-destructive text-sm'>
            {t('Unable to load assistant tools. Please try again.')}
          </p>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={() => void catalog.refetch()}
            disabled={catalog.isFetching}
          >
            {t('Retry')}
          </Button>
        </div>
      ) : catalog.isPending ? (
        <p className='text-muted-foreground text-sm' role='status'>
          {t('Loading assistant tools...')}
        </p>
      ) : groups ? (
        <>
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <Input
              className='max-w-md'
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder={t('Search tools, groups, or permissions')}
              aria-label={t('Search assistant tools')}
              autoComplete='off'
            />
            {valid && (
              <span className='text-muted-foreground text-sm' role='status'>
                {t('Enabled {{enabled}} of {{total}} tools', {
                  enabled,
                  total,
                })}
              </span>
            )}
          </div>
          {groups.length === 0 ? (
            <p className='text-muted-foreground text-sm'>
              {t('No assistant tools are available.')}
            </p>
          ) : filteredGroups?.length === 0 ? (
            <p className='text-muted-foreground text-sm'>
              {t('No tools match your search.')}
            </p>
          ) : (
            filteredGroups?.map((group) => {
              const groupEnabled = valid && policy?.groups[group.id] !== false
              const groupCount =
                valid && policy
                  ? group.tools.filter((tool) =>
                      isAssistantToolEnabled(policy, group.id, tool.name)
                    ).length
                  : 0
              return (
                <fieldset
                  key={group.id}
                  className='min-w-0 rounded-lg border'
                  data-tool-group={group.id}
                >
                  <legend className='sr-only'>{t(group.label)}</legend>
                  <div className='bg-muted/20 flex items-center justify-between gap-4 rounded-t-lg p-3'>
                    <div className='min-w-0 space-y-1'>
                      <label
                        className='text-sm font-medium'
                        htmlFor={`assistant-tool-group-${group.id}`}
                      >
                        {t(group.label)}
                      </label>
                      {valid && (
                        <p className='text-muted-foreground text-xs'>
                          {t('Enabled {{enabled}} of {{total}} tools', {
                            enabled: groupCount,
                            total: group.tools.length,
                          })}
                        </p>
                      )}
                    </div>
                    <Switch
                      id={`assistant-tool-group-${group.id}`}
                      checked={groupEnabled}
                      disabled={props.disabled || !valid}
                      aria-label={t('Enable tool group {{group}}', {
                        group: t(group.label),
                      })}
                      onCheckedChange={(checked) => {
                        if (valid && policy) {
                          props.onChange(
                            updateAssistantToolPolicy(
                              policy,
                              'groups',
                              group.id,
                              checked
                            )
                          )
                        }
                      }}
                    />
                  </div>
                  {!groupEnabled && valid && (
                    <p className='text-muted-foreground px-3 pt-3 text-xs'>
                      {t(
                        'This group is off. Enable it to use or change its individual tools.'
                      )}
                    </p>
                  )}
                  <div className='divide-y'>
                    {group.visibleTools.map((tool) => (
                      <div
                        key={tool.name}
                        className='flex items-start justify-between gap-4 p-3'
                        data-tool-name={tool.name}
                      >
                        <div className='min-w-0 space-y-1.5'>
                          <label
                            className='block text-sm font-medium'
                            htmlFor={`assistant-tool-${tool.name}`}
                          >
                            {t(tool.label)}
                          </label>
                          <p className='text-muted-foreground text-sm'>
                            {t(tool.description)}
                          </p>
                          <div className='flex flex-wrap gap-1.5'>
                            <Badge variant='secondary'>
                              {t(effectLabels[tool.effect])}
                            </Badge>
                            <Badge variant='outline'>
                              {t(accessLabels[tool.access])}
                            </Badge>
                            <span className='text-muted-foreground font-mono text-xs break-all'>
                              {tool.name}
                            </span>
                          </div>
                          {!groupEnabled && valid && (
                            <p className='text-muted-foreground text-xs'>
                              {t('Saved tool choice: {{choice}}', {
                                choice: t(
                                  policy?.tools[tool.name] === false
                                    ? 'Off'
                                    : 'On'
                                ),
                              })}
                            </p>
                          )}
                        </div>
                        <Switch
                          id={`assistant-tool-${tool.name}`}
                          className='mt-0.5'
                          checked={
                            valid && policy
                              ? isAssistantToolEnabled(
                                  policy,
                                  group.id,
                                  tool.name
                                )
                              : false
                          }
                          disabled={props.disabled || !valid || !groupEnabled}
                          aria-label={t('Enable tool {{tool}}', {
                            tool: t(tool.label),
                          })}
                          onCheckedChange={(checked) => {
                            if (valid && policy && groupEnabled) {
                              props.onChange(
                                updateAssistantToolPolicy(
                                  policy,
                                  'tools',
                                  tool.name,
                                  checked
                                )
                              )
                            }
                          }}
                        />
                      </div>
                    ))}
                  </div>
                </fieldset>
              )
            })
          )}
        </>
      ) : null}
    </div>
  )
}
