/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'

import { storeApi } from './api'
import { StoreError } from './shared'
import type { StoreLink, StoreLinkPreset } from './types'
import { storeRequestKey } from './utils'

export function StoreLinkPresetChooser({
  presets,
  onAdd,
}: {
  presets: StoreLinkPreset[]
  onAdd: (link: StoreLink) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const [selected, setSelected] = useState('')
  const preset = presets.find((item) => item.id === selected)
  if (!presets.length) return null
  return (
    <Field>
      <FieldLabel htmlFor={id}>{t('Choose a link preset')}</FieldLabel>
      <div className='flex flex-wrap items-center gap-2'>
        <NativeSelect
          id={id}
          value={selected}
          onChange={(event) => setSelected(event.target.value)}
          className='min-w-40 flex-1'
        >
          <NativeSelectOption value=''>
            {t('Choose a link preset')}
          </NativeSelectOption>
          {presets.map((item) => (
            <NativeSelectOption key={item.id} value={item.id}>
              {item.title}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={!preset}
          onClick={() => {
            if (!preset) return
            const { title, url, description } = preset
            onAdd({ title, url, description })
            setSelected('')
          }}
        >
          {t('Add preset link')}
        </Button>
      </div>
    </Field>
  )
}

export function StoreLinkPresetsSettings({
  presets,
  onSaved,
}: {
  presets: StoreLinkPreset[]
  onSaved: () => Promise<void>
}) {
  const { t } = useTranslation()
  const id = useId()
  const [draft, setDraft] = useState(() =>
    presets.map((preset) => ({ ...preset }))
  )
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  function change(index: number, key: keyof StoreLink, value: string) {
    setDraft((current) =>
      current.map((item, position) =>
        position === index ? { ...item, [key]: value } : item
      )
    )
  }
  async function save(event: React.FormEvent) {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await storeApi.saveLinkPresets(draft)
      await onSaved()
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <form onSubmit={(event) => void save(event)} className='border-t pt-5'>
      <FieldSet disabled={busy}>
        <FieldLegend>{t('Product link presets')}</FieldLegend>
        <FieldDescription>
          {t(
            'Presets are public and copied into editable product links. Updating a preset does not change existing product links.'
          )}
        </FieldDescription>
        <StoreError error={error} />
        <FieldGroup>
          {draft.map((preset, index) => (
            <FieldSet key={preset.id} className='rounded-lg border p-4'>
              <FieldLegend variant='label'>
                {t('Link preset')} {index + 1}
              </FieldLegend>
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor={`${id}-${preset.id}-title`}>
                    {t('Link title')}
                  </FieldLabel>
                  <Input
                    id={`${id}-${preset.id}-title`}
                    value={preset.title}
                    required
                    maxLength={200}
                    onChange={(event) =>
                      change(index, 'title', event.target.value)
                    }
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor={`${id}-${preset.id}-url`}>
                    {t('Link URL')}
                  </FieldLabel>
                  <Input
                    id={`${id}-${preset.id}-url`}
                    type='url'
                    value={preset.url}
                    required
                    maxLength={4096}
                    onChange={(event) =>
                      change(index, 'url', event.target.value)
                    }
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor={`${id}-${preset.id}-description`}>
                    {t('Link description')}
                  </FieldLabel>
                  <Textarea
                    id={`${id}-${preset.id}-description`}
                    value={preset.description}
                    maxLength={4096}
                    onChange={(event) =>
                      change(index, 'description', event.target.value)
                    }
                  />
                </Field>
                <Button
                  type='button'
                  variant='outline'
                  onClick={() =>
                    setDraft((current) =>
                      current.filter((item) => item.id !== preset.id)
                    )
                  }
                >
                  {t('Remove')}
                </Button>
              </FieldGroup>
            </FieldSet>
          ))}
        </FieldGroup>
        <div className='flex flex-wrap gap-2'>
          <Button
            type='button'
            variant='outline'
            disabled={draft.length >= 50}
            onClick={() =>
              setDraft((current) => [
                ...current,
                { id: storeRequestKey(), title: '', url: '', description: '' },
              ])
            }
          >
            {t('Add link preset')}
          </Button>
          <Button type='submit'>
            {t(busy ? 'Saving...' : 'Save link presets')}
          </Button>
        </div>
      </FieldSet>
    </form>
  )
}
