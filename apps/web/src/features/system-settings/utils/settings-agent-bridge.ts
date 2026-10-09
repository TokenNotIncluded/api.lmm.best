/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
/** Explicitly supported, non-secret fields. No credentials, prices or access policies. */
export const SETTINGS_AGENT_FIELDS = {
  SystemName: {
    title: 'System Name',
    type: 'string',
    minLength: 1,
    maxLength: 80,
  },
  DisplayTokenStatEnabled: {
    title: 'Display Token Statistics',
    type: 'boolean',
  },
  DefaultCollapseSidebar: {
    title: 'Collapse sidebar by default',
    type: 'boolean',
  },
  DataExportEnabled: { title: 'Enable Data Dashboard', type: 'boolean' },
  DataExportInterval: {
    title: 'Refresh interval (minutes)',
    type: 'integer',
    minimum: 1,
    maximum: 1440,
  },
  DataExportDefaultTime: {
    title: 'Default time granularity',
    type: 'string',
    enum: ['hour', 'day', 'week'],
  },
} as const
export type SettingsAgentField = keyof typeof SETTINGS_AGENT_FIELDS
export type SettingValue = string | number | boolean

type FormAdapter = {
  owner: number
  path: string
  fields: readonly SettingsAgentField[]
  read: (field: SettingsAgentField) => unknown
  write: (field: SettingsAgentField, value: unknown) => void
  validate: (fields: SettingsAgentField[]) => Promise<boolean>
  isSaving: () => boolean
  changed: (field: SettingsAgentField) => boolean
  notify: () => void
}
const forms = new Map<string, FormAdapter>()
const pending = new Set<string>()

export function registerSettingsAgentForm(adapter: FormAdapter) {
  const id = crypto.randomUUID()
  forms.set(id, adapter)
  return () => {
    forms.delete(id)
    pending.delete(id)
  }
}

export function inspectSettingsAgentForms(owner: number, path: string) {
  return [...forms.entries()].flatMap(([id, form]) => {
    if (form.owner !== owner || form.path !== path) return []
    return [
      {
        id,
        path,
        saving: form.isSaving(),
        fields: form.fields.map((name) => ({
          name,
          ...SETTINGS_AGENT_FIELDS[name],
          value: validateValue(name, form.read(name)) ? form.read(name) : null,
          changed: form.changed(name),
        })),
      },
    ]
  })
}

function validateValue(name: SettingsAgentField, value: unknown) {
  const schema: Record<string, unknown> = SETTINGS_AGENT_FIELDS[name]
  if (schema.type === 'boolean') return typeof value === 'boolean'
  if (schema.type === 'integer') {
    return (
      typeof value === 'number' &&
      Number.isInteger(value) &&
      value >= Number(schema.minimum) &&
      value <= Number(schema.maximum)
    )
  }
  if (typeof value !== 'string') return false
  if (Array.isArray(schema.enum)) return schema.enum.includes(value)
  return (
    value.trim().length >= Number(schema.minLength) &&
    value.length <= Number(schema.maxLength) &&
    [...value].every(
      (char) => char.charCodeAt(0) >= 32 && char.charCodeAt(0) !== 127
    )
  )
}

/** Stage through the mounted form; never call a settings API or submit a form. */
export async function stageSettingsAgentForm(input: {
  id: string
  owner: number
  path: string
  changes: Record<string, unknown>
  signal: AbortSignal
  assertAccess: () => void
}) {
  input.assertAccess()
  input.signal.throwIfAborted()
  const form = forms.get(input.id)
  if (!form || form.owner !== input.owner || form.path !== input.path) {
    throw new Error(
      'Read the current form with lmm_settings_form before editing'
    )
  }
  if (form.isSaving() || pending.has(input.id)) {
    throw new Error(
      'The form is busy; read it again after the current action finishes'
    )
  }
  const changes = Object.entries(input.changes)
  if (!changes.length || changes.length > 6) {
    throw new TypeError('Provide between one and six supported fields')
  }
  // Validate the entire request before touching any field. Reject unknown keys,
  // inherited names, non-finite numbers and unsupported fields on this page.
  for (const [name, value] of changes) {
    if (
      !Object.hasOwn(SETTINGS_AGENT_FIELDS, name) ||
      !form.fields.includes(name as SettingsAgentField) ||
      !validateValue(name as SettingsAgentField, value)
    ) {
      throw new TypeError(
        `Unsupported field or invalid value: ${name.slice(0, 80)}`
      )
    }
  }
  const keys = changes.map(([name]) => name as SettingsAgentField)
  const previous = keys.map((name) => form.read(name))
  pending.add(input.id)
  try {
    changes.forEach(([name, value]) =>
      form.write(name as SettingsAgentField, value)
    )
    const valid = await form.validate(keys)
    input.signal.throwIfAborted()
    input.assertAccess()
    if (forms.get(input.id) !== form) {
      throw new Error('The settings form was closed; open it again')
    }
    if (!valid) {
      throw new Error(
        'The form rejected the proposed values; the draft was restored'
      )
    }
    form.notify()
    return {
      staged: keys,
      persisted: false,
      next: 'Review the visible draft and use the existing Save button to apply it.',
    }
  } catch (error) {
    if (forms.get(input.id) === form) {
      changes.forEach(([name, value], index) => {
        // Do not overwrite a newer human edit made during async validation.
        if (Object.is(form.read(name as SettingsAgentField), value)) {
          form.write(name as SettingsAgentField, previous[index])
        }
      })
    }
    throw error
  } finally {
    pending.delete(input.id)
  }
}
