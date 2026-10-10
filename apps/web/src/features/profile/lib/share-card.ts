/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import {
  buildProfileActivityCells,
  PROFILE_ACTIVITY_WEEKS,
  type ProfileDailyUsage,
} from './activity'

export const PROFILE_SHARE_URL = 'https://api.lmm.best'
export const PROFILE_SHARE_CARD_WIDTH = 1200
export const PROFILE_SHARE_CARD_HEIGHT = 865

export interface ProfileShareCardContent {
  displayName: string
  username: string
  roleLabel: string
  totalTokens: string
  peakDailyTokens: string
  requestCount: string
  currentStreak: string
  longestStreak: string
  days: ProfileDailyUsage[]
  activityAvailable: boolean
  locale: string
  labels: {
    totalTokens: string
    activity: string
    activityUnavailable: string
    peakDailyTokens: string
    requestCount: string
    currentStreak: string
    longestStreak: string
  }
}

const COLOR = {
  background: '#202020',
  foreground: '#f3f3f3',
  muted: '#aaa9a8',
  border: '#363636',
  avatar: '#bcd1ca',
  levels: ['#303030', '#494949', '#626262', '#858585', '#b0b0b0', '#e3e3e3'],
} as const

function truncateToWidth(
  context: CanvasRenderingContext2D,
  value: string,
  maxWidth: number
): string {
  if (context.measureText(value).width <= maxWidth) return value
  const characters = Array.from(value)
  while (characters.length) {
    characters.pop()
    const result = `${characters.join('')}…`
    if (context.measureText(result).width <= maxWidth) return result
  }
  return '…'
}

function fillCenteredText(
  context: CanvasRenderingContext2D,
  value: string,
  x: number,
  y: number,
  maxWidth: number
) {
  context.fillText(truncateToWidth(context, value, maxWidth), x, y)
}

function drawAvatar(
  context: CanvasRenderingContext2D,
  displayName: string,
  image?: ImageBitmap
) {
  const x = 600
  const y = 136
  const radius = 70
  context.save()
  context.beginPath()
  context.arc(x, y, radius, 0, Math.PI * 2)
  context.clip()
  if (image) {
    const side = Math.min(image.width, image.height)
    context.drawImage(
      image,
      (image.width - side) / 2,
      (image.height - side) / 2,
      side,
      side,
      x - radius,
      y - radius,
      radius * 2,
      radius * 2
    )
  } else {
    context.fillStyle = COLOR.avatar
    context.fillRect(x - radius, y - radius, radius * 2, radius * 2)
    context.fillStyle = COLOR.background
    context.textAlign = 'center'
    context.font = '600 64px "Public Sans Variable", "Public Sans", sans-serif'
    context.fillText(
      Array.from(displayName.trim())[0]?.toUpperCase() ?? '?',
      x,
      y + 22
    )
  }
  context.restore()
}

function drawIdentity(
  context: CanvasRenderingContext2D,
  content: ProfileShareCardContent,
  avatar?: ImageBitmap
) {
  drawAvatar(context, content.displayName, avatar)
  context.textAlign = 'center'
  context.fillStyle = COLOR.foreground
  context.font = '500 42px "Public Sans Variable", "Public Sans", sans-serif'
  fillCenteredText(context, content.displayName, 600, 283, 800)

  context.font = '400 22px "Public Sans Variable", "Public Sans", sans-serif'
  const handle = `@${content.username}`
  const handleWidth = Math.min(360, context.measureText(handle).width)
  const roleWidth = Math.min(
    220,
    context.measureText(content.roleLabel).width + 26
  )
  const groupWidth = handleWidth + 18 + roleWidth
  const startX = (PROFILE_SHARE_CARD_WIDTH - groupWidth) / 2
  context.textAlign = 'left'
  context.fillStyle = COLOR.muted
  context.fillText(truncateToWidth(context, handle, 360), startX, 332)

  const roleX = startX + handleWidth + 18
  context.strokeStyle = COLOR.border
  context.lineWidth = 1
  context.beginPath()
  context.roundRect(roleX, 307, roleWidth, 36, 18)
  context.stroke()
  context.fillStyle = COLOR.muted
  context.textAlign = 'center'
  context.font = '400 18px "Public Sans Variable", "Public Sans", sans-serif'
  fillCenteredText(
    context,
    content.roleLabel,
    roleX + roleWidth / 2,
    331,
    roleWidth - 22
  )
}

function drawStats(
  context: CanvasRenderingContext2D,
  content: ProfileShareCardContent
) {
  const stats = [
    [content.totalTokens, content.labels.totalTokens],
    [content.peakDailyTokens, content.labels.peakDailyTokens],
    [content.requestCount, content.labels.requestCount],
    [content.currentStreak, content.labels.currentStreak],
    [content.longestStreak, content.labels.longestStreak],
  ]
  context.strokeStyle = COLOR.border
  context.lineWidth = 1
  context.beginPath()
  context.roundRect(40, 379, 1120, 112, 44)
  context.stroke()
  const width = 1120 / 5
  for (let index = 0; index < stats.length; index += 1) {
    const x = 40 + index * width + width / 2
    if (index > 0) {
      context.beginPath()
      context.moveTo(40 + index * width, 380)
      context.lineTo(40 + index * width, 490)
      context.stroke()
    }
    context.textAlign = 'center'
    context.fillStyle = COLOR.foreground
    context.font = '500 31px "Public Sans Variable", "Public Sans", sans-serif'
    fillCenteredText(context, stats[index][0], x, 429, width - 24)
    context.fillStyle = COLOR.muted
    context.font = '400 17px "Public Sans Variable", "Public Sans", sans-serif'
    fillCenteredText(context, stats[index][1], x, 463, width - 20)
  }
}

function drawActivity(
  context: CanvasRenderingContext2D,
  content: ProfileShareCardContent
) {
  context.textAlign = 'left'
  context.fillStyle = COLOR.foreground
  context.font = '500 28px "Public Sans Variable", "Public Sans", sans-serif'
  context.fillText(content.labels.activity, 40, 571)

  const cells = content.activityAvailable
    ? buildProfileActivityCells(content.days, 'daily')
    : []
  for (let week = 0; week < PROFILE_ACTIVITY_WEEKS; week += 1) {
    for (let day = 0; day < 7; day += 1) {
      const level = cells[week * 7 + day]?.level ?? 0
      context.fillStyle = COLOR.levels[level] ?? COLOR.levels[0]
      context.beginPath()
      context.roundRect(40 + week * 21, 607 + day * 21, 16, 16, 4)
      context.fill()
    }
  }

  if (content.activityAvailable) {
    const formatter = new Intl.DateTimeFormat(content.locale, {
      month: 'short',
    })
    let previousMonth = -1
    const months: { week: number; date: Date }[] = []
    context.fillStyle = COLOR.muted
    context.font = '400 15px "Public Sans Variable", "Public Sans", sans-serif'
    for (let week = 0; week < PROFILE_ACTIVITY_WEEKS; week += 1) {
      const date = content.days[week * 7 + 3]?.date
      if (!date || date.getMonth() === previousMonth) continue
      previousMonth = date.getMonth()
      months.push({ week, date })
    }
    if (months.length > 1 && months[1].week - months[0].week < 4) {
      months.shift()
    }
    for (const month of months) {
      context.fillText(formatter.format(month.date), 40 + month.week * 21, 785)
    }
  } else {
    context.fillStyle = COLOR.muted
    context.font = '400 17px "Public Sans Variable", "Public Sans", sans-serif'
    context.fillText(content.labels.activityUnavailable, 40, 785)
  }
}

export function drawProfileShareCard(
  canvas: HTMLCanvasElement,
  content: ProfileShareCardContent,
  avatar?: ImageBitmap
): void {
  canvas.width = PROFILE_SHARE_CARD_WIDTH
  canvas.height = PROFILE_SHARE_CARD_HEIGHT
  const context = canvas.getContext('2d')
  if (!context) throw new Error('Canvas is unavailable')
  context.fillStyle = COLOR.background
  context.fillRect(0, 0, canvas.width, canvas.height)
  drawIdentity(context, content, avatar)
  drawStats(context, content)
  drawActivity(context, content)

  context.textAlign = 'left'
  context.fillStyle = COLOR.muted
  context.font = '500 17px "Public Sans Variable", "Public Sans", sans-serif'
  context.fillText('LMM Best', 40, 834)
  context.textAlign = 'right'
  context.fillStyle = COLOR.foreground
  context.fillText(PROFILE_SHARE_URL, 1160, 834)
}

export function profileSharePostText(message: string): string {
  return `${message.trim()}\n${PROFILE_SHARE_URL}`
}

export function profileShareXIntent(message: string): string {
  const url = new URL('https://twitter.com/intent/tweet')
  url.searchParams.set('text', message.trim())
  url.searchParams.set('url', PROFILE_SHARE_URL)
  return url.toString()
}
