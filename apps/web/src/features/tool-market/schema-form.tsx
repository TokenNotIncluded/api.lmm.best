/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import {
  guidedParameters,
  parameterValue,
  readParameterSchema,
  schemaObject,
  updateArgument,
} from './schema-form-utils'

export function ToolArgumentsForm({
  schema,
  value,
  onChange,
  disabled,
}: {
  schema: string
  value: string
  onChange: (value: string) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const definition = readParameterSchema(schema)
  const fields = guidedParameters(definition)
  const [advanced, setAdvanced] = useState(fields.length === 0)
  const [fieldError, setFieldError] = useState(false)
  let parsed: Record<string, unknown> = {}
  let validObject = false
  try {
    const data: unknown = JSON.parse(value)
    if (schemaObject(data)) {
      parsed = data
      validObject = true
    }
  } catch {
    // Malformed advanced input remains editable, without discarding it.
  }
  const change = (name: string, data: unknown) => {
    try {
      onChange(updateArgument(value, name, data))
      setFieldError(false)
    } catch {
      setFieldError(true)
    }
  }
  return (
    <div className='space-y-4'>
      {!!fields.length && (
        <div className='flex flex-wrap gap-2'>
          <Button
            type='button'
            variant={!advanced ? 'secondary' : 'ghost'}
            disabled={disabled || !validObject}
            onClick={() => setAdvanced(false)}
          >
            {t('Parameter form')}
          </Button>
          <Button
            type='button'
            variant={advanced ? 'secondary' : 'ghost'}
            disabled={disabled}
            onClick={() => setAdvanced(true)}
          >
            {t('Advanced JSON')}
          </Button>
        </div>
      )}
      {advanced || !validObject ? (
        <Field>
          <FieldLabel htmlFor={`${id}-json`}>
            {t('Arguments (JSON)')}
          </FieldLabel>
          <Textarea
            id={`${id}-json`}
            className='min-h-32 font-mono text-xs'
            value={value}
            disabled={disabled}
            onChange={(event) => onChange(event.target.value)}
          />
        </Field>
      ) : (
        fields.map(([name, field], index) => {
          const fieldID = `${id}-${index}`
          const current = Object.hasOwn(parsed, name) ? parsed[name] : undefined
          const required =
            Array.isArray(definition?.required) &&
            definition.required.includes(name)
          const options = Array.isArray(field.enum) ? field.enum : null
          return (
            <Field key={name}>
              <FieldLabel htmlFor={fieldID}>
                {typeof field.title === 'string' ? field.title : name}
                {required && (
                  <span className='text-muted-foreground'>{t('Required')}</span>
                )}
              </FieldLabel>
              {options || field.type === 'boolean' ? (
                <select
                  id={fieldID}
                  className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
                  disabled={disabled}
                  value={
                    current === undefined
                      ? ''
                      : options
                        ? String(
                            options.findIndex(
                              (item) =>
                                JSON.stringify(item) === JSON.stringify(current)
                            )
                          )
                        : String(current)
                  }
                  onChange={(event) =>
                    change(
                      name,
                      event.target.value === ''
                        ? undefined
                        : options
                          ? options[Number(event.target.value)]
                          : event.target.value === 'true'
                    )
                  }
                >
                  <option value=''>{t('Not set')}</option>
                  {options ? (
                    options.map((item, option) => (
                      <option key={option} value={option}>
                        {typeof item === 'string' ? item : JSON.stringify(item)}
                      </option>
                    ))
                  ) : (
                    <>
                      <option value='false'>{t('No')}</option>
                      <option value='true'>{t('Yes')}</option>
                    </>
                  )}
                </select>
              ) : field.type === 'object' ||
                field.type === 'array' ||
                !['string', 'number', 'integer'].includes(
                  String(field.type)
                ) ? (
                <div>
                  {current !== undefined && (
                    <pre className='bg-muted max-h-32 overflow-auto rounded-md p-2 text-xs'>
                      {JSON.stringify(current, null, 2)}
                    </pre>
                  )}
                  <Button
                    type='button'
                    variant='outline'
                    disabled={disabled}
                    onClick={() => setAdvanced(true)}
                  >
                    {t('Edit in advanced JSON')}
                  </Button>
                </div>
              ) : (
                <Input
                  id={fieldID}
                  disabled={disabled}
                  value={current === undefined ? '' : String(current)}
                  type='text'
                  inputMode={
                    field.type === 'string'
                      ? undefined
                      : field.type === 'integer'
                        ? 'numeric'
                        : 'decimal'
                  }
                  onChange={(event) =>
                    change(name, parameterValue(field, event.target.value))
                  }
                />
              )}
              {typeof field.description === 'string' && (
                <FieldDescription>{field.description}</FieldDescription>
              )}
            </Field>
          )
        })
      )}
      {fieldError && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Enter valid JSON.')}
        </p>
      )}
    </div>
  )
}
