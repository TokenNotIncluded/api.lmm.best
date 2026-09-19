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
import {
  AlertTriangle,
  ChevronDown,
  GripVertical,
  Info,
  Plus,
  Trash2,
} from 'lucide-react'
import {
  useState,
  useMemo,
  useEffect,
  useCallback,
  memo,
  type ReactNode,
} from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { StaticRowActions } from '@/components/data-table/static/static-row-actions'
import { Dialog } from '@/components/dialog'
import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'

import { safeJsonParse } from '../utils/json-parser'

type GroupRatioVisualEditorProps = {
  groupRatio: string
  topupGroupRatio: string
  userUsableGroups: string
  groupGroupRatio: string
  autoGroups: string
  maxTokenAutoGroupsField?: ReactNode
  groupSpecialUsableGroup: string
  onChange: (field: string, value: string) => void
}

type GroupPricingRow = {
  _id: string
  name: string
  ratio: string
  topupRatio: string
  selectable: boolean
  description: string
}

type RegistryEntry = {
  name: string
  ratio: number
}

type GroupOverride = {
  targetGroup: string
  ratio: number | null
}

type RatioMapParseResult = {
  map: Record<string, number>
  isValid: boolean
}

type UsableMapParseResult = {
  map: Record<string, string>
  isValid: boolean
}

type NestedRatioMapParseResult = {
  map: Record<string, Record<string, number>>
  isValid: boolean
  invalidUserGroups: Set<string>
}

type AutoGroupsParseResult = {
  groups: string[]
  isValid: boolean
}

const sectionCardClassName =
  'relative rounded-none shadow-none ring-0 before:pointer-events-none before:absolute before:inset-0 before:rounded-none before:border before:border-border/90'
const sectionHeaderClassName = 'border-b bg-muted/20'

let groupPricingIdCounter = 0
function createGroupPricingId() {
  groupPricingIdCounter += 1
  return `gpr_${groupPricingIdCounter}`
}

function normalizeRatio(value: unknown): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 1
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return false
  }
  const prototype = Object.getPrototypeOf(value)
  return prototype === Object.prototype || prototype === null
}

function parseJsonValue(value: string): { value: unknown; isValid: boolean } {
  if (value.trim() === '') return { value: {}, isValid: true }
  try {
    return { value: JSON.parse(value), isValid: true }
  } catch {
    return { value: undefined, isValid: false }
  }
}

function parseRatioMapResult(value: string): RatioMapParseResult {
  const parsed = parseJsonValue(value)
  if (!parsed.isValid || !isPlainObject(parsed.value)) {
    return { map: {}, isValid: false }
  }

  const map: Record<string, number> = {}
  let isValid = true
  for (const [name, ratio] of Object.entries(parsed.value)) {
    if (typeof ratio !== 'number' || !Number.isFinite(ratio) || ratio < 0) {
      isValid = false
      continue
    }
    map[name] = ratio
  }
  return { map, isValid }
}

function parseUsableMapResult(value: string): UsableMapParseResult {
  const parsed = parseJsonValue(value)
  if (!parsed.isValid || !isPlainObject(parsed.value)) {
    return { map: {}, isValid: false }
  }

  const map: Record<string, string> = {}
  let isValid = true
  for (const [name, description] of Object.entries(parsed.value)) {
    if (typeof description !== 'string') {
      isValid = false
      continue
    }
    map[name] = description
  }
  return { map, isValid }
}

function parseNestedRatioMapResult(value: string): NestedRatioMapParseResult {
  const parsed = parseJsonValue(value)
  if (!parsed.isValid || !isPlainObject(parsed.value)) {
    return { map: {}, isValid: false, invalidUserGroups: new Set() }
  }

  const map: Record<string, Record<string, number>> = {}
  const invalidUserGroups = new Set<string>()
  let isValid = true
  for (const [userGroup, rawOverrides] of Object.entries(parsed.value)) {
    if (!isPlainObject(rawOverrides)) {
      isValid = false
      invalidUserGroups.add(userGroup)
      continue
    }
    const overrides: Record<string, number> = {}
    for (const [targetGroup, ratio] of Object.entries(rawOverrides)) {
      if (typeof ratio !== 'number' || !Number.isFinite(ratio) || ratio < 0) {
        isValid = false
        invalidUserGroups.add(userGroup)
        continue
      }
      overrides[targetGroup] = ratio
    }
    map[userGroup] = overrides
  }
  return { map, isValid, invalidUserGroups }
}

function parseAutoGroupsResult(value: string): AutoGroupsParseResult {
  if (value.trim() === '') return { groups: [], isValid: true }
  const parsed = parseJsonValue(value)
  if (!parsed.isValid || !Array.isArray(parsed.value)) {
    return { groups: [], isValid: false }
  }
  if (!parsed.value.every((group) => typeof group === 'string')) {
    return { groups: [], isValid: false }
  }
  return { groups: parsed.value, isValid: true }
}

function stableNameCompare(left: string, right: string): number {
  if (left < right) return -1
  if (left > right) return 1
  return 0
}

function compareEffectiveRatio(
  left: Pick<RegistryEntry, 'name' | 'ratio'>,
  right: Pick<RegistryEntry, 'name' | 'ratio'>
): number {
  if (left.ratio !== right.ratio) return left.ratio - right.ratio
  return stableNameCompare(left.name, right.name)
}

function buildOptimizedAutoGroups(
  registry: RegistryEntry[],
  userGroupOverrides?: Record<string, number>
): string[] {
  const knownGroups = new Map<string, RegistryEntry>()
  for (const entry of registry) {
    const name = entry.name.trim()
    if (!name || knownGroups.has(name)) continue
    const override = parseOverrideRatio(userGroupOverrides?.[name])
    knownGroups.set(name, {
      name,
      ratio: override ?? normalizeRatio(entry.ratio),
    })
  }

  return [...knownGroups.values()]
    .sort(compareEffectiveRatio)
    .map((entry) => entry.name)
}

function parseOverrideRatio(value: unknown): number | null {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : null
}

function isValidRequiredRatio(value: string): boolean {
  const parsed = Number(value)
  return value.trim() !== '' && Number.isFinite(parsed) && parsed >= 0
}

function isValidOptionalRatio(value: string): boolean {
  return value.trim() === '' || isValidRequiredRatio(value)
}

function compareOverrides(left: GroupOverride, right: GroupOverride): number {
  if (left.ratio === null && right.ratio !== null) return 1
  if (left.ratio !== null && right.ratio === null) return -1
  if (
    left.ratio !== null &&
    right.ratio !== null &&
    left.ratio !== right.ratio
  ) {
    return left.ratio - right.ratio
  }
  return stableNameCompare(left.targetGroup, right.targetGroup)
}

function parseRatioMap(value: string): Record<string, number> {
  return parseRatioMapResult(value).map
}

function parseUsableMap(value: string): Record<string, string> {
  return parseUsableMapResult(value).map
}

function parseNestedRatioMap(
  value: string
): Record<string, Record<string, number>> {
  return parseNestedRatioMapResult(value).map
}

function buildGroupPricingRows(
  groupRatio: string,
  userUsableGroups: string,
  topupGroupRatio: string
): GroupPricingRow[] {
  const ratioMap = parseRatioMap(groupRatio)
  const usableMap = parseUsableMap(userUsableGroups)
  const topupMap = parseRatioMap(topupGroupRatio)
  const names = new Set([
    ...Object.keys(ratioMap),
    ...Object.keys(usableMap),
    ...Object.keys(topupMap),
  ])

  return [...names]
    .filter((name) => name.trim() !== '')
    .map((name) => {
      const topupRatio = Number(topupMap[name])
      return {
        _id: createGroupPricingId(),
        name,
        ratio: String(normalizeRatio(ratioMap[name])),
        topupRatio:
          Object.hasOwn(topupMap, name) &&
          Number.isFinite(topupRatio) &&
          topupRatio >= 0
            ? String(topupRatio)
            : '',
        selectable: Object.hasOwn(usableMap, name),
        description: String(usableMap[name] ?? ''),
      }
    })
}

function serializeGroupPricingRows(rows: GroupPricingRow[]) {
  const groupRatio: Record<string, number> = {}
  const userUsableGroups: Record<string, string> = {}
  const topupGroupRatio: Record<string, number> = {}

  for (const row of rows) {
    const name = row.name.trim()
    if (!name) continue
    groupRatio[name] = normalizeRatio(row.ratio)
    if (row.selectable) {
      userUsableGroups[name] = row.description
    }
    const topup = row.topupRatio.trim()
    if (topup !== '' && Number.isFinite(Number(topup)) && Number(topup) >= 0) {
      topupGroupRatio[name] = Number(topup)
    }
  }

  return {
    GroupRatio: JSON.stringify(groupRatio, null, 2),
    UserUsableGroups: JSON.stringify(userUsableGroups, null, 2),
    TopupGroupRatio: JSON.stringify(topupGroupRatio, null, 2),
  }
}

function getGroupPricingValidation(rows: GroupPricingRow[]) {
  const names = new Set<string>()
  const duplicateNames = new Set<string>()
  let hasInvalidRatio = false

  for (const row of rows) {
    const name = row.name.trim()
    if (name) {
      if (names.has(name)) duplicateNames.add(name)
      names.add(name)
    }
    if (
      !isValidRequiredRatio(row.ratio) ||
      !isValidOptionalRatio(row.topupRatio)
    ) {
      hasInvalidRatio = true
    }
  }

  return { duplicateNames: [...duplicateNames], hasInvalidRatio }
}

function groupPricingSignature(rows: GroupPricingRow[]): string {
  const serialized = serializeGroupPricingRows(rows)
  return JSON.stringify({
    groupRatio: parseRatioMap(serialized.GroupRatio),
    userUsableGroups: parseUsableMap(serialized.UserUsableGroups),
    topupGroupRatio: parseRatioMap(serialized.TopupGroupRatio),
  })
}

function sourceGroupPricingSignature(
  groupRatio: string,
  userUsableGroups: string,
  topupGroupRatio: string
): string {
  return JSON.stringify({
    groupRatio: parseRatioMap(groupRatio),
    userUsableGroups: parseUsableMap(userUsableGroups),
    topupGroupRatio: parseRatioMap(topupGroupRatio),
  })
}

function UnknownGroupBadge() {
  const { t } = useTranslation()
  return (
    <StatusBadge variant='danger' copyable={false}>
      <AlertTriangle className='mr-1 h-3 w-3' />
      {t('Not in pricing table')}
    </StatusBadge>
  )
}

type GroupNameSelectProps = {
  options: string[]
  value: string | null
  placeholder: string
  onValueChange: (value: string) => void
  className?: string
}

function GroupNameSelect(props: GroupNameSelectProps) {
  const options = useMemo(() => {
    if (props.value && !props.options.includes(props.value)) {
      return [props.value, ...props.options]
    }
    return props.options
  }, [props.options, props.value])

  return (
    <Select
      value={props.value === '' ? null : props.value}
      onValueChange={(v) => {
        if (typeof v === 'string' && v !== '') props.onValueChange(v)
      }}
    >
      <SelectTrigger className={props.className ?? 'w-48'}>
        <SelectValue placeholder={props.placeholder} />
      </SelectTrigger>
      <SelectContent alignItemWithTrigger={false}>
        <SelectGroup>
          {options.map((name) => (
            <SelectItem key={name} value={name}>
              {name}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}

export const GroupRatioVisualEditor = memo(function GroupRatioVisualEditor({
  groupRatio,
  topupGroupRatio,
  userUsableGroups,
  groupGroupRatio,
  autoGroups,
  maxTokenAutoGroupsField,
  groupSpecialUsableGroup,
  onChange,
}: GroupRatioVisualEditorProps) {
  const { t } = useTranslation()
  const [detailGroup, setDetailGroup] = useState<string | null>(null)
  const [optimizationUserGroup, setOptimizationUserGroup] = useState<
    string | null
  >(null)
  const groupRatioResult = useMemo(
    () => parseRatioMapResult(groupRatio),
    [groupRatio]
  )
  const topupGroupRatioResult = useMemo(
    () => parseRatioMapResult(topupGroupRatio),
    [topupGroupRatio]
  )
  const userUsableGroupsResult = useMemo(
    () => parseUsableMapResult(userUsableGroups),
    [userUsableGroups]
  )
  const groupGroupRatioResult = useMemo(
    () => parseNestedRatioMapResult(groupGroupRatio),
    [groupGroupRatio]
  )
  const autoGroupsResult = useMemo(
    () => parseAutoGroupsResult(autoGroups),
    [autoGroups]
  )

  const registry = useMemo<RegistryEntry[]>(() => {
    const ratioMap = groupRatioResult.map
    const usableMap = userUsableGroupsResult.map
    const topupMap = topupGroupRatioResult.map
    const names = new Set([
      ...Object.keys(ratioMap),
      ...Object.keys(usableMap),
      ...Object.keys(topupMap),
    ])
    return [...names]
      .filter((name) => name.trim() !== '')
      .map((name) => ({
        name,
        ratio: normalizeRatio(ratioMap[name]),
      }))
  }, [
    groupRatioResult.map,
    topupGroupRatioResult.map,
    userUsableGroupsResult.map,
  ])

  const registryNames = useMemo(
    () => registry.map((entry) => entry.name),
    [registry]
  )

  // Auto groups
  const autoGroupsList = useMemo(() => {
    return autoGroupsResult.groups
  }, [autoGroupsResult.groups])

  const handleAutoGroupAdd = useCallback(
    (name: string) => {
      if (autoGroupsList.includes(name)) return
      onChange('AutoGroups', JSON.stringify([...autoGroupsList, name], null, 2))
    },
    [autoGroupsList, onChange]
  )

  const handleAutoGroupDelete = useCallback(
    (index: number) => {
      const list = autoGroupsList.filter((_, i) => i !== index)
      onChange('AutoGroups', JSON.stringify(list, null, 2))
    },
    [autoGroupsList, onChange]
  )

  const handleAutoGroupMove = useCallback(
    (index: number, direction: 'up' | 'down') => {
      const list = [...autoGroupsList]
      const newIndex = direction === 'up' ? index - 1 : index + 1
      if (newIndex < 0 || newIndex >= list.length) return
      ;[list[index], list[newIndex]] = [list[newIndex], list[index]]
      onChange('AutoGroups', JSON.stringify(list, null, 2))
    },
    [autoGroupsList, onChange]
  )

  const optimizationOverrides = useMemo(() => {
    if (!optimizationUserGroup) return undefined
    const overrides = groupGroupRatioResult.map[optimizationUserGroup]
    if (
      typeof overrides !== 'object' ||
      overrides === null ||
      Array.isArray(overrides)
    ) {
      return undefined
    }
    return overrides
  }, [groupGroupRatioResult.map, optimizationUserGroup])

  const optimizationError = useMemo(() => {
    if (
      !groupRatioResult.isValid ||
      !topupGroupRatioResult.isValid ||
      !userUsableGroupsResult.isValid ||
      !autoGroupsResult.isValid ||
      !groupGroupRatioResult.isValid
    ) {
      return t(
        'Cannot optimize until AutoGroups and ratio maps are valid JSON with non-negative finite numeric values.'
      )
    }
    if (
      optimizationUserGroup &&
      groupGroupRatioResult.invalidUserGroups.has(optimizationUserGroup)
    ) {
      return t(
        'Cannot optimize with this baseline because it has an invalid special ratio override.'
      )
    }
    return null
  }, [
    autoGroupsResult.isValid,
    groupGroupRatioResult.invalidUserGroups,
    groupGroupRatioResult.isValid,
    groupRatioResult.isValid,
    optimizationUserGroup,
    t,
    topupGroupRatioResult.isValid,
    userUsableGroupsResult.isValid,
  ])

  const handleAutoGroupOptimize = useCallback(() => {
    // This intentionally replaces the manual order only after an explicit
    // administrator action. Unknown/stale entries are excluded because the
    // backend cannot assign a user to a group that is not registered here.
    onChange(
      'AutoGroups',
      JSON.stringify(
        buildOptimizedAutoGroups(registry, optimizationOverrides),
        null,
        2
      )
    )
  }, [onChange, optimizationOverrides, registry])

  const autoGroupCandidates = useMemo(
    () => registryNames.filter((name) => !autoGroupsList.includes(name)),
    [registryNames, autoGroupsList]
  )

  const autoGroupItems = useMemo(() => {
    const occurrences = new Map<string, number>()
    return autoGroupsList.map((group) => {
      const occurrence = (occurrences.get(group) ?? 0) + 1
      occurrences.set(group, occurrence)
      return { group, key: `${group}-${occurrence}` }
    })
  }, [autoGroupsList])

  return (
    <div className='space-y-4'>
      <GroupPricingTable
        groupRatio={groupRatio}
        userUsableGroups={userUsableGroups}
        topupGroupRatio={topupGroupRatio}
        onChange={onChange}
        onShowDetail={setDetailGroup}
      />

      <GroupOverrideRules
        registry={registry}
        groupGroupRatio={groupGroupRatio}
        onChange={onChange}
      />

      {/* Auto Groups */}
      <Card className={sectionCardClassName}>
        <CardHeader className={sectionHeaderClassName}>
          <CardTitle>{t('Auto assignment order')}</CardTitle>
          <CardDescription>
            {t(
              'Priority order for tokens in the auto group. The system tries groups from top to bottom.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className='space-y-4'>
            {maxTokenAutoGroupsField}
            <div className='flex flex-wrap items-center gap-2'>
              <GroupNameSelect
                options={autoGroupCandidates}
                value={null}
                placeholder={t('Add group')}
                onValueChange={handleAutoGroupAdd}
              />
              <Button
                variant='outline'
                size='sm'
                onClick={handleAutoGroupOptimize}
                disabled={optimizationError !== null}
              >
                {t('Optimize by effective cost')}
              </Button>
            </div>
            <div className='space-y-2'>
              <Label>{t('Optimization baseline user group')}</Label>
              <Select
                value={optimizationUserGroup ?? '__base_ratio__'}
                onValueChange={(value) => {
                  if (typeof value !== 'string') return
                  setOptimizationUserGroup(
                    value === '__base_ratio__' ? null : value
                  )
                }}
              >
                <SelectTrigger className='w-full sm:w-64'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    <SelectItem value='__base_ratio__'>
                      {t('Base cost multipliers')}
                    </SelectItem>
                    {registryNames.map((name) => (
                      <SelectItem key={name} value={name}>
                        {name}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            <p className='text-muted-foreground text-xs'>
              {t(
                "Manual order is preserved until you use Optimize. This changes the global order for every user, but runtime assignment still filters each user's visible groups. Optimize uses base cost multipliers by default; selecting a user group applies its exact special cost overrides before sorting."
              )}
            </p>
            {optimizationError && (
              <p className='text-destructive text-sm' role='alert'>
                {optimizationError}
              </p>
            )}
            {autoGroupsList.length > 0 && (
              <div className='space-y-2'>
                {autoGroupItems.map(({ group, key }, index) => (
                  <div
                    key={key}
                    className='flex items-center gap-2 rounded-md border p-3'
                  >
                    <GripVertical className='text-muted-foreground h-4 w-4' />
                    <span className='font-medium'>{group}</span>
                    {!registryNames.includes(group) && <UnknownGroupBadge />}
                    <div className='ml-auto flex gap-1'>
                      <Button
                        variant='ghost'
                        size='sm'
                        disabled={index === 0}
                        onClick={() => handleAutoGroupMove(index, 'up')}
                      >
                        ↑
                      </Button>
                      <Button
                        variant='ghost'
                        size='sm'
                        disabled={index === autoGroupsList.length - 1}
                        onClick={() => handleAutoGroupMove(index, 'down')}
                      >
                        ↓
                      </Button>
                      <Button
                        variant='ghost'
                        size='sm'
                        onClick={() => handleAutoGroupDelete(index)}
                      >
                        <Trash2 className='h-4 w-4' />
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </CardContent>
      </Card>

      <GroupDetailSheet
        groupName={detailGroup}
        onOpenChange={(open) => {
          if (!open) setDetailGroup(null)
        }}
        registry={registry}
        topupGroupRatio={topupGroupRatio}
        userUsableGroups={userUsableGroups}
        groupGroupRatio={groupGroupRatio}
        autoGroups={autoGroupsList}
        groupSpecialUsableGroup={groupSpecialUsableGroup}
      />
    </div>
  )
})

type GroupPricingTableProps = {
  groupRatio: string
  userUsableGroups: string
  topupGroupRatio: string
  onChange: (field: string, value: string) => void
  onShowDetail: (name: string) => void
}

function GroupPricingTable({
  groupRatio,
  userUsableGroups,
  topupGroupRatio,
  onChange,
  onShowDetail,
}: GroupPricingTableProps) {
  const { t } = useTranslation()
  const [rows, setRows] = useState<GroupPricingRow[]>(() =>
    buildGroupPricingRows(groupRatio, userUsableGroups, topupGroupRatio)
  )

  useEffect(() => {
    const incomingSignature = sourceGroupPricingSignature(
      groupRatio,
      userUsableGroups,
      topupGroupRatio
    )
    setRows((currentRows) => {
      if (groupPricingSignature(currentRows) === incomingSignature) {
        return currentRows
      }
      return buildGroupPricingRows(
        groupRatio,
        userUsableGroups,
        topupGroupRatio
      )
    })
  }, [groupRatio, userUsableGroups, topupGroupRatio])

  const emitRows = useCallback(
    (nextRows: GroupPricingRow[]) => {
      setRows(nextRows)
      const validation = getGroupPricingValidation(nextRows)
      if (validation.duplicateNames.length > 0 || validation.hasInvalidRatio) {
        return
      }
      const serialized = serializeGroupPricingRows(nextRows)
      onChange('GroupRatio', serialized.GroupRatio)
      onChange('UserUsableGroups', serialized.UserUsableGroups)
      onChange('TopupGroupRatio', serialized.TopupGroupRatio)
    },
    [onChange]
  )

  const updateRow = useCallback(
    (
      id: string,
      field: Exclude<keyof GroupPricingRow, '_id'>,
      value: string | number | boolean
    ) => {
      emitRows(
        rows.map((row) => (row._id === id ? { ...row, [field]: value } : row))
      )
    },
    [emitRows, rows]
  )

  const addRow = useCallback(() => {
    const existingNames = new Set(rows.map((row) => row.name))
    let index = 1
    let name = `group_${index}`
    while (existingNames.has(name)) {
      index += 1
      name = `group_${index}`
    }
    emitRows([
      ...rows,
      {
        _id: createGroupPricingId(),
        name,
        ratio: '1',
        topupRatio: '',
        selectable: true,
        description: '',
      },
    ])
  }, [emitRows, rows])

  const removeRow = useCallback(
    (id: string) => {
      emitRows(rows.filter((row) => row._id !== id))
    },
    [emitRows, rows]
  )

  const validation = useMemo(() => getGroupPricingValidation(rows), [rows])
  const duplicateNames = validation.duplicateNames

  return (
    <Card className={sectionCardClassName}>
      <CardHeader className={sectionHeaderClassName}>
        <div className='flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between'>
          <div>
            <CardTitle>{t('Pricing groups')}</CardTitle>
            <CardDescription>
              {t(
                'Set a fixed price multiplier for each routing group. The top-up ratio remains an independent balance multiplier.'
              )}
            </CardDescription>
          </div>
          <Button onClick={addRow} size='sm' className='sm:self-start'>
            <Plus className='mr-2 h-4 w-4' />
            {t('Add group')}
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        <div className='space-y-3'>
          <StaticDataTable
            data={rows}
            getRowKey={(row) => row._id}
            emptyClassName='text-muted-foreground h-20 text-sm'
            emptyContent={t('No groups yet. Add a group to get started.')}
            columns={[
              {
                id: 'group',
                header: t('Group name'),
                className: 'min-w-40',
                cell: (row) => (
                  <Input
                    value={row.name}
                    onChange={(event) =>
                      updateRow(row._id, 'name', event.target.value)
                    }
                    aria-invalid={duplicateNames.includes(row.name.trim())}
                  />
                ),
              },
              {
                id: 'ratio',
                header: t('Cost multiplier'),
                className: 'w-28',
                cell: (row) => (
                  <Input
                    type='number'
                    min={0}
                    step={0.1}
                    value={row.ratio}
                    aria-invalid={!isValidRequiredRatio(row.ratio)}
                    onChange={(event) =>
                      updateRow(row._id, 'ratio', event.target.value)
                    }
                  />
                ),
              },
              {
                id: 'topup-ratio',
                header: t('Top-up ratio'),
                className: 'w-28',
                cell: (row) => (
                  <Input
                    type='number'
                    min={0}
                    step={0.1}
                    value={row.topupRatio}
                    placeholder={t('Not set')}
                    aria-invalid={!isValidOptionalRatio(row.topupRatio)}
                    onChange={(event) =>
                      updateRow(row._id, 'topupRatio', event.target.value)
                    }
                  />
                ),
              },
              {
                id: 'selectable',
                header: t('User selectable'),
                className: 'w-28 text-center',
                cell: (row) => (
                  <div className='flex justify-center'>
                    <Checkbox
                      checked={row.selectable}
                      onCheckedChange={(checked) =>
                        updateRow(row._id, 'selectable', checked === true)
                      }
                      aria-label={t('User selectable')}
                    />
                  </div>
                ),
              },
              {
                id: 'description',
                header: t('Description'),
                className: 'min-w-56',
                cell: (row) =>
                  row.selectable ? (
                    <Input
                      value={row.description}
                      placeholder={t('Group description')}
                      onChange={(event) =>
                        updateRow(row._id, 'description', event.target.value)
                      }
                    />
                  ) : (
                    <span className='text-muted-foreground px-3 text-sm'>
                      -
                    </span>
                  ),
              },
              {
                id: 'actions',
                header: t('Actions'),
                className: 'text-right',
                cellClassName: 'text-right',
                cell: (row) => (
                  <div className='flex justify-end gap-1'>
                    <Button
                      variant='ghost'
                      size='sm'
                      onClick={() => onShowDetail(row.name.trim())}
                      disabled={!row.name.trim()}
                      aria-label={t('Details')}
                    >
                      <Info className='h-4 w-4' />
                    </Button>
                    <Button
                      variant='ghost'
                      size='sm'
                      onClick={() => removeRow(row._id)}
                      aria-label={t('Delete')}
                    >
                      <Trash2 className='h-4 w-4' />
                    </Button>
                  </div>
                ),
              },
            ]}
          />

          {duplicateNames.length > 0 && (
            <p className='text-destructive text-sm'>
              {t('Duplicate group names: {{names}}', {
                names: duplicateNames.join(', '),
              })}
            </p>
          )}
          {validation.hasInvalidRatio && (
            <p className='text-destructive text-sm'>
              {t(
                'Cost multipliers must be finite numbers greater than or equal to zero.'
              )}
            </p>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

type GroupOverrideRulesProps = {
  registry: RegistryEntry[]
  groupGroupRatio: string
  onChange: (field: string, value: string) => void
}

function GroupOverrideRules({
  registry,
  groupGroupRatio,
  onChange,
}: GroupOverrideRulesProps) {
  const { t } = useTranslation()
  const [userGroupDialogOpen, setUserGroupDialogOpen] = useState(false)
  const [userGroupInput, setUserGroupInput] = useState<string | null>(null)
  const [overrideDialogOpen, setOverrideDialogOpen] = useState(false)
  const [overrideUserGroup, setOverrideUserGroup] = useState<string | null>(
    null
  )
  const [overrideEditData, setOverrideEditData] =
    useState<GroupOverride | null>(null)

  const registryNames = useMemo(
    () => registry.map((entry) => entry.name),
    [registry]
  )

  const baseRatioByName = useMemo(() => {
    const map = new Map<string, number>()
    for (const entry of registry) map.set(entry.name, entry.ratio)
    return map
  }, [registry])

  const groupGroupRatioList = useMemo(() => {
    const map = parseNestedRatioMap(groupGroupRatio)
    return Object.entries(map)
      .filter(
        ([userGroup, overrides]) =>
          userGroup.trim() !== '' &&
          typeof overrides === 'object' &&
          overrides !== null &&
          !Array.isArray(overrides)
      )
      .map(([userGroup, overrides]) => ({
        userGroup,
        overrides: Object.entries(overrides)
          .filter(([targetGroup]) => targetGroup.trim() !== '')
          .map(([targetGroup, ratio]) => ({
            targetGroup,
            ratio: parseOverrideRatio(ratio),
          }))
          .sort(compareOverrides),
      }))
      .sort((left, right) => stableNameCompare(left.userGroup, right.userGroup))
  }, [groupGroupRatio])

  const emitMap = useCallback(
    (map: Record<string, Record<string, number>>) => {
      onChange('GroupGroupRatio', JSON.stringify(map, null, 2))
    },
    [onChange]
  )

  const handleUserGroupSave = useCallback(() => {
    if (!userGroupInput) return
    const map = parseNestedRatioMap(groupGroupRatio)
    if (!map[userGroupInput]) {
      map[userGroupInput] = {}
    }
    emitMap(map)
    setUserGroupDialogOpen(false)
    setUserGroupInput(null)
  }, [userGroupInput, groupGroupRatio, emitMap])

  const handleUserGroupDelete = useCallback(
    (userGroup: string) => {
      const map = parseNestedRatioMap(groupGroupRatio)
      delete map[userGroup]
      emitMap(map)
    },
    [groupGroupRatio, emitMap]
  )

  const handleOverrideAdd = useCallback((userGroup: string) => {
    setOverrideUserGroup(userGroup)
    setOverrideEditData(null)
    setOverrideDialogOpen(true)
  }, [])

  const handleOverrideEdit = useCallback(
    (userGroup: string, override: GroupOverride) => {
      setOverrideUserGroup(userGroup)
      setOverrideEditData(override)
      setOverrideDialogOpen(true)
    },
    []
  )

  const handleOverrideSave = useCallback(
    (targetGroup: string, ratio: number, oldTargetGroup?: string) => {
      if (!overrideUserGroup) return
      const map = parseNestedRatioMap(groupGroupRatio)
      if (!map[overrideUserGroup]) {
        map[overrideUserGroup] = {}
      }
      if (oldTargetGroup && oldTargetGroup !== targetGroup) {
        delete map[overrideUserGroup][oldTargetGroup]
      }
      map[overrideUserGroup][targetGroup] = ratio
      emitMap(map)
      setOverrideDialogOpen(false)
    },
    [overrideUserGroup, groupGroupRatio, emitMap]
  )

  const handleOverrideDelete = useCallback(
    (userGroup: string, targetGroup: string) => {
      const map = parseNestedRatioMap(groupGroupRatio)
      if (map[userGroup]) {
        delete map[userGroup][targetGroup]
        if (Object.keys(map[userGroup]).length === 0) {
          delete map[userGroup]
        }
      }
      emitMap(map)
    },
    [groupGroupRatio, emitMap]
  )

  return (
    <Card className={sectionCardClassName}>
      <CardHeader className={sectionHeaderClassName}>
        <CardTitle>{t('Special cost rules')}</CardTitle>
        <CardDescription>
          {t(
            'Each rule reads as a sentence: users of one group use a special cost multiplier when billed as another group. An exact user group plus billing group rule takes priority; otherwise the billing group base cost applies.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className='space-y-4'>
          <Button
            onClick={() => {
              setUserGroupInput(null)
              setUserGroupDialogOpen(true)
            }}
            size='sm'
          >
            <Plus className='mr-2 h-4 w-4' />
            {t('Add user group')}
          </Button>
          {groupGroupRatioList.length > 0 && (
            <div className='space-y-3'>
              {groupGroupRatioList.map((userGroupData) => (
                <Collapsible key={userGroupData.userGroup}>
                  <div className='rounded-lg border'>
                    <div className='flex items-center justify-between p-4'>
                      <div className='flex items-center gap-2'>
                        <CollapsibleTrigger
                          render={<Button variant='ghost' size='sm' />}
                        >
                          <ChevronDown className='h-4 w-4' />
                        </CollapsibleTrigger>
                        <span className='font-semibold'>
                          {userGroupData.userGroup}
                        </span>
                        {!registryNames.includes(userGroupData.userGroup) && (
                          <AlertTriangle
                            className='text-destructive h-4 w-4'
                            aria-label={t('Not in pricing table')}
                          />
                        )}
                        <span className='text-muted-foreground text-sm'>
                          {t('{{count}} override', {
                            count: userGroupData.overrides.length,
                          })}
                        </span>
                      </div>
                      <div className='flex gap-2'>
                        <Button
                          variant='ghost'
                          size='sm'
                          onClick={() =>
                            handleOverrideAdd(userGroupData.userGroup)
                          }
                        >
                          <Plus className='h-4 w-4' />
                        </Button>
                        <Button
                          variant='ghost'
                          size='sm'
                          onClick={() =>
                            handleUserGroupDelete(userGroupData.userGroup)
                          }
                        >
                          <Trash2 className='h-4 w-4' />
                        </Button>
                      </div>
                    </div>
                    <CollapsibleContent>
                      {userGroupData.overrides.length > 0 && (
                        <div className='border-t'>
                          <StaticDataTable
                            className='rounded-none border-0'
                            data={userGroupData.overrides}
                            getRowKey={(override) => override.targetGroup}
                            columns={[
                              {
                                id: 'target-group',
                                header: t('Billing group'),
                                cellClassName: 'font-medium',
                                cell: (override) => (
                                  <span className='inline-flex items-center gap-1.5'>
                                    {override.targetGroup}
                                    {!registryNames.includes(
                                      override.targetGroup
                                    ) && (
                                      <AlertTriangle
                                        className='text-destructive h-3.5 w-3.5'
                                        aria-label={t('Not in pricing table')}
                                      />
                                    )}
                                  </span>
                                ),
                              },
                              {
                                id: 'ratio',
                                header: t('Cost multiplier'),
                                cell: (override) => {
                                  const baseRatio = baseRatioByName.get(
                                    override.targetGroup
                                  )
                                  return (
                                    <span className='inline-flex items-center gap-1.5'>
                                      {override.ratio ??
                                        t('Invalid cost multiplier')}
                                      {override.ratio !== null &&
                                        baseRatio !== undefined &&
                                        baseRatio !== override.ratio && (
                                          <span className='text-muted-foreground text-xs'>
                                            {t('(instead of {{ratio}})', {
                                              ratio: baseRatio,
                                            })}
                                          </span>
                                        )}
                                    </span>
                                  )
                                },
                              },
                              {
                                id: 'actions',
                                header: t('Actions'),
                                className: 'text-right',
                                cellClassName: 'text-right',
                                cell: (override) => (
                                  <StaticRowActions
                                    editLabel={t('Edit')}
                                    deleteLabel={t('Delete')}
                                    menuLabel={t('Open menu')}
                                    onEdit={() =>
                                      handleOverrideEdit(
                                        userGroupData.userGroup,
                                        override
                                      )
                                    }
                                    onDelete={() =>
                                      handleOverrideDelete(
                                        userGroupData.userGroup,
                                        override.targetGroup
                                      )
                                    }
                                  />
                                ),
                              },
                            ]}
                          />
                        </div>
                      )}
                    </CollapsibleContent>
                  </div>
                </Collapsible>
              ))}
            </div>
          )}
        </div>
      </CardContent>

      {/* Add user group dialog */}
      <Dialog
        open={userGroupDialogOpen}
        onOpenChange={setUserGroupDialogOpen}
        title={t('Add user group')}
        description={t(
          'Create a new user group to configure ratio overrides for.'
        )}
        contentHeight='auto'
        bodyClassName='space-y-4'
        footer={
          <>
            <Button
              variant='outline'
              onClick={() => setUserGroupDialogOpen(false)}
            >
              {t('Cancel')}
            </Button>
            <Button onClick={handleUserGroupSave} disabled={!userGroupInput}>
              {t('Add')}
            </Button>
          </>
        }
      >
        <div className='space-y-4 py-4'>
          <div className='space-y-2'>
            <Label>{t('User group name')}</Label>
            <GroupNameSelect
              className='w-full'
              options={registryNames}
              value={userGroupInput}
              placeholder={t('Select a group')}
              onValueChange={setUserGroupInput}
            />
          </div>
        </div>
      </Dialog>

      <GroupOverrideDialog
        open={overrideDialogOpen}
        onOpenChange={setOverrideDialogOpen}
        onSave={handleOverrideSave}
        editData={overrideEditData}
        userGroup={overrideUserGroup}
        groupOptions={registryNames}
        baseRatioByName={baseRatioByName}
      />
    </Card>
  )
}

type GroupOverrideDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSave: (targetGroup: string, ratio: number, oldTargetGroup?: string) => void
  editData: GroupOverride | null
  userGroup: string | null
  groupOptions: string[]
  baseRatioByName: Map<string, number>
}

function GroupOverrideDialog({
  open,
  onOpenChange,
  onSave,
  editData,
  userGroup,
  groupOptions,
  baseRatioByName,
}: GroupOverrideDialogProps) {
  const { t } = useTranslation()
  const [targetGroup, setTargetGroup] = useState<string | null>(null)
  const [ratio, setRatio] = useState('')

  useEffect(() => {
    if (!open) {
      setTargetGroup(null)
      setRatio('')
      return
    }

    setTargetGroup(editData?.targetGroup ?? null)
    setRatio(editData?.ratio === null ? '' : String(editData?.ratio ?? ''))
  }, [editData, open])

  const baseRatio = targetGroup ? baseRatioByName.get(targetGroup) : undefined

  const handleSave = () => {
    if (!targetGroup || !ratio.trim()) return
    const parsedRatio = Number(ratio)
    if (!Number.isFinite(parsedRatio) || parsedRatio < 0) return

    onSave(targetGroup, parsedRatio, editData?.targetGroup)
    setTargetGroup(null)
    setRatio('')
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={editData ? t('Edit cost override') : t('Add cost override')}
      description={
        userGroup
          ? t(
              'Configure a custom cost multiplier for "{{userGroup}}" users when using a specific token group.',
              { userGroup }
            )
          : t(
              'Configure a custom cost multiplier for when users use a specific token group.'
            )
      }
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button onClick={handleSave}>
            {editData ? t('Update') : t('Add')}
          </Button>
        </>
      }
    >
      <div className='space-y-4 py-4'>
        <div className='space-y-2'>
          <Label>{t('Billing group')}</Label>
          <GroupNameSelect
            className='w-full'
            options={groupOptions}
            value={targetGroup}
            placeholder={t('Select a group')}
            onValueChange={setTargetGroup}
          />
          <p className='text-muted-foreground text-xs'>
            {t('The token group that will have a custom ratio')}
          </p>
        </div>
        <div className='space-y-2'>
          <Label>{t('Cost multiplier')}</Label>
          <Input
            type='number'
            min={0}
            step={0.01}
            value={ratio}
            onChange={(e) => {
              const val = e.target.value
              if (
                val === '' ||
                (Number.isFinite(Number(val)) && Number(val) >= 0)
              ) {
                setRatio(val)
              }
            }}
            placeholder={baseRatio === undefined ? '0.9' : String(baseRatio)}
          />
          <p className='text-muted-foreground text-xs'>
            {baseRatio !== undefined
              ? t('(instead of {{ratio}})', { ratio: baseRatio })
              : t(
                  'Cost multiplier applied when {{userGroup}} uses {{targetGroup}}',
                  {
                    userGroup: userGroup || t('this user group'),
                    targetGroup: targetGroup || t('this token group'),
                  }
                )}
          </p>
        </div>
      </div>
    </Dialog>
  )
}

type GroupDetailSheetProps = {
  groupName: string | null
  onOpenChange: (open: boolean) => void
  registry: RegistryEntry[]
  topupGroupRatio: string
  userUsableGroups: string
  groupGroupRatio: string
  autoGroups: string[]
  groupSpecialUsableGroup: string
}

type VisibilityRule = {
  userGroup: string
  visible: boolean
  description: string
}

function parseSpecialGroupKey(rawKey: string): {
  visible: boolean
  groupName: string
} {
  if (rawKey.startsWith('-:')) {
    return { visible: false, groupName: rawKey.slice(2) }
  }
  if (rawKey.startsWith('+:')) {
    return { visible: true, groupName: rawKey.slice(2) }
  }
  return { visible: true, groupName: rawKey }
}

function GroupDetailSheet(props: GroupDetailSheetProps) {
  const { t } = useTranslation()
  const name = props.groupName

  const detail = useMemo(() => {
    if (!name) return null

    const entry = props.registry.find((item) => item.name === name)
    const topupMap = parseRatioMap(props.topupGroupRatio)
    const usableMap = parseUsableMap(props.userUsableGroups)
    const overrideMap = parseNestedRatioMap(props.groupGroupRatio)
    const specialMap = safeJsonParse<Record<string, Record<string, string>>>(
      props.groupSpecialUsableGroup,
      { fallback: {}, silent: true }
    )

    // Overrides that apply when other user groups bill as this group
    const incomingOverrides: { userGroup: string; ratio: number }[] = []
    for (const [userGroup, overrides] of Object.entries(overrideMap)) {
      if (Object.hasOwn(overrides, name)) {
        incomingOverrides.push({ userGroup, ratio: overrides[name] })
      }
    }

    // Overrides that apply when users of this group bill as other groups
    const outgoingOverrides = Object.entries(overrideMap[name] ?? {}).map(
      ([targetGroup, ratio]) => ({ targetGroup, ratio })
    )

    // Visibility rules targeting this group
    const visibilityRules: VisibilityRule[] = []
    for (const [userGroup, inner] of Object.entries(specialMap)) {
      if (typeof inner !== 'object' || inner === null) continue
      for (const [rawKey, desc] of Object.entries(inner)) {
        const parsed = parseSpecialGroupKey(rawKey)
        if (parsed.groupName !== name) continue
        visibilityRules.push({
          userGroup,
          visible: parsed.visible,
          description: typeof desc === 'string' ? desc : '',
        })
      }
    }

    const autoIndex = props.autoGroups.indexOf(name)

    return {
      ratio: entry?.ratio,
      topupRatio: Object.hasOwn(topupMap, name) ? String(topupMap[name]) : null,
      selectable: Object.hasOwn(usableMap, name),
      description: String(usableMap[name] ?? ''),
      incomingOverrides,
      outgoingOverrides,
      visibilityRules,
      autoIndex,
    }
  }, [
    name,
    props.registry,
    props.topupGroupRatio,
    props.userUsableGroups,
    props.groupGroupRatio,
    props.autoGroups,
    props.groupSpecialUsableGroup,
  ])

  return (
    <Sheet open={name !== null} onOpenChange={props.onOpenChange}>
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-lg')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {t('Group details')}
            {name ? `: ${name}` : ''}
          </SheetTitle>
          <SheetDescription>
            {t('Everything configured for this group, in one place.')}
          </SheetDescription>
        </SheetHeader>

        {detail && (
          <div className={sideDrawerFormClassName('gap-5')}>
            <section className='space-y-2'>
              <h3 className='text-sm font-semibold'>{t('Overview')}</h3>
              <dl className='space-y-1.5 text-sm'>
                <div className='flex justify-between'>
                  <dt className='text-muted-foreground'>
                    {t('Cost multiplier')}
                  </dt>
                  <dd className='font-medium'>{detail.ratio ?? '-'}</dd>
                </div>
                <div className='flex justify-between'>
                  <dt className='text-muted-foreground'>{t('Top-up ratio')}</dt>
                  <dd className='font-medium'>
                    {detail.topupRatio ?? t('Not set')}
                  </dd>
                </div>
                <div className='flex justify-between'>
                  <dt className='text-muted-foreground'>
                    {t('User selectable')}
                  </dt>
                  <dd className='font-medium'>
                    {detail.selectable ? t('Yes') : t('No')}
                  </dd>
                </div>
                {detail.selectable && detail.description && (
                  <div className='flex justify-between gap-4'>
                    <dt className='text-muted-foreground'>
                      {t('Description')}
                    </dt>
                    <dd className='text-right font-medium'>
                      {detail.description}
                    </dd>
                  </div>
                )}
                <div className='flex justify-between'>
                  <dt className='text-muted-foreground'>
                    {t('Auto assignment order')}
                  </dt>
                  <dd className='font-medium'>
                    {detail.autoIndex >= 0
                      ? t('Position {{position}}', {
                          position: detail.autoIndex + 1,
                        })
                      : t('Not included')}
                  </dd>
                </div>
              </dl>
            </section>

            <section className='space-y-2'>
              <h3 className='text-sm font-semibold'>
                {t('Ratio overrides when billed as this group')}
              </h3>
              {detail.incomingOverrides.length === 0 ? (
                <p className='text-muted-foreground text-sm'>{t('None')}</p>
              ) : (
                <ul className='space-y-1 text-sm'>
                  {detail.incomingOverrides.map((item) => (
                    <li
                      key={item.userGroup}
                      className='flex justify-between rounded-md border px-3 py-1.5'
                    >
                      <span>
                        {t('Users in {{group}}', { group: item.userGroup })}
                      </span>
                      <span className='font-medium'>{item.ratio}</span>
                    </li>
                  ))}
                </ul>
              )}
            </section>

            <section className='space-y-2'>
              <h3 className='text-sm font-semibold'>
                {t('Ratio overrides for users of this group')}
              </h3>
              {detail.outgoingOverrides.length === 0 ? (
                <p className='text-muted-foreground text-sm'>{t('None')}</p>
              ) : (
                <ul className='space-y-1 text-sm'>
                  {detail.outgoingOverrides.map((item) => (
                    <li
                      key={item.targetGroup}
                      className='flex justify-between rounded-md border px-3 py-1.5'
                    >
                      <span>
                        {t('When billed as {{group}}', {
                          group: item.targetGroup,
                        })}
                      </span>
                      <span className='font-medium'>{item.ratio}</span>
                    </li>
                  ))}
                </ul>
              )}
            </section>

            <section className='space-y-2'>
              <h3 className='text-sm font-semibold'>
                {t('Special visibility rules')}
              </h3>
              {detail.visibilityRules.length === 0 ? (
                <p className='text-muted-foreground text-sm'>{t('None')}</p>
              ) : (
                <ul className='space-y-1 text-sm'>
                  {detail.visibilityRules.map((rule) => (
                    <li
                      key={`${rule.userGroup}-${rule.visible}`}
                      className='flex items-center justify-between rounded-md border px-3 py-1.5'
                    >
                      <span>
                        {rule.visible
                          ? t('Extra visible to {{group}}', {
                              group: rule.userGroup,
                            })
                          : t('Hidden from {{group}}', {
                              group: rule.userGroup,
                            })}
                      </span>
                      <StatusBadge
                        variant={rule.visible ? 'info' : 'danger'}
                        copyable={false}
                      >
                        {rule.visible ? t('Visible') : t('Hidden')}
                      </StatusBadge>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}
