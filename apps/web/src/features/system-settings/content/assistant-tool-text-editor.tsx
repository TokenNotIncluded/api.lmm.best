/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import type { AssistantCatalogTool, AssistantToolRule } from './assistant-tool-policy'
import {
  renderAssistantToolDescription,
  validAssistantToolTextFields,
} from './assistant-tool-text'

export function AssistantToolTextEditor({ tool, rule, disabled, onChange }: {
  tool: AssistantCatalogTool
  rule: AssistantToolRule
  disabled?: boolean
  onChange: (value: Partial<AssistantToolRule>) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const [description, setDescription] = useState(rule.description)
  const [fields, setFields] = useState<Record<string, string>>(rule.parameter_descriptions ?? {})
  // Keep unfinished template input local. Typing '{{' must not invalidate the
  // parent form and close the configuration dialog on the next render.
  const savedText = JSON.stringify({ description: rule.description, fields: rule.parameter_descriptions ?? {} })
  useEffect(() => {
    const saved = JSON.parse(savedText) as { description?: string; fields: Record<string, string> }
    setDescription(saved.description)
    setFields(saved.fields)
  }, [tool.name, savedText])
  const schema = tool.text_schema
  if (!schema) {
    return <p className='text-muted-foreground text-sm'>{t('Update the server to edit tool descriptions.')}</p>
  }
  const values = { ...schema.variables, min_level: String(rule.min_level), max_level: String(rule.max_level) }
  const text = description ?? schema.description
  const known = new Set(schema.parameters.map((field) => field.path))
  const stale = Object.keys(fields).filter((path) => !known.has(path))
  const draft = {
    ...(description === undefined ? {} : { description }),
    parameter_descriptions: fields,
  }
  const valid = validAssistantToolTextFields(draft)
  const removeField = (path: string) => setFields((current) =>
    Object.fromEntries(Object.entries(current).filter(([key]) => key !== path))
  )
  const inputClass = 'bg-background w-full rounded-lg border px-3 py-2 text-sm leading-relaxed'
  return (
    <fieldset disabled={disabled} className='space-y-4 disabled:opacity-60' aria-labelledby={`${id}-heading`}>
      <legend id={`${id}-heading`} className='font-medium'>{t('Tool descriptions sent to AI')}</legend>
      <p className='text-muted-foreground text-sm'>{t('Edit guidance here. Account permissions, required fields and amount limits are still checked by the server.')}</p>
      <div className='space-y-2'>
        <label htmlFor={`${id}-description`}>{t('Tool description')}</label>
        <textarea id={`${id}-description`} className={inputClass} rows={6} value={text}
          onChange={(event) => setDescription(event.target.value)} />
        <Button type='button' variant='ghost' size='sm' disabled={disabled || description === undefined}
          onClick={() => setDescription(undefined)}>{t('Use default description')}</Button>
      </div>
      <details className='space-y-3'>
        <summary className='cursor-pointer text-sm font-medium'>{t('Description variables')}</summary>
        <p className='text-muted-foreground text-xs'>{t('The reward bound below is the last saved server value, in wallet ledger credits. A pending bound change is not reflected in this preview.')}</p>
        {Object.entries(values).map(([name, value]) => (
          <div key={name} className='flex flex-wrap justify-between gap-2 text-xs'>
            <code>{`{{${name}}}`}</code><span>{value}</span>
          </div>
        ))}
      </details>
      <details className='space-y-3'>
        <summary className='cursor-pointer text-sm font-medium'>{t('Parameter descriptions')}</summary>
        {schema.parameters.map((field, index) => (
          <div key={field.path} className='space-y-2 py-2'>
            <label htmlFor={`${id}-field-${index}`} className='block font-mono text-xs break-all'>
              {field.path || t('Parameters object')}
            </label>
            <textarea id={`${id}-field-${index}`} rows={3} className={inputClass}
              value={fields[field.path] ?? field.description}
              onChange={(event) => setFields((current) => ({ ...current, [field.path]: event.target.value }))} />
            <Button type='button' variant='ghost' size='sm' disabled={disabled || !Object.hasOwn(fields, field.path)}
              onClick={() => removeField(field.path)}>{t('Use default description')}</Button>
          </div>
        ))}
        {stale.map((path) => (
          <div key={path} className='space-y-1 text-xs'>
            <p>{t('This field no longer exists. Its override has no effect.')}</p>
            <code className='break-all'>{path || '/'}</code>
            <Button type='button' size='sm' variant='ghost' onClick={() => removeField(path)}>{t('Remove unused description')}</Button>
          </div>
        ))}
      </details>
      <details className='space-y-2'>
        <summary className='cursor-pointer text-sm font-medium'>{t('AI description preview')}</summary>
        <pre className='bg-muted/30 rounded-lg p-3 text-xs whitespace-pre-wrap'>{renderAssistantToolDescription(text, values)}</pre>
        {Object.entries(fields).filter(([path]) => known.has(path)).map(([path, value]) => (
          <div key={path} className='space-y-1 text-xs'>
            <code className='break-all'>{path || t('Parameters object')}</code>
            <p className='whitespace-pre-wrap'>{renderAssistantToolDescription(value, values)}</p>
          </div>
        ))}
      </details>
      {!valid && <p role='alert' className='text-destructive text-sm'>{t('Use only the listed variables. Tool descriptions allow 4096 bytes; each parameter description allows 2048 bytes; at most 128 parameter overrides are allowed.')}</p>}
      <div className='flex flex-wrap gap-2'>
        <Button type='button' variant='secondary' disabled={disabled || !valid} onClick={() => onChange({
          description,
          parameter_descriptions: Object.keys(fields).length ? fields : undefined,
        })}>{t('Apply descriptions to draft')}</Button>
        <Button type='button' variant='ghost' disabled={disabled} onClick={() => {
          setDescription(undefined)
          setFields({})
        }}>{t('Restore default descriptions')}</Button>
      </div>
      <p className='text-muted-foreground text-xs'>{t('Apply descriptions to the draft, then save assistant settings. Closing this dialog without applying discards these text edits.')}</p>
    </fieldset>
  )
}
