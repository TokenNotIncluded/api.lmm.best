/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import { MODERATION_MODELS } from './moderation-config'
import { getModerationModels } from './security-audit-api'

export function ModerationRouteFields(props: {
  group: string
  model: string
  groups: string[]
  disabled?: boolean
  idPrefix: string
  onGroupChange: (group: string) => void
  onModelChange: (model: string) => void
}) {
  const { t } = useTranslation()
  const modelsQuery = useQuery({
    queryKey: ['moderation-routing-models', props.group],
    queryFn: () => getModerationModels(props.group),
    enabled: !props.disabled && Boolean(props.group),
    staleTime: 60_000,
    retry: false,
  })
  const models = (modelsQuery.data ?? []).filter((model) =>
    MODERATION_MODELS.includes(model as (typeof MODERATION_MODELS)[number])
  )
  const groupOptions = [
    ...new Set([...props.groups, props.group].filter(Boolean)),
  ].sort()
  const isUnavailable =
    modelsQuery.data !== undefined && !models.includes(props.model)
  return (
    <div
      className='grid gap-5 sm:grid-cols-2 lg:col-span-2'
      data-testid={`${props.idPrefix}-route-fields`}
    >
      <div className='space-y-2'>
        <Label htmlFor={`${props.idPrefix}-group`}>
          {t('Review routing group')}
        </Label>
        <Select
          value={props.group}
          onValueChange={(value) => value && props.onGroupChange(value)}
          disabled={props.disabled}
        >
          <SelectTrigger id={`${props.idPrefix}-group`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              {groupOptions.map((group) => (
                <SelectItem key={group} value={group}>
                  {group}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Choose the OpenAI channel group used to send moderation requests.'
          )}
        </p>
      </div>
      <div className='space-y-2'>
        <Label htmlFor={`${props.idPrefix}-model`}>
          {t('Moderation model')}
        </Label>
        <Select
          value={props.model}
          onValueChange={(value) => value && props.onModelChange(value)}
          disabled={props.disabled}
        >
          <SelectTrigger id={`${props.idPrefix}-model`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              {[...new Set([...models, props.model].filter(Boolean))].map(
                (model) => (
                  <SelectItem
                    key={model}
                    value={model}
                    disabled={!models.includes(model)}
                  >
                    {model}
                  </SelectItem>
                )
              )}
            </SelectGroup>
          </SelectContent>
        </Select>
        <div className='flex flex-wrap items-center gap-2'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={props.disabled || modelsQuery.isFetching}
            onClick={() => void modelsQuery.refetch()}
          >
            <RefreshCw className='size-3.5' />
            {t('Get model list')}
          </Button>
          <span className='text-muted-foreground text-xs'>
            {t('Example: omni-moderation-latest')}
          </span>
        </div>
        {modelsQuery.isError ? (
          <p className='text-destructive text-xs' role='alert'>
            {t('Unable to load moderation models. Try again.')}
          </p>
        ) : isUnavailable ? (
          <p className='text-warning text-xs' role='status'>
            {t(
              'This moderation model is not available through an official OpenAI channel in this group.'
            )}
          </p>
        ) : null}
      </div>
    </div>
  )
}
