/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock as moduleMock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type { Root } from 'react-dom/client'
import type { UseFormReturn } from 'react-hook-form'

import type { GroupRatioOptionValues } from '../group-ratio-option-values'
import { formatGroupRatioValues } from '../group-ratio-save-state'

const domWindow = new Window({
  url: 'https://console.example.test/system-settings/billing/group-pricing',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Event',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})

const baseline: GroupRatioOptionValues = {
  GroupRatio: '{"default":1}',
  TopupGroupRatio: '{}',
  UserUsableGroups: '{"default":"Default"}',
  GroupGroupRatio: '{}',
  AutoGroups: '["default"]',
  MaxTokenAutoGroups: 4,
  DefaultUseAutoGroup: false,
  GroupSpecialUsableGroup: '{}',
  GroupWarnings: '{}',
}
const optionKeys: Record<string, string> = {
  GroupSpecialUsableGroup: 'group_ratio_setting.group_special_usable_group',
  GroupWarnings: 'group_ratio_setting.group_warnings',
}
type WriteResponse = { success: boolean; message: string }
type ReadResponse = WriteResponse & {
  data: Array<{ key: string; value: string }>
}
const ok: WriteResponse = { success: true, message: '' }
let stored: Record<string, string>
let writeImpl: () => Promise<WriteResponse>
let readImpl: () => Promise<ReadResponse>
const writes: Array<Record<string, string>> = []
const errors: string[] = []
const warnings: string[] = []
const successes: string[] = []
const t = (key: string) => key

function readStored(): ReadResponse {
  return {
    ...ok,
    data: Object.entries(stored).map(([key, value]) => ({ key, value })),
  }
}

moduleMock.module('../../api', () => ({
  getSystemOptions: () => readImpl(),
  updateSystemOptions: async (values: Record<string, string>) => {
    writes.push({ ...values })
    const response = await writeImpl()
    if (response.success) Object.assign(stored, values)
    return response
  },
}))
moduleMock.module('react-i18next', () => ({ useTranslation: () => ({ t }) }))
moduleMock.module('sonner', () => ({
  toast: {
    success: (message: string) => successes.push(message),
    error: (message: string) => errors.push(message),
    warning: (message: string) => warnings.push(message),
    info: () => {},
  },
}))

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider, notifyManager } = await import(
  '@tanstack/react-query'
)
const { useForm } = await import('react-hook-form')
const { useGroupRatioSettings } = await import('../use-group-ratio-settings')
notifyManager.setScheduler(queueMicrotask)

let root: Root
let container: HTMLDivElement
let queryClient: InstanceType<typeof QueryClient>
let form: UseFormReturn<GroupRatioOptionValues>
let state: ReturnType<typeof useGroupRatioSettings>

function Harness({ defaults }: { defaults: GroupRatioOptionValues }) {
  form = useForm<GroupRatioOptionValues>({
    defaultValues: formatGroupRatioValues(defaults),
  })
  // Subscribe to dirty-field changes so assertions cover reset/setValue behavior.
  const { dirtyFields } = form.formState
  state = useGroupRatioSettings(form, defaults)
  return <output>{Object.keys(dirtyFields).length}</output>
}

async function render(defaults = baseline) {
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <Harness defaults={defaults} />
      </QueryClientProvider>
    )
  })
}

async function edit(ratio: number) {
  await act(async () => {
    form.setValue('GroupRatio', JSON.stringify({ default: ratio }), {
      shouldDirty: true,
    })
  })
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((complete) => {
    resolve = complete
  })
  return { promise, resolve }
}

beforeEach(async () => {
  stored = Object.fromEntries(
    Object.entries(baseline).map(([key, value]) => [
      optionKeys[key] ?? key,
      String(value),
    ])
  )
  writeImpl = async () => ok
  readImpl = async () => readStored()
  writes.length = 0
  errors.length = 0
  warnings.length = 0
  successes.length = 0
  queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity },
      mutations: { retry: false },
    },
  })
  container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  await render()
})

afterEach(async () => {
  await act(async () => root.unmount())
  queryClient.clear()
  container.remove()
})
after(() => domWindow.close())

test('parent rerenders with equal defaults do not erase the draft', async () => {
  await edit(2)
  await render({ ...baseline })
  assert.equal(JSON.parse(form.getValues('GroupRatio')).default, 2)
  assert.equal(form.getFieldState('GroupRatio').isDirty, true)
  await act(async () => state.saveGroupRatios(form.getValues()))
  assert.deepEqual(writes, [{ GroupRatio: '{"default":2}' }])
  assert.equal(form.getFieldState('GroupRatio').isDirty, false)
})

test('refresh merges untouched fields without saving them again', async () => {
  await edit(2)
  await render({ ...baseline, MaxTokenAutoGroups: 6 })
  stored.MaxTokenAutoGroups = '6'
  assert.equal(form.getValues('MaxTokenAutoGroups'), 6)
  assert.equal(JSON.parse(form.getValues('GroupRatio')).default, 2)
  await act(async () => state.saveGroupRatios(form.getValues()))
  assert.deepEqual(writes, [{ GroupRatio: '{"default":2}' }])
})

test('business errors preserve the draft for retry', async () => {
  await edit(2)
  writeImpl = async () => ({ success: false, message: 'Invalid group' })
  await act(async () => state.saveGroupRatios(form.getValues()))
  assert.deepEqual(errors, ['Invalid group'])
  assert.equal(successes.length, 0)
  assert.equal(JSON.parse(form.getValues('GroupRatio')).default, 2)
  assert.equal(form.getFieldState('GroupRatio').isDirty, true)
  writeImpl = async () => ok
  await act(async () => state.saveGroupRatios(form.getValues()))
  assert.deepEqual(writes, [
    { GroupRatio: '{"default":2}' },
    { GroupRatio: '{"default":2}' },
  ])
})

test('network failures are handled and retain the saved baseline', async () => {
  await edit(2)
  writeImpl = async () => {
    throw new Error('Network unavailable')
  }
  await act(async () => state.saveGroupRatios(form.getValues()))
  assert.deepEqual(errors, ['Network unavailable'])
  assert.equal(form.getFieldState('GroupRatio').isDirty, true)
  writeImpl = async () => ok
  await act(async () => state.saveGroupRatios(form.getValues()))
  assert.equal(writes.length, 2)
})

test('same-tick double clicks send one atomic request', async () => {
  await edit(2)
  await act(async () => {
    form.setValue('GroupWarnings', '{"free":{"enabled":false}}', {
      shouldDirty: true,
    })
  })
  const started = deferred<void>()
  const gate = deferred<WriteResponse>()
  writeImpl = () => {
    started.resolve()
    return gate.promise
  }
  let first!: Promise<void>
  await act(async () => {
    first = state.saveGroupRatios(form.getValues())
    await state.saveGroupRatios(form.getValues())
    await started.promise
  })
  assert.deepEqual(writes, [
    {
      GroupRatio: '{"default":2}',
      'group_ratio_setting.group_warnings': '{"free":{"enabled":false}}',
    },
  ])
  assert.equal(state.isSaving, true)
  await act(async () => {
    gate.resolve(ok)
    await first
  })
  assert.equal(state.isSaving, false)
})

test('edits during a save remain pending for the next save', async () => {
  await edit(2)
  const started = deferred<void>()
  const gate = deferred<WriteResponse>()
  writeImpl = () => {
    started.resolve()
    return gate.promise
  }
  let saving!: Promise<void>
  await act(async () => {
    saving = state.saveGroupRatios(form.getValues())
    await started.promise
  })
  await edit(3)
  await render({ ...baseline })
  await act(async () => {
    gate.resolve(ok)
    await saving
  })
  assert.equal(JSON.parse(form.getValues('GroupRatio')).default, 3)
  assert.equal(form.getFieldState('GroupRatio').isDirty, true)
  writeImpl = async () => ok
  await act(async () => state.saveGroupRatios(form.getValues()))
  assert.deepEqual(writes, [
    { GroupRatio: '{"default":2}' },
    { GroupRatio: '{"default":3}' },
  ])
})

test('a failed refresh does not undo an acknowledged save', async () => {
  await edit(2)
  readImpl = async () => {
    throw new Error('Refresh unavailable')
  }
  await act(async () => state.saveGroupRatios(form.getValues()))
  await render({ ...baseline })
  assert.equal(JSON.parse(form.getValues('GroupRatio')).default, 2)
  assert.equal(errors.length, 0)
  assert.equal(warnings.length, 1)
  assert.equal(form.getFieldState('GroupRatio').isDirty, false)
  await act(async () => state.saveGroupRatios(form.getValues()))
  assert.equal(writes.length, 1)
})

test('the save remains pending until server readback completes', async () => {
  await edit(2)
  const started = deferred<void>()
  const gate = deferred<ReadResponse>()
  readImpl = () => {
    started.resolve()
    return gate.promise
  }
  let saving!: Promise<void>
  await act(async () => {
    saving = state.saveGroupRatios(form.getValues())
    await started.promise
  })
  assert.equal(state.isSaving, true)
  assert.equal(successes.length, 0)
  await act(async () => {
    gate.resolve(readStored())
    await saving
  })
  assert.equal(state.isSaving, false)
  assert.equal(successes.length, 1)
  assert.deepEqual(queryClient.getQueryData(['system-options']), readStored())
})
