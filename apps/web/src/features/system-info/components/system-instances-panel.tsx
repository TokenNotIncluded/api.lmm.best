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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  Cpu,
  HardDrive,
  Loader2,
  MemoryStick,
  RefreshCw,
  ServerCog,
  Trash2,
} from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestampRelative, formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  deleteStaleSystemInstance,
  deleteStaleSystemInstances,
  listSystemInstances,
} from '../api'
import type { SystemInstance, SystemInstanceStatus } from '../types'

const INSTANCE_POLL_INTERVAL_MS = 30_000
const INSTANCE_SKELETON_KEYS = [
  'system-instance-skeleton-1',
  'system-instance-skeleton-2',
  'system-instance-skeleton-3',
]

const STATUS_CLASS_NAME: Record<SystemInstanceStatus, string> = {
  online: 'console-status-success-badge',
  stale: 'console-status-warning-badge',
}

const STATUS_DOT_CLASS_NAME: Record<SystemInstanceStatus, string> = {
  online: 'forge-status-dot-success',
  stale: 'forge-status-dot-warning',
}

function roleLabel(instance: SystemInstance) {
  if (instance.info?.role?.is_master) return 'master'
  return 'worker'
}

function roleDescriptionKey(instance: SystemInstance) {
  if (instance.info?.role?.is_master) {
    return 'Master instances run scheduled background tasks.'
  }
  return 'Worker instances do not run master-only background tasks.'
}

function runtimeLabel(instance: SystemInstance) {
  const runtime = instance.info?.runtime
  if (!runtime?.goos && !runtime?.goarch) return '-'

  const parts: string[] = []
  if (runtime.goos || runtime.goarch) {
    parts.push([runtime.goos, runtime.goarch].filter(Boolean).join('/'))
  }
  return parts.join(' · ')
}

function getReporterId(instance: SystemInstance) {
  return instance.reporter_id || instance.node_name
}

function getPhysicalNodeName(instance: SystemInstance) {
  return (
    instance.physical_node_name ||
    instance.info?.node?.name ||
    instance.node_name
  )
}

function getInstanceSlot(instance: SystemInstance) {
  return (
    instance.instance_slot ||
    instance.info?.reporter?.slot ||
    instance.info?.runtime?.instance_slot
  )
}

function getInstanceDisplayName(instance: SystemInstance) {
  const slot = getInstanceSlot(instance)
  return slot
    ? `${getPhysicalNodeName(instance)} (${slot})`
    : getPhysicalNodeName(instance)
}

function formatPercent(value?: number | null) {
  if (typeof value !== 'number' || Number.isNaN(value)) return '-'
  return `${new Intl.NumberFormat(undefined, {
    maximumFractionDigits: 1,
  }).format(value)}%`
}

function formatBytes(bytes?: number): string {
  if (typeof bytes !== 'number' || Number.isNaN(bytes)) return '-'
  if (bytes === 0) return '0 B'
  if (bytes < 0) return `-${formatBytes(-bytes)}`

  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    units.length - 1
  )
  const value = bytes / 1024 ** index
  return `${new Intl.NumberFormat(undefined, {
    maximumFractionDigits: index === 0 ? 0 : 1,
  }).format(value)} ${units[index]}`
}

function ringColorClass(percent: number | null) {
  if (percent === null) return 'text-muted-foreground/40'
  if (percent >= 90) return 'console-status-danger'
  if (percent >= 70) return 'console-status-warning-icon'
  return 'console-status-success'
}

function percentTone(percent: number | null): IconBadgeTone {
  if (percent === null) return 'neutral'
  if (percent >= 90) return 'destructive'
  if (percent >= 70) return 'warning'
  return 'success'
}

/**
 * Mean of the reported percentages, ignoring instances that report nothing for
 * the metric. Returns `null` when no instance reports it, so the fleet summary
 * can say "unknown" instead of pretending the fleet sits at 0%.
 */
function averagePercent(values: Array<number | undefined | null>) {
  const reported = values.filter(
    (value): value is number =>
      typeof value === 'number' && !Number.isNaN(value)
  )
  if (reported.length === 0) return null
  return reported.reduce((sum, value) => sum + value, 0) / reported.length
}

function aggregateStoragePercent(instances: SystemInstance[]) {
  let used = 0
  let total = 0
  for (const instance of instances) {
    const storage = instance.info?.resources?.storage
    if (!storage) continue
    if (typeof storage.used_bytes === 'number') used += storage.used_bytes
    if (typeof storage.total_bytes === 'number') total += storage.total_bytes
  }
  if (total <= 0) return null
  return (used / total) * 100
}

type FleetStats = {
  online: number
  stale: number
  cpuPercent: number | null
  memoryPercent: number | null
  storagePercent: number | null
  reportedMemory: number
  reportedCpu: number
}

function summarizeFleet(instances: SystemInstance[]): FleetStats {
  const cpuValues = instances.map((i) => i.info?.resources?.cpu?.usage_percent)
  const memoryValues = instances.map(
    (i) => i.info?.resources?.memory?.usage_percent
  )
  return {
    online: instances.filter((i) => i.status === 'online').length,
    stale: instances.filter((i) => i.status === 'stale').length,
    cpuPercent: averagePercent(cpuValues),
    memoryPercent: averagePercent(memoryValues),
    storagePercent: aggregateStoragePercent(instances),
    reportedCpu: cpuValues.filter((v) => typeof v === 'number').length,
    reportedMemory: memoryValues.filter((v) => typeof v === 'number').length,
  }
}

type FleetStatTileProps = {
  icon: ReactNode
  tone: IconBadgeTone
  label: string
  value: string
  hint?: string
}

/**
 * A single read-only fleet figure. Values are `tabular-nums` and never animate —
 * uptime and load numbers must stay put while the panel polls.
 */
function FleetStatTile(props: FleetStatTileProps) {
  return (
    <div className='bg-card flex items-center gap-3 px-3 py-2.5 sm:px-4'>
      <IconBadge tone={props.tone} size='md'>
        {props.icon}
      </IconBadge>
      <div className='min-w-0'>
        <div className='text-muted-foreground text-[11px] font-medium tracking-wide uppercase'>
          {props.label}
        </div>
        <div className='font-mono text-lg leading-tight font-semibold tabular-nums'>
          {props.value}
        </div>
        {props.hint ? (
          <div className='text-muted-foreground truncate text-[11px]'>
            {props.hint}
          </div>
        ) : null}
      </div>
    </div>
  )
}

function FleetStatRow(props: { stats: FleetStats }) {
  const { t } = useTranslation()
  const { stats } = props

  return (
    <dl
      className='border-border/70 bg-border/40 grid grid-cols-2 gap-px border-b sm:grid-cols-3 lg:grid-cols-5'
      aria-label={t('Fleet summary')}
    >
      <FleetStatTile
        icon={<CheckCircle2 aria-hidden='true' />}
        tone={stats.online > 0 ? 'success' : 'neutral'}
        label={t('Online')}
        value={String(stats.online)}
        hint={t('{{count}} stale', { count: stats.stale })}
      />
      <FleetStatTile
        icon={<Activity aria-hidden='true' />}
        tone={percentTone(stats.cpuPercent)}
        label={t('Average CPU')}
        value={formatPercent(stats.cpuPercent)}
        hint={t('{{count}} reporting', { count: stats.reportedCpu })}
      />
      <FleetStatTile
        icon={<MemoryStick aria-hidden='true' />}
        tone={percentTone(stats.memoryPercent)}
        label={t('Average memory')}
        value={formatPercent(stats.memoryPercent)}
        hint={t('{{count}} reporting', { count: stats.reportedMemory })}
      />
      <FleetStatTile
        icon={<HardDrive aria-hidden='true' />}
        tone={percentTone(stats.storagePercent)}
        label={t('Total storage used')}
        value={formatPercent(stats.storagePercent)}
      />
      <FleetStatTile
        icon={<Cpu aria-hidden='true' />}
        tone='neutral'
        label={t('Instances')}
        value={String(stats.online + stats.stale)}
      />
    </dl>
  )
}

type RingProgressProps = {
  percent: number | null
  size?: number
}

function RingProgress(props: RingProgressProps) {
  const size = props.size ?? 22
  const stroke = 2.5
  const radius = (size - stroke) / 2
  const circumference = 2 * Math.PI * radius
  const offset =
    props.percent === null
      ? circumference
      : circumference - (props.percent / 100) * circumference

  return (
    <svg
      width={size}
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      className='shrink-0 -rotate-90'
      aria-hidden='true'
    >
      <circle
        cx={size / 2}
        cy={size / 2}
        r={radius}
        fill='none'
        strokeWidth={stroke}
        stroke='currentColor'
        className='text-muted'
      />
      <circle
        cx={size / 2}
        cy={size / 2}
        r={radius}
        fill='none'
        strokeWidth={stroke}
        strokeLinecap='round'
        stroke='currentColor'
        strokeDasharray={circumference}
        strokeDashoffset={offset}
        className={ringColorClass(props.percent)}
      />
    </svg>
  )
}

type ResourceCellProps = {
  value?: number
  tooltip?: ReactNode
}

function ResourceCell(props: ResourceCellProps) {
  const percent =
    typeof props.value === 'number' && !Number.isNaN(props.value)
      ? Math.max(0, Math.min(100, props.value))
      : null
  const content = (
    <div className='flex items-center gap-2'>
      <RingProgress percent={percent} />
      <span className='font-mono text-[11px] tabular-nums'>
        {formatPercent(props.value)}
      </span>
    </div>
  )

  if (!props.tooltip) return content

  return (
    <TooltipProvider delay={100}>
      <Tooltip>
        <TooltipTrigger className='block w-full rounded-sm text-left focus-visible:ring-2 focus-visible:outline-none'>
          {content}
        </TooltipTrigger>
        <TooltipContent className='max-w-80'>{props.tooltip}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

type SystemInstancesTableProps = {
  instances: SystemInstance[]
  deletingReporterId: string | null
  isDeletingInstance: boolean
  onDeleteStaleInstance: (instance: SystemInstance) => void
}

/** Icon + text status chip. Colour alone never carries the state. */
function InstanceStatusBadge(props: { status: SystemInstanceStatus }) {
  const { t } = useTranslation()

  return (
    <Badge
      variant='secondary'
      className={cn('gap-1.5', STATUS_CLASS_NAME[props.status])}
    >
      <span
        className={cn(
          'size-1.5 rounded-full',
          STATUS_DOT_CLASS_NAME[props.status]
        )}
        aria-hidden='true'
      />
      {t(props.status)}
    </Badge>
  )
}

function InstanceRoleBadge(props: { instance: SystemInstance }) {
  const { t } = useTranslation()

  return (
    <TooltipProvider delay={100}>
      <Tooltip>
        <TooltipTrigger
          className='inline-flex shrink-0 rounded-full focus-visible:ring-2 focus-visible:outline-none'
          aria-label={t('Node role')}
        >
          <Badge variant='outline'>{roleLabel(props.instance)}</Badge>
        </TooltipTrigger>
        <TooltipContent>{t(roleDescriptionKey(props.instance))}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

/** Name, slot, hostname and the "set NODE_NAME" hint. Shared by table + cards. */
function InstanceIdentity(props: { instance: SystemInstance }) {
  const { t } = useTranslation()
  const instance = props.instance
  const reporterId = getReporterId(instance)
  const instanceSlot = getInstanceSlot(instance)
  const shouldConfigure =
    instance.info?.node?.should_configure_manually === true && !instanceSlot

  return (
    <div className='flex min-w-0 items-start gap-2'>
      <span
        className={cn(
          'mt-1.5 size-2 shrink-0 rounded-full',
          STATUS_DOT_CLASS_NAME[instance.status]
        )}
        aria-hidden='true'
      />
      <div className='min-w-0'>
        <div className='flex min-w-0 items-center gap-1.5'>
          <span className='truncate text-sm font-medium'>
            {getPhysicalNodeName(instance)}
          </span>
          {instanceSlot && (
            <Badge
              variant='outline'
              className='shrink-0 font-mono text-[10px]'
              aria-label={`${t('Runtime')}: ${instanceSlot}`}
            >
              {instanceSlot}
            </Badge>
          )}
          {shouldConfigure && (
            <Popover>
              <PopoverTrigger
                className='inline-flex shrink-0 rounded-full focus-visible:ring-2 focus-visible:outline-none'
                aria-label={t('Configure NODE_NAME')}
              >
                <Badge
                  variant='outline'
                  className='console-status-warning-badge'
                >
                  <AlertTriangle className='size-3' aria-hidden='true' />
                </Badge>
              </PopoverTrigger>
              <PopoverContent align='start' className='w-80'>
                <PopoverHeader>
                  <PopoverTitle>{t('Configure NODE_NAME')}</PopoverTitle>
                  <PopoverDescription>
                    {t(
                      'This instance is using an automatic hostname. Set NODE_NAME to a stable unique value for multi-instance management.'
                    )}
                  </PopoverDescription>
                </PopoverHeader>
                <div className='space-y-2 text-xs'>
                  <div>
                    <div className='mb-1 font-medium'>{t('Example')}</div>
                    <code className='bg-muted block rounded-md px-2 py-1.5 font-mono text-[11px] break-all'>
                      NODE_NAME=lmm-forge-app-1
                    </code>
                  </div>
                  <p className='text-muted-foreground'>
                    {t(
                      'Use a different stable value for each instance, then restart the service.'
                    )}
                  </p>
                </div>
              </PopoverContent>
            </Popover>
          )}
        </div>
        <div className='text-muted-foreground truncate font-mono text-[11px]'>
          {instance.info?.host?.hostname || '-'}
        </div>
        {instanceSlot && (
          <div
            className='text-muted-foreground/80 truncate font-mono text-[10px]'
            title={reporterId}
          >
            {reporterId}
          </div>
        )}
      </div>
    </div>
  )
}

function StorageCell(props: { instance: SystemInstance }) {
  const { t } = useTranslation()
  const storage = props.instance.info?.resources?.storage

  return (
    <ResourceCell
      value={storage?.used_percent}
      tooltip={
        storage ? (
          <div className='space-y-1 text-xs'>
            <div className='grid grid-cols-[auto_1fr] gap-x-3 gap-y-1'>
              <span className='text-muted-foreground'>{t('Used')}</span>
              <span className='font-mono'>
                {formatBytes(storage.used_bytes)}
              </span>
              <span className='text-muted-foreground'>{t('Free')}</span>
              <span className='font-mono'>
                {formatBytes(storage.free_bytes)}
              </span>
              <span className='text-muted-foreground'>{t('Total')}</span>
              <span className='font-mono'>
                {formatBytes(storage.total_bytes)}
              </span>
            </div>
          </div>
        ) : undefined
      }
    />
  )
}

/**
 * Destructive row action. Kept as a real text button at `h-9` so the touch
 * target stays usable on phones, where the icon-only variant was too small.
 */
function DeleteStaleInstanceButton(props: {
  instance: SystemInstance
  isDeletingThisInstance: boolean
  isDeletingInstance: boolean
  onDeleteStaleInstance: (instance: SystemInstance) => void
  className?: string
}) {
  const { t } = useTranslation()

  if (props.instance.status !== 'stale') {
    return <span className='text-muted-foreground text-xs'>-</span>
  }

  return (
    <Button
      type='button'
      variant='destructive'
      size='sm'
      onClick={() => props.onDeleteStaleInstance(props.instance)}
      disabled={props.isDeletingInstance || props.isDeletingThisInstance}
      aria-label={t('Delete stale instance')}
      className={cn('min-h-9', props.className)}
    >
      {props.isDeletingThisInstance ? (
        <Loader2
          data-icon='inline-start'
          className='size-3.5 animate-spin'
          aria-hidden='true'
        />
      ) : (
        <Trash2
          data-icon='inline-start'
          className='size-3.5'
          aria-hidden='true'
        />
      )}
      {t('Delete')}
    </Button>
  )
}

function SystemInstancesList(props: SystemInstancesTableProps) {
  const { t, i18n } = useTranslation()

  return (
    <>
      {/* Phones: one card per instance, no horizontal scrolling. */}
      <ul className='space-y-3 sm:hidden'>
        {props.instances.map((instance) => {
          const reporterId = getReporterId(instance)
          const resources = instance.info?.resources
          const isDeletingThisInstance =
            props.isDeletingInstance && props.deletingReporterId === reporterId

          return (
            <li
              key={reporterId}
              className='bg-card rounded-lg border p-3 shadow-none'
            >
              <div className='flex items-start justify-between gap-2'>
                <InstanceIdentity instance={instance} />
                <InstanceStatusBadge status={instance.status} />
              </div>

              <dl className='mt-3 grid grid-cols-2 gap-x-3 gap-y-2.5 text-xs'>
                <div className='flex flex-col gap-1'>
                  <dt className='text-muted-foreground'>{t('Role')}</dt>
                  <dd>
                    <InstanceRoleBadge instance={instance} />
                  </dd>
                </div>
                <div className='flex flex-col gap-1'>
                  <dt className='text-muted-foreground'>{t('CPU')}</dt>
                  <dd>
                    <ResourceCell value={resources?.cpu?.usage_percent} />
                  </dd>
                </div>
                <div className='flex flex-col gap-1'>
                  <dt className='text-muted-foreground'>{t('Memory')}</dt>
                  <dd>
                    <ResourceCell value={resources?.memory?.usage_percent} />
                  </dd>
                </div>
                <div className='flex flex-col gap-1'>
                  <dt className='text-muted-foreground'>{t('Storage')}</dt>
                  <dd>
                    <StorageCell instance={instance} />
                  </dd>
                </div>
                <div className='flex flex-col gap-1'>
                  <dt className='text-muted-foreground'>{t('Version')}</dt>
                  <dd className='truncate font-mono'>
                    {instance.info?.runtime?.version || '-'}
                  </dd>
                </div>
                <div className='flex flex-col gap-1'>
                  <dt className='text-muted-foreground'>{t('Runtime')}</dt>
                  <dd className='truncate font-mono'>
                    {runtimeLabel(instance)}
                  </dd>
                </div>
                <div className='col-span-2 flex flex-col gap-1'>
                  <dt className='text-muted-foreground'>{t('Started')}</dt>
                  <dd className='whitespace-nowrap'>
                    {formatTimestampToDate(instance.started_at)}
                  </dd>
                </div>
                <div className='col-span-2 flex flex-col gap-1'>
                  <dt className='text-muted-foreground'>{t('Last Seen')}</dt>
                  <dd
                    className='whitespace-nowrap'
                    title={formatTimestampToDate(instance.last_seen_at)}
                  >
                    {formatTimestampRelative(
                      instance.last_seen_at,
                      'seconds',
                      toIntlLocale(i18n.language)
                    )}
                  </dd>
                </div>
              </dl>

              {instance.status === 'stale' ? (
                <div className='mt-3'>
                  <DeleteStaleInstanceButton
                    instance={instance}
                    isDeletingThisInstance={isDeletingThisInstance}
                    isDeletingInstance={props.isDeletingInstance}
                    onDeleteStaleInstance={props.onDeleteStaleInstance}
                    className='w-full'
                  />
                </div>
              ) : null}
            </li>
          )
        })}
      </ul>

      {/* Tablet and up: the comparison table. */}
      <div className='hidden overflow-x-auto rounded-md border sm:block'>
        <Table className='min-w-[1230px]'>
          <TableHeader>
            <TableRow className='bg-muted/40 hover:bg-muted/40'>
              <TableHead className='h-9 min-w-[240px] px-4 text-xs'>
                {t('Instances')}
              </TableHead>
              <TableHead className='h-9 w-[110px] text-xs'>
                {t('Status')}
              </TableHead>
              <TableHead className='h-9 w-[100px] text-xs'>
                {t('Role')}
              </TableHead>
              <TableHead className='h-9 w-[96px] text-xs'>{t('CPU')}</TableHead>
              <TableHead className='h-9 w-[96px] text-xs'>
                {t('Memory')}
              </TableHead>
              <TableHead className='h-9 w-[96px] text-xs'>
                {t('Storage')}
              </TableHead>
              <TableHead className='h-9 w-[100px] text-xs'>
                {t('Version')}
              </TableHead>
              <TableHead className='h-9 w-[140px] text-xs'>
                {t('Runtime')}
              </TableHead>
              <TableHead className='h-9 w-[170px] text-xs'>
                {t('Started')}
              </TableHead>
              <TableHead className='h-9 w-[170px] text-xs'>
                {t('Last Seen')}
              </TableHead>
              <TableHead className='h-9 w-[90px] pr-4 text-right text-xs'>
                {t('Actions')}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {props.instances.map((instance) => {
              const reporterId = getReporterId(instance)
              const resources = instance.info?.resources
              const isDeletingThisInstance =
                props.isDeletingInstance &&
                props.deletingReporterId === reporterId
              return (
                <TableRow key={reporterId} className='hover:bg-muted/30'>
                  <TableCell className='px-4 py-2.5 align-middle'>
                    <InstanceIdentity instance={instance} />
                  </TableCell>
                  <TableCell className='py-2.5 align-middle'>
                    <InstanceStatusBadge status={instance.status} />
                  </TableCell>
                  <TableCell className='py-2.5 align-middle'>
                    <InstanceRoleBadge instance={instance} />
                  </TableCell>
                  <TableCell className='py-2.5 align-middle'>
                    <ResourceCell value={resources?.cpu?.usage_percent} />
                  </TableCell>
                  <TableCell className='py-2.5 align-middle'>
                    <ResourceCell value={resources?.memory?.usage_percent} />
                  </TableCell>
                  <TableCell className='py-2.5 align-middle'>
                    <StorageCell instance={instance} />
                  </TableCell>
                  <TableCell className='py-2.5 align-middle'>
                    <div className='truncate font-mono text-xs'>
                      {instance.info?.runtime?.version || '-'}
                    </div>
                  </TableCell>
                  <TableCell className='py-2.5 align-middle'>
                    <div className='truncate font-mono text-xs'>
                      {runtimeLabel(instance)}
                    </div>
                  </TableCell>
                  <TableCell className='text-muted-foreground py-2.5 align-middle text-xs whitespace-nowrap'>
                    {formatTimestampToDate(instance.started_at)}
                  </TableCell>
                  <TableCell
                    className='text-muted-foreground py-2.5 align-middle text-xs whitespace-nowrap'
                    title={formatTimestampToDate(instance.last_seen_at)}
                  >
                    {formatTimestampRelative(
                      instance.last_seen_at,
                      'seconds',
                      toIntlLocale(i18n.language)
                    )}
                  </TableCell>
                  <TableCell className='py-2.5 pr-4 text-right align-middle'>
                    <div className='flex justify-end'>
                      <DeleteStaleInstanceButton
                        instance={instance}
                        isDeletingThisInstance={isDeletingThisInstance}
                        isDeletingInstance={props.isDeletingInstance}
                        onDeleteStaleInstance={props.onDeleteStaleInstance}
                      />
                    </div>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
    </>
  )
}

export function SystemInstancesPanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [deleteTarget, setDeleteTarget] = useState<SystemInstance | null>(null)
  const [deleteAllConfirmOpen, setDeleteAllConfirmOpen] = useState(false)
  const [deletingReporterId, setDeletingReporterId] = useState<string | null>(
    null
  )
  const instancesQuery = useQuery({
    queryKey: ['system-info', 'instances'],
    queryFn: async () => {
      const res = await listSystemInstances()
      if (!res.success || !Array.isArray(res.data)) {
        throw new Error(res.message || t('We could not load instances.'))
      }
      return res.data
    },
    staleTime: 30 * 1000,
    retry: false,
    refetchInterval: INSTANCE_POLL_INTERVAL_MS,
  })

  const instances = instancesQuery.data ?? []
  const staleInstances = instances.filter(
    (instance) => instance.status === 'stale'
  )
  const hasStaleInstances = staleInstances.length > 0
  const loading = instancesQuery.isLoading
  const refreshing = instancesQuery.isFetching && !instancesQuery.isLoading
  const fleetStats = summarizeFleet(instances)

  const invalidateInstances = async () => {
    await queryClient.invalidateQueries({
      queryKey: ['system-info', 'instances'],
    })
  }

  const deleteStaleInstanceMutation = useMutation({
    mutationFn: async (reporterId: string) => {
      const res = await deleteStaleSystemInstance(reporterId)
      if (!res.success) {
        throw new Error(res.message || t('Delete failed'))
      }
      return res
    },
    onMutate: (reporterId) => {
      setDeletingReporterId(reporterId)
    },
    onSuccess: async () => {
      toast.success(t('Deleted stale instance'))
      await invalidateInstances()
      setDeleteTarget(null)
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Delete failed'))
      void invalidateInstances()
    },
    onSettled: () => {
      setDeletingReporterId(null)
    },
  })

  const deleteStaleInstancesMutation = useMutation({
    mutationFn: async () => {
      const res = await deleteStaleSystemInstances()
      if (!res.success) {
        throw new Error(res.message || t('Delete failed'))
      }
      return res
    },
    onSuccess: async (res) => {
      toast.success(
        t('Deleted {{count}} stale instances', {
          count: res.data?.deleted_count ?? 0,
        })
      )
      await invalidateInstances()
      setDeleteAllConfirmOpen(false)
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Delete failed'))
    },
  })

  const isMutatingInstance =
    deletingReporterId !== null || deleteStaleInstanceMutation.isPending

  let instancesContent: ReactNode
  if (loading) {
    instancesContent = (
      <div className='space-y-2 p-4 sm:p-5'>
        {INSTANCE_SKELETON_KEYS.map((key) => (
          <Skeleton key={key} className='h-9 w-full rounded-md' />
        ))}
      </div>
    )
  } else if (instancesQuery.isError) {
    instancesContent = (
      <ErrorState
        title={t('We could not load instances.')}
        description={
          instancesQuery.error instanceof Error
            ? instancesQuery.error.message
            : undefined
        }
        onRetry={() => {
          void instancesQuery.refetch()
        }}
        className='min-h-[220px]'
      />
    )
  } else if (instances.length === 0) {
    instancesContent = (
      <EmptyState
        icon={ServerCog}
        title={t('No instances have reported yet.')}
        description={t(
          'Start the service on a node and it will appear here within a minute.'
        )}
        action={
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void instancesQuery.refetch()}
            disabled={instancesQuery.isFetching}
          >
            <RefreshCw
              data-icon='inline-start'
              className={cn('size-3.5', refreshing && 'animate-spin')}
              aria-hidden='true'
            />
            {t('Check again')}
          </Button>
        }
      />
    )
  } else {
    instancesContent = (
      <div className='p-4 sm:p-5'>
        <SystemInstancesList
          instances={instances}
          deletingReporterId={deletingReporterId}
          isDeletingInstance={
            isMutatingInstance || deleteStaleInstancesMutation.isPending
          }
          onDeleteStaleInstance={setDeleteTarget}
        />
      </div>
    )
  }

  return (
    <>
      <section className='bg-card overflow-hidden rounded-none border shadow-none'>
        <div className='flex flex-col gap-3 border-b px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5'>
          <div className='min-w-0'>
            <div className='flex items-center gap-2'>
              <span className='bg-muted text-muted-foreground inline-flex size-7 items-center justify-center rounded-md'>
                <ServerCog className='size-4' aria-hidden='true' />
              </span>
              <div className='min-w-0'>
                <h3 className='text-sm font-semibold'>{t('Instances')}</h3>
                <p className='text-muted-foreground mt-0.5 text-xs'>
                  {t(
                    'Runtime instances reporting from this deployment; slots on the same node are listed separately.'
                  )}
                </p>
              </div>
            </div>
          </div>
          <div className='flex shrink-0 flex-wrap items-center gap-2 sm:justify-end'>
            <span className='text-muted-foreground text-xs' aria-live='polite'>
              {t('Auto-refreshing every {{seconds}}s', {
                seconds: INSTANCE_POLL_INTERVAL_MS / 1000,
              })}
            </span>
            {hasStaleInstances ? (
              <Button
                type='button'
                variant='destructive'
                size='sm'
                onClick={() => setDeleteAllConfirmOpen(true)}
                disabled={
                  isMutatingInstance || deleteStaleInstancesMutation.isPending
                }
              >
                {deleteStaleInstancesMutation.isPending ? (
                  <Loader2
                    data-icon='inline-start'
                    className='size-3.5 animate-spin'
                    aria-hidden='true'
                  />
                ) : (
                  <Trash2
                    data-icon='inline-start'
                    className='size-3.5'
                    aria-hidden='true'
                  />
                )}
                {t('Delete all stale')}
              </Button>
            ) : null}
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => void instancesQuery.refetch()}
              disabled={instancesQuery.isFetching}
              aria-label={t('Refresh')}
            >
              <RefreshCw
                data-icon='inline-start'
                className={cn('size-3.5', refreshing && 'animate-spin')}
                aria-hidden='true'
              />
              {refreshing ? t('Refreshing...') : t('Refresh')}
            </Button>
          </div>
        </div>

        <div aria-busy={instancesQuery.isFetching}>
          {!loading && !instancesQuery.isError && instances.length > 0 ? (
            <FleetStatRow stats={fleetStats} />
          ) : null}
          {instancesContent}
        </div>
      </section>

      <ConfirmDialog
        open={deleteAllConfirmOpen}
        onOpenChange={setDeleteAllConfirmOpen}
        title={t('Delete stale instances')}
        desc={t(
          'Delete {{count}} stale instance records? Online instances will not be deleted.',
          { count: staleInstances.length }
        )}
        destructive
        isLoading={deleteStaleInstancesMutation.isPending}
        confirmText={
          deleteStaleInstancesMutation.isPending
            ? t('Deleting...')
            : t('Delete')
        }
        handleConfirm={() => deleteStaleInstancesMutation.mutate()}
      />

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
        title={t('Delete stale instance')}
        desc={
          deleteTarget
            ? t(
                'Delete stale instance "{{name}}"? If it has reported again, it will not be deleted.',
                { name: getInstanceDisplayName(deleteTarget) }
              )
            : ''
        }
        destructive
        isLoading={deleteStaleInstanceMutation.isPending}
        confirmText={
          deleteStaleInstanceMutation.isPending ? t('Deleting...') : t('Delete')
        }
        handleConfirm={() => {
          if (!deleteTarget) return
          deleteStaleInstanceMutation.mutate(getReporterId(deleteTarget))
        }}
      />
    </>
  )
}
